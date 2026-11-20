// Package redis runs one Redis command against a saved instance.
package redis

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"gatekeep/internal/plugin"
)

const poolName = "connection"

// Plugin is the Redis resource type.
type Plugin struct{}

func (Plugin) Info() plugin.Info {
	return plugin.Info{
		Type:  "redis",
		Label: "Redis",
		Fields: []plugin.Field{
			{Name: "host", Label: "Host"},
			{Name: "port", Label: "Port"},
			{Name: "database", Label: "Database"},
			{Name: "user", Label: "User", Optional: true},
			{Name: "password", Label: "Password", Secret: true, Optional: true},
		},
		ToolDescription: "Runs one Redis command. Cancel or timeout only closes this tool call's connection to Redis. Redis may keep doing heavy work on the server; there is no reliable server-side kill for most commands.",
		CommandDescription: "A JSON array of strings: the command name first, then each argument, for example [\"GET\",\"user:1001\"]. Same shape Redis uses on the wire. Avoid commands that can scan the whole keyspace or run long scripts.",
	}
}

func (Plugin) Test(ctx context.Context, config, secrets map[string]string) error {
	cfg, err := connSettings(config, secrets)
	if err != nil {
		return err
	}
	client, err := openClient(cfg)
	if err != nil {
		return clean(err, cfg.password)
	}
	defer client.Close()
	return clean(client.Ping(ctx).Err(), cfg.password)
}

// Execute runs one command. The client is kept in cache and closed after this
// resource sits idle. Cancelling the context ends the wait and drops the command
// connection. That does not stop server-side load.
func (Plugin) Execute(ctx context.Context, command string, config, secrets map[string]string, cache plugin.Cache) (any, error) {
	argv, err := parseCommand(command)
	if err != nil {
		return nil, err
	}
	cfg, err := connSettings(config, secrets)
	if err != nil {
		return nil, err
	}
	client, err := pooled(ctx, cfg, cache)
	if err != nil {
		return nil, clean(err, cfg.password)
	}
	stop := keepAlive(cache)
	defer stop()

	session := client.EphemeralConn()
	defer session.Close()

	raw, err := session.Do(ctx, toArgs(argv)...).Result()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, clean(dropIfBroken(ctx, cache, err), cfg.password)
	}
	return mapReply(raw)
}

type connConfig struct {
	host     string
	port     string
	database int
	user     string
	password string
	addr     string
}

func connSettings(config, secrets map[string]string) (connConfig, error) {
	host := strings.TrimSpace(config["host"])
	if host == "" {
		return connConfig{}, errors.New("host is required")
	}
	port := strings.TrimSpace(config["port"])
	if port == "" {
		port = "6379"
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return connConfig{}, fmt.Errorf("port must be between 1 and 65535")
	}
	dbText := strings.TrimSpace(config["database"])
	if dbText == "" {
		dbText = "0"
	}
	database, err := strconv.Atoi(dbText)
	if err != nil || database < 0 {
		return connConfig{}, errors.New("database must be a non-negative number")
	}
	user := strings.TrimSpace(config["user"])
	password := ""
	if secrets != nil {
		password = secrets["password"]
	}
	return connConfig{
		host:     host,
		port:     strconv.Itoa(number),
		database: database,
		user:     user,
		password: password,
		addr:     net.JoinHostPort(host, strconv.Itoa(number)),
	}, nil
}

func (cfg connConfig) key() string {
	return strings.Join([]string{cfg.addr, strconv.Itoa(cfg.database), cfg.user, cfg.password}, "\x00")
}

type cachedClient struct {
	key    string
	client *goredis.Client
}

func (c *cachedClient) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

func pooled(ctx context.Context, cfg connConfig, cache plugin.Cache) (*goredis.Client, error) {
	key := cfg.key()
	if current := currentClient(cache); current != nil {
		if current.key == key {
			if err := current.client.Ping(ctx).Err(); err == nil {
				return current.client, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		cache.Drop(poolName)
	}
	opened, err := openClient(cfg)
	if err != nil {
		return nil, err
	}
	if err := opened.Ping(ctx).Err(); err != nil {
		opened.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	stored, ok := cache.Update(poolName, func(current any, live bool) (any, bool) {
		if live {
			if existing, isClient := current.(*cachedClient); isClient && existing.key == key && existing.client != nil {
				return nil, false
			}
		}
		return &cachedClient{key: key, client: opened}, true
	})
	if !ok {
		opened.Close()
		return nil, errors.New("could not store the connection")
	}
	existing, isClient := stored.(*cachedClient)
	if !isClient || existing.client == nil {
		opened.Close()
		cache.Drop(poolName)
		return nil, errors.New("could not store the connection")
	}
	if existing.client != opened {
		opened.Close()
	}
	return existing.client, nil
}

func currentClient(cache plugin.Cache) *cachedClient {
	value, ok := cache.Update(poolName, func(any, bool) (any, bool) {
		return nil, false
	})
	if !ok {
		return nil
	}
	current, isClient := value.(*cachedClient)
	if !isClient || current.client == nil {
		cache.Drop(poolName)
		return nil
	}
	return current
}

func keepAlive(cache plugin.Cache) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				cache.Update(poolName, func(any, bool) (any, bool) { return nil, false })
			}
		}
	}()
	return func() { close(stop) }
}

func dropIfBroken(ctx context.Context, cache plugin.Cache, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() == nil && broken(err) {
		cache.Drop(poolName)
	}
	return err
}

func broken(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, goredis.Nil) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "broken pipe") ||
		strings.Contains(err.Error(), "EOF")
}

func openClient(cfg connConfig) (*goredis.Client, error) {
	options := &goredis.Options{
		Addr:         cfg.addr,
		DB:           cfg.database,
		Password:     cfg.password,
		DialTimeout:  10 * time.Second,
		ReadTimeout:  0,
		WriteTimeout: 0,
		PoolSize:     4,
		MinIdleConns: 1,
	}
	if cfg.user != "" {
		options.Username = cfg.user
	}
	return goredis.NewClient(options), nil
}

func clean(err error, secret string) error {
	if err == nil || secret == "" || !strings.Contains(err.Error(), secret) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "*****"))
}
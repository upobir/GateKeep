// Package mongodb runs one MongoDB command against a saved database.
package mongodb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"gatekeep/internal/plugin"
)

const poolName = "connection"

// Plugin is the MongoDB resource type.
type Plugin struct{}

func (Plugin) Info() plugin.Info {
	return plugin.Info{
		Type:  "mongodb",
		Label: "MongoDB",
		Fields: []plugin.Field{
			{Name: "host", Label: "Host"},
			{Name: "port", Label: "Port"},
			{Name: "database", Label: "Database"},
			{Name: "user", Label: "User"},
			{Name: "authDatabase", Label: "Auth database"},
			{Name: "password", Label: "Password", Secret: true},
		},
		ToolDescription:    "Runs one MongoDB command.",
		CommandDescription: "One MongoDB command document as extended JSON, run on the resource database. The first field is the command name, for example {\"find\":\"orders\",\"filter\":{\"status\":\"open\"},\"limit\":20}. A command that returns a cursor comes back as its documents.",
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
	defer disconnect(client)
	return clean(client.Ping(ctx, readpref.PrimaryPreferred()), cfg.password)
}

// Execute runs one command. The client is kept in cache and closed after this
// resource sits idle. Cancelling the context closes the socket the command is
// using. killOp then stops that connection's operation if the server still has it.
func (Plugin) Execute(ctx context.Context, command string, config, secrets map[string]string, cache plugin.Cache) (any, error) {
	parsed, err := parseCommand(command)
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

	track := &opTrack{}
	runCtx := context.WithValue(ctx, opKey{}, track)
	result, err := parsed.run(runCtx, client, cfg)
	if ctx.Err() != nil {
		if id, ok := track.id(); ok {
			killConnection(client, id)
		}
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, clean(dropIfBroken(ctx, cache, err), cfg.password)
	}
	return result, nil
}

type connConfig struct {
	host         string
	port         string
	database     string
	user         string
	password     string
	authDatabase string
	addr         string
}

func connSettings(config, secrets map[string]string) (connConfig, error) {
	host := strings.TrimSpace(config["host"])
	if host == "" {
		return connConfig{}, errors.New("host is required")
	}
	port := strings.TrimSpace(config["port"])
	if port == "" {
		port = "27017"
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return connConfig{}, fmt.Errorf("port must be between 1 and 65535")
	}
	database := strings.TrimSpace(config["database"])
	if database == "" {
		return connConfig{}, errors.New("database is required")
	}
	user := strings.TrimSpace(config["user"])
	if user == "" {
		return connConfig{}, errors.New("user is required")
	}
	authDatabase := strings.TrimSpace(config["authDatabase"])
	if authDatabase == "" {
		authDatabase = "admin"
	}
	password := ""
	if secrets != nil {
		password = secrets["password"]
	}
	return connConfig{
		host:         host,
		port:         strconv.Itoa(number),
		database:     database,
		user:         user,
		password:     password,
		authDatabase: authDatabase,
		addr:         net.JoinHostPort(host, strconv.Itoa(number)),
	}, nil
}

func (cfg connConfig) key() string {
	return strings.Join([]string{cfg.addr, cfg.database, cfg.user, cfg.authDatabase, cfg.password}, "\x00")
}

// cachedClient is one resource's client. key is the connection it was opened
// with, so an edited resource does not keep talking to the old database.
type cachedClient struct {
	key    string
	client *mongo.Client
}

func (c *cachedClient) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return disconnect(c.client)
}

func disconnect(client *mongo.Client) error {
	if client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return client.Disconnect(ctx)
}

func pooled(ctx context.Context, cfg connConfig, cache plugin.Cache) (*mongo.Client, error) {
	key := cfg.key()
	if current := currentClient(cache); current != nil {
		if current.key == key {
			if err := current.client.Ping(ctx, readpref.PrimaryPreferred()); err == nil {
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
	if err := opened.Ping(ctx, readpref.PrimaryPreferred()); err != nil {
		disconnect(opened)
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
		disconnect(opened)
		return nil, errors.New("could not store the connection")
	}
	existing, isClient := stored.(*cachedClient)
	if !isClient || existing.client == nil {
		disconnect(opened)
		cache.Drop(poolName)
		return nil, errors.New("could not store the connection")
	}
	if existing.client != opened {
		disconnect(opened)
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
	var command mongo.CommandError
	if errors.As(err, &command) {
		return false
	}
	var write mongo.WriteException
	if errors.As(err, &write) {
		return false
	}
	var bulk mongo.BulkWriteException
	if errors.As(err, &bulk) {
		return false
	}
	if errors.Is(err, mongo.ErrClientDisconnected) || mongo.IsNetworkError(err) || mongo.IsTimeout(err) {
		return true
	}
	return false
}

func openClient(cfg connConfig) (*mongo.Client, error) {
	// One configured host is the server to use. Direct skips replica-set discovery.
	// The pool stays small. Cancellation uses a second connection for killOp.
	return mongo.Connect(options.Client().
		SetHosts([]string{cfg.addr}).
		SetDirect(true).
		SetAuth(options.Credential{
			AuthSource:  cfg.authDatabase,
			Username:    cfg.user,
			Password:    cfg.password,
			PasswordSet: true,
		}).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second).
		SetMaxPoolSize(4).
		SetAppName("gatekeep").
		SetMonitor(commandMonitor))
}

func clean(err error, secret string) error {
	if err == nil || secret == "" || !strings.Contains(err.Error(), secret) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "*****"))
}

// opTrack records the server connection of the command running on this context.
// The monitor sees the same context the command was given.
type opTrack struct {
	mu     sync.Mutex
	connID int64
	set    bool
}

func (t *opTrack) note(id int64) {
	t.mu.Lock()
	t.connID = id
	t.set = true
	t.mu.Unlock()
}

func (t *opTrack) id() (int64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connID, t.set
}

type opKey struct{}

var commandMonitor = &event.CommandMonitor{
	Started: func(ctx context.Context, evt *event.CommandStartedEvent) {
		track, _ := ctx.Value(opKey{}).(*opTrack)
		if track == nil || evt == nil || evt.ServerConnectionID == nil {
			return
		}
		track.note(*evt.ServerConnectionID)
	},
}

// killConnection stops operations still running on the connection that was
// cancelled. The id is the server connection id from the command that was
// approved, so another command on this client is left alone.
func killConnection(client *mongo.Client, connID int64) {
	if client == nil || connID == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := client.Database("admin").RunCommand(ctx, bson.D{
		{Key: "currentOp", Value: true},
		{Key: "connectionId", Value: connID},
	}).Raw()
	if err != nil {
		return
	}
	ops, err := raw.LookupErr("inprog")
	if err != nil || ops.Type != bson.TypeArray {
		return
	}
	values, err := ops.Array().Values()
	if err != nil {
		return
	}
	for _, value := range values {
		if value.Type != bson.TypeEmbeddedDocument {
			continue
		}
		doc := value.Document()
		kind, err := doc.LookupErr("op")
		if err != nil || kind.Type != bson.TypeString || kind.StringValue() == "none" {
			continue
		}
		opid, err := doc.LookupErr("opid")
		if err != nil {
			continue
		}
		number, ok := opid.AsInt64OK()
		if !ok || number == 0 {
			continue
		}
		_ = client.Database("admin").RunCommand(ctx, bson.D{
			{Key: "killOp", Value: int32(1)},
			{Key: "op", Value: number},
		})
	}
}

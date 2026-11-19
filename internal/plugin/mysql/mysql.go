// Package mysql runs one MySQL statement against a saved database.
package mysql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"gatekeep/internal/plugin"
)

const poolName = "connection"

// Plugin is the MySQL resource type.
type Plugin struct{}

func (Plugin) Info() plugin.Info {
	return plugin.Info{
		Type:  "mysql",
		Label: "MySQL",
		Fields: []plugin.Field{
			{Name: "host", Label: "Host"},
			{Name: "port", Label: "Port"},
			{Name: "database", Label: "Database"},
			{Name: "user", Label: "User"},
			{Name: "password", Label: "Password", Secret: true},
		},
		ToolDescription:    "Runs one MySQL statement.",
		CommandDescription: "One MySQL statement. A statement that returns rows has the column names once and each row as an array.",
	}
}

func (Plugin) Test(ctx context.Context, config, secrets map[string]string) error {
	db, err := openDB(config, secrets)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

// Execute runs one statement. The connection pool is kept in cache and closed
// after this resource sits idle. Cancelling the context closes the connection
// the statement is using and KILL QUERY stops that session on the server.
func (Plugin) Execute(ctx context.Context, command string, config, secrets map[string]string, cache plugin.Cache) (any, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, errors.New("command is required")
	}
	db, err := pooled(ctx, config, secrets, cache)
	if err != nil {
		return nil, err
	}
	stop := keepAlive(cache)
	defer stop()

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, dropIfBroken(ctx, cache, err)
	}
	defer conn.Close()

	// The driver discards the handshake connection id. This session stays pinned
	// to conn, so the id still names the statement when the context is cancelled.
	id, err := connectionID(ctx, conn)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, dropIfBroken(ctx, cache, err)
	}

	rows, err := conn.QueryContext(ctx, command)
	if err != nil {
		if stop := stopped(ctx, config, secrets, id); stop != nil {
			return nil, stop
		}
		return nil, dropIfBroken(ctx, cache, err)
	}
	result, err := readQuery(rows)
	closeErr := rows.Close()
	if stop := stopped(ctx, config, secrets, id); stop != nil {
		return nil, stop
	}
	if err != nil {
		return nil, dropIfBroken(ctx, cache, err)
	}
	if closeErr != nil {
		return nil, dropIfBroken(ctx, cache, closeErr)
	}
	return result, nil
}

// cachedPool is one resource's connection pool. key is the connection it was
// opened with, so an edited resource does not keep talking to the old database.
type cachedPool struct {
	key string
	db  *sql.DB
}

func (p *cachedPool) Close() error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Close()
}

func pooled(ctx context.Context, config, secrets map[string]string, cache plugin.Cache) (*sql.DB, error) {
	key, err := poolKey(config, secrets)
	if err != nil {
		return nil, err
	}
	if pool := currentPool(cache); pool != nil {
		if pool.key == key {
			if err := pool.db.PingContext(ctx); err == nil {
				return pool.db, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		cache.Drop(poolName)
	}
	opened, err := openDB(config, secrets)
	if err != nil {
		return nil, err
	}
	if err := opened.PingContext(ctx); err != nil {
		opened.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	stored, ok := cache.Update(poolName, func(current any, live bool) (any, bool) {
		if live {
			if existing, isPool := current.(*cachedPool); isPool && existing.key == key && existing.db != nil {
				return nil, false
			}
		}
		return &cachedPool{key: key, db: opened}, true
	})
	if !ok {
		opened.Close()
		return nil, errors.New("could not store the connection")
	}
	existing, isPool := stored.(*cachedPool)
	if !isPool || existing.db == nil {
		opened.Close()
		cache.Drop(poolName)
		return nil, errors.New("could not store the connection")
	}
	if existing.db != opened {
		opened.Close()
	}
	return existing.db, nil
}

func currentPool(cache plugin.Cache) *cachedPool {
	value, ok := cache.Update(poolName, func(current any, live bool) (any, bool) {
		return nil, false
	})
	if !ok {
		return nil
	}
	pool, isPool := value.(*cachedPool)
	if !isPool || pool.db == nil {
		cache.Drop(poolName)
		return nil
	}
	return pool
}

func poolKey(config, secrets map[string]string) (string, error) {
	cfg, err := driverConfig(config, secrets)
	if err != nil {
		return "", err
	}
	return cfg.FormatDSN(), nil
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

func connectionID(ctx context.Context, conn *sql.Conn) (uint64, error) {
	var id uint64
	err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id)
	return id, err
}

// stopped kills the server session when this call was cancelled or timed out.
// The id is the integer from CONNECTION_ID, and the kill uses its own connection
// so it is not blocked on the session that is being stopped. The pool stays.
func stopped(ctx context.Context, config, secrets map[string]string, id uint64) error {
	if ctx.Err() == nil {
		return nil
	}
	killQuery(config, secrets, id)
	return ctx.Err()
}

func killQuery(config, secrets map[string]string, id uint64) {
	if id == 0 {
		return
	}
	db, err := openDB(config, secrets)
	if err != nil {
		return
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	killCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = db.ExecContext(killCtx, "KILL QUERY "+strconv.FormatUint(id, 10))
}

func broken(err error) bool {
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, mysql.ErrInvalidConn) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		return my.Number == 2006 || my.Number == 2013
	}
	return false
}

func openDB(config, secrets map[string]string) (*sql.DB, error) {
	cfg, err := driverConfig(config, secrets)
	if err != nil {
		return nil, err
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	// A few idle connections stay ready for the next statement on this resource.
	// Cancellation dials its own connection, so it does not wait on this pool.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	return db, nil
}

func driverConfig(config, secrets map[string]string) (*mysql.Config, error) {
	host := strings.TrimSpace(config["host"])
	if host == "" {
		return nil, errors.New("host is required")
	}
	port := strings.TrimSpace(config["port"])
	if port == "" {
		port = "3306"
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	user := strings.TrimSpace(config["user"])
	if user == "" {
		return nil, errors.New("user is required")
	}
	database := strings.TrimSpace(config["database"])
	if database == "" {
		return nil, errors.New("database is required")
	}
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = secrets["password"]
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, strconv.Itoa(number))
	cfg.DBName = database
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Timeout = 10 * time.Second
	cfg.CheckConnLiveness = true
	// Params would be sent as system variables. Charset is a connection option.
	if err := cfg.Apply(mysql.Charset("utf8mb4", "")); err != nil {
		return nil, err
	}
	return cfg, nil
}

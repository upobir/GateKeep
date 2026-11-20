// Package postgres runs one PostgreSQL statement against a saved database.
package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"gatekeep/internal/plugin"
)

const poolName = "connection"

// Plugin is the PostgreSQL resource type.
type Plugin struct{}

func (Plugin) Info() plugin.Info {
	return plugin.Info{
		Type:  "postgres",
		Label: "PostgreSQL",
		Fields: []plugin.Field{
			{Name: "host", Label: "Host"},
			{Name: "port", Label: "Port"},
			{Name: "database", Label: "Database"},
			{Name: "user", Label: "User"},
			{Name: "password", Label: "Password", Secret: true},
		},
		ToolDescription:    "Runs one PostgreSQL statement.",
		CommandDescription: "One PostgreSQL statement. A statement that returns rows has the column names once and each row as an array.",
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
// the statement is using and pg_cancel_backend stops that session on the server.
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

	// The backend pid names this session on the server while conn stays pinned.
	pid, err := backendPID(ctx, conn)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, dropIfBroken(ctx, cache, err)
	}

	rows, err := conn.QueryContext(ctx, command)
	if err != nil {
		if stop := stopped(ctx, config, secrets, pid); stop != nil {
			return nil, stop
		}
		return nil, dropIfBroken(ctx, cache, err)
	}
	result, err := readQuery(rows)
	closeErr := rows.Close()
	if stop := stopped(ctx, config, secrets, pid); stop != nil {
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
	return cfg.ConnString(), nil
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

func backendPID(ctx context.Context, conn *sql.Conn) (int32, error) {
	var pid int32
	err := conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid)
	return pid, err
}

// stopped cancels the server session when this call was cancelled or timed out.
// The pid comes from pg_backend_pid, and the cancel uses its own connection so
// it is not blocked on the session that is being stopped. The pool stays.
func stopped(ctx context.Context, config, secrets map[string]string, pid int32) error {
	if ctx.Err() == nil {
		return nil
	}
	cancelQuery(config, secrets, pid)
	return ctx.Err()
}

func cancelQuery(config, secrets map[string]string, pid int32) {
	if pid == 0 {
		return
	}
	db, err := openDB(config, secrets)
	if err != nil {
		return
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = db.ExecContext(cancelCtx, "SELECT pg_cancel_backend($1)", pid)
}

func broken(err error) bool {
	if errors.Is(err, driver.ErrBadConn) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "08000", "08001", "08003", "08004", "08006", "57P01", "57P02", "57P03":
			return true
		}
	}
	return false
}

func openDB(config, secrets map[string]string) (*sql.DB, error) {
	cfg, err := driverConfig(config, secrets)
	if err != nil {
		return nil, err
	}
	db := stdlib.OpenDB(*cfg)
	// A few idle connections stay ready for the next statement on this resource.
	// Cancellation dials its own connection, so it does not wait on this pool.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	return db, nil
}

func driverConfig(config, secrets map[string]string) (*pgx.ConnConfig, error) {
	host := strings.TrimSpace(config["host"])
	if host == "" {
		return nil, errors.New("host is required")
	}
	port := strings.TrimSpace(config["port"])
	if port == "" {
		port = "5432"
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
	password := secrets["password"]
	link := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, strconv.Itoa(number)),
		Path:   "/" + database,
	}
	query := link.Query()
	query.Set("sslmode", "disable")
	link.RawQuery = query.Encode()
	cfg, err := pgx.ParseConfig(link.String())
	if err != nil {
		return nil, err
	}
	cfg.ConnectTimeout = 10 * time.Second
	return cfg, nil
}
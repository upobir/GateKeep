// Package clickhouse runs one ClickHouse statement against a saved database.
package clickhouse

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"gatekeep/internal/plugin"
)

const poolName = "connection"

// Plugin is the ClickHouse resource type. It uses the HTTP interface because
// the driver's native Query fails on statements that send no rows, such as DDL
// and INSERT, while over HTTP one Query call handles every statement.
type Plugin struct{}

func (Plugin) Info() plugin.Info {
	return plugin.Info{
		Type:  "clickhouse",
		Label: "ClickHouse",
		Fields: []plugin.Field{
			{Name: "host", Label: "Host"},
			{Name: "port", Label: "Port"},
			{Name: "database", Label: "Database"},
			{Name: "user", Label: "User"},
			{Name: "password", Label: "Password", Secret: true},
			{Name: "tls", Label: "TLS", Optional: true},
		},
		ToolDescription:    "Runs one ClickHouse statement.",
		CommandDescription: "One ClickHouse SQL statement. A statement that returns rows has the column names once and each row as an array.",
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
// after this resource sits idle. The statement runs under a query id of its
// own, so when the context is cancelled KILL QUERY stops it on the server.
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

	queryID, err := newQueryID()
	if err != nil {
		return nil, err
	}
	queryCtx := clickhouse.Context(ctx, clickhouse.WithQueryID(queryID))

	rows, err := db.QueryContext(queryCtx, command)
	if err != nil {
		if stop := stopped(ctx, config, secrets, queryID); stop != nil {
			return nil, stop
		}
		return nil, dropIfBroken(ctx, cache, err)
	}
	result, err := readQuery(rows)
	closeErr := rows.Close()
	if stop := stopped(ctx, config, secrets, queryID); stop != nil {
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

// poolKey names the connection. The password is part of it so a changed
// password opens a new pool, but it never leaves this process.
func poolKey(config, secrets map[string]string) (string, error) {
	opts, err := driverOptions(config, secrets)
	if err != nil {
		return "", err
	}
	mode, _ := tlsMode(config)
	return strings.Join([]string{opts.Addr[0], opts.Auth.Database, opts.Auth.Username, opts.Auth.Password, mode}, "\x00"), nil
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

func newQueryID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "gatekeep-" + hex.EncodeToString(raw[:]), nil
}

// stopped kills the statement on the server when this call was cancelled or
// timed out. The query id is ours, and the kill uses its own connection so it
// is not blocked on the statement that is being stopped. The pool stays.
func stopped(ctx context.Context, config, secrets map[string]string, queryID string) error {
	if ctx.Err() == nil {
		return nil
	}
	killQuery(config, secrets, queryID)
	return ctx.Err()
}

func killQuery(config, secrets map[string]string, queryID string) {
	if queryID == "" {
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
	// queryID is hex from newQueryID, so it is safe to quote directly.
	_, _ = db.ExecContext(killCtx, "KILL QUERY WHERE query_id = '"+queryID+"'")
}

func broken(err error) bool {
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var exception *clickhouse.Exception
	if errors.As(err, &exception) {
		// NETWORK_ERROR, SOCKET_TIMEOUT, ALL_CONNECTION_TRIES_FAILED
		switch exception.Code {
		case 209, 210, 279:
			return true
		}
	}
	return false
}

func openDB(config, secrets map[string]string) (*sql.DB, error) {
	opts, err := driverOptions(config, secrets)
	if err != nil {
		return nil, err
	}
	db := clickhouse.OpenDB(opts)
	// A few idle connections stay ready for the next statement on this resource.
	// Cancellation dials its own connection, so it does not wait on this pool.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	return db, nil
}

func driverOptions(config, secrets map[string]string) (*clickhouse.Options, error) {
	host := strings.TrimSpace(config["host"])
	if host == "" {
		return nil, errors.New("host is required")
	}
	port := strings.TrimSpace(config["port"])
	if port == "" {
		port = "8123"
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
	mode, err := tlsMode(config)
	if err != nil {
		return nil, err
	}
	// With TLS set the driver talks HTTPS. Go fills in the server name.
	var tlsConfig *tls.Config
	switch mode {
	case "on":
		tlsConfig = &tls.Config{}
	case "skip-verify":
		tlsConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &clickhouse.Options{
		TLS:      tlsConfig,
		Protocol: clickhouse.HTTP,
		Addr:     []string{net.JoinHostPort(host, strconv.Itoa(number))},
		Auth: clickhouse.Auth{
			Database: database,
			Username: user,
			Password: secrets["password"],
		},
		DialTimeout: 10 * time.Second,
	}, nil
}

// tlsMode reads the optional tls field. Empty means off, so resources saved
// before this option existed connect the way they did.
func tlsMode(config map[string]string) (string, error) {
	switch mode := config["tls"]; mode {
	case "", "off":
		return "off", nil
	case "on", "skip-verify":
		return mode, nil
	default:
		return "", errors.New("tls must be off, on or skip-verify")
	}
}

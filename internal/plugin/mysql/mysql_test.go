package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"gatekeep/internal/cache"
)

func TestCell(t *testing.T) {
	when := time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC)
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	if got := cell([]byte("Ada"), "VARCHAR"); got != "Ada" {
		t.Fatalf("text = %#v", got)
	}
	if got := cell([]byte{0, 1}, "BIT"); got != uint64(1) {
		t.Fatalf("bit = %#v", got)
	}
	if got := cell([]byte(`{"n":1}`), "JSON"); got.(map[string]any)["n"] != float64(1) {
		t.Fatalf("json = %#v", got)
	}
	if got := cell(nil, "VARCHAR"); got != nil {
		t.Fatalf("null = %#v", got)
	}
	if got := cell(when, "DATETIME"); got != "2026-11-20 01:02:03" {
		t.Fatalf("datetime = %#v", got)
	}
	if got := cell(day, "DATE"); got != "2026-11-20" {
		t.Fatalf("date = %#v", got)
	}
	if got := cell(int64(7), "BIGINT"); got != int64(7) {
		t.Fatalf("int = %#v", got)
	}
}

func TestDriverConfig(t *testing.T) {
	cfg, err := driverConfig(map[string]string{
		"host": "db.internal", "port": "3307", "database": "orders", "user": "reader",
	}, map[string]string{"password": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "db.internal:3307" || cfg.DBName != "orders" || cfg.User != "reader" || cfg.Passwd != "secret" {
		t.Fatalf("config = %+v", cfg)
	}
	if _, err := driverConfig(map[string]string{"host": "db", "port": "0", "database": "orders", "user": "reader"}, nil); err == nil {
		t.Fatal("expected an invalid port to fail")
	}
	if _, err := driverConfig(map[string]string{"port": "3306", "database": "orders", "user": "reader"}, nil); err == nil {
		t.Fatal("expected a missing host to fail")
	}
}

func TestDriverConfigTLS(t *testing.T) {
	base := map[string]string{"host": "db.internal", "port": "3306", "database": "orders", "user": "reader"}
	cases := map[string]string{"": "tls=false", "off": "tls=false", "on": "tls=true", "skip-verify": "tls=skip-verify"}
	for mode, want := range cases {
		config := map[string]string{"tls": mode}
		for key, value := range base {
			config[key] = value
		}
		cfg, err := driverConfig(config, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(cfg.FormatDSN(), want) {
			t.Fatalf("tls %q = %s", mode, cfg.FormatDSN())
		}
	}
	base["tls"] = "maybe"
	if _, err := driverConfig(base, nil); err == nil {
		t.Fatal("expected an unknown tls mode to fail")
	}
}

// The MySQL 8 image makes a self-signed certificate, so skip-verify connects
// over TLS and on refuses the certificate.
func TestLiveMySQLTLS(t *testing.T) {
	config := map[string]string{
		"host":     env("GATEKEEP_MYSQL_HOST", "127.0.0.1"),
		"port":     env("GATEKEEP_MYSQL_PORT", "3306"),
		"database": env("GATEKEEP_MYSQL_DATABASE", "mysql"),
		"user":     env("GATEKEEP_MYSQL_USER", "root"),
	}
	secrets := map[string]string{"password": env("GATEKEEP_MYSQL_PASSWORD", "example")}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := (Plugin{}).Test(ctx, config, secrets); err != nil {
		t.Skipf("mysql is not available: %v", err)
	}
	config["tls"] = "skip-verify"
	result, err := Plugin{}.Execute(context.Background(), "SHOW STATUS LIKE 'Ssl_cipher'", config, secrets, cache.New().Handle(1, time.Hour))
	if err != nil {
		t.Skipf("server has no TLS: %v", err)
	}
	rows := result.(map[string]any)["rows"].([][]any)
	if len(rows) != 1 || rows[0][1] == "" {
		t.Fatalf("not encrypted: %v", rows)
	}
	config["tls"] = "on"
	if err := (Plugin{}).Test(context.Background(), config, secrets); err == nil {
		t.Fatal("expected a self-signed certificate to be refused")
	}
}

func TestExecuteRequiresCommand(t *testing.T) {
	_, err := Plugin{}.Execute(context.Background(), "  ", nil, nil, cache.New().Handle(1, time.Hour))
	if err == nil || err.Error() != "command is required" {
		t.Fatal(err)
	}
}

func TestLiveMySQL(t *testing.T) {
	config := map[string]string{
		"host":     env("GATEKEEP_MYSQL_HOST", "127.0.0.1"),
		"port":     env("GATEKEEP_MYSQL_PORT", "3306"),
		"database": env("GATEKEEP_MYSQL_DATABASE", "mysql"),
		"user":     env("GATEKEEP_MYSQL_USER", "root"),
	}
	password := env("GATEKEEP_MYSQL_PASSWORD", "example")
	secrets := map[string]string{"password": password}
	p := Plugin{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Test(ctx, config, secrets); err != nil {
		t.Skipf("mysql is not available: %v", err)
	}
	if err := p.Test(context.Background(), config, map[string]string{"password": password + "-no"}); err == nil || strings.Contains(err.Error(), password) {
		t.Fatalf("bad password error = %v", err)
	}

	mem := cache.New()
	handle := mem.Handle(1, time.Hour)
	result, err := p.Execute(context.Background(), "SELECT 1 AS id, 'Ada' AS name, NULL AS note", config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"columns":["id","name","note"],"rows":[[1,"Ada",null]]}` {
		t.Fatalf("select = %s", raw)
	}
	again, err := p.Execute(context.Background(), "SELECT 2 AS id", config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(again)
	if string(raw) != `{"columns":["id"],"rows":[[2]]}` {
		t.Fatalf("second select = %s", raw)
	}
	if len(mem.List(1, time.Hour, time.Now())) != 1 {
		t.Fatal("connection was not reused from the cache")
	}
	if _, err := p.Execute(context.Background(), "SELECT missing_column_gatekeep", config, secrets, handle); err == nil {
		t.Fatal("expected an invalid statement to fail")
	}

	const scratch = "gatekeep_driver_test"
	if _, err := p.Execute(context.Background(), "CREATE DATABASE IF NOT EXISTS "+scratch, config, secrets, handle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = p.Execute(context.Background(), "DROP DATABASE IF EXISTS "+scratch, config, secrets, handle)
	})
	scratchConfig := map[string]string{}
	for key, value := range config {
		scratchConfig[key] = value
	}
	scratchConfig["database"] = scratch
	where, err := p.Execute(context.Background(), "SELECT DATABASE() AS db", scratchConfig, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(where)
	if string(raw) != `{"columns":["db"],"rows":[["gatekeep_driver_test"]]}` {
		t.Fatalf("database = %s", raw)
	}
	if _, err := p.Execute(context.Background(), "CREATE TABLE probe (id INT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(20))", scratchConfig, secrets, handle); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Execute(context.Background(), "INSERT INTO probe (name) VALUES ('Ada')", scratchConfig, secrets, handle); err != nil {
		t.Fatal(err)
	}
	selected, err := p.Execute(context.Background(), "SELECT id, name FROM probe", scratchConfig, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(selected)
	if string(raw) != `{"columns":["id","name"],"rows":[[1,"Ada"]]}` {
		t.Fatalf("probe = %s", raw)
	}

	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Execute(runCtx, "SELECT SLEEP(30)", config, secrets, handle)
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SLEEP did not stop when the context was cancelled")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		listed, err := p.Execute(context.Background(), "SELECT INFO FROM information_schema.PROCESSLIST WHERE INFO LIKE 'SELECT SLEEP(%'", config, secrets, handle)
		if err != nil {
			t.Fatal(err)
		}
		rows, _ := listed.(map[string]any)["rows"].([][]any)
		if len(rows) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("SLEEP still running after cancel: %v", rows)
		}
		time.Sleep(50 * time.Millisecond)
	}
	followed, err := p.Execute(context.Background(), "SELECT 3 AS id", config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(followed)
	if string(raw) != `{"columns":["id"],"rows":[[3]]}` {
		t.Fatalf("query after cancel = %s", raw)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

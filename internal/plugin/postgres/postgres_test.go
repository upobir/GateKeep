package postgres

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
	if got := cell([]byte("Ada"), "TEXT"); got != "Ada" {
		t.Fatalf("text = %#v", got)
	}
	if got := cell([]byte{0, 1}, "BIT"); got != uint64(1) {
		t.Fatalf("bit = %#v", got)
	}
	if got := cell([]byte(`{"n":1}`), "JSONB"); got.(map[string]any)["n"] != float64(1) {
		t.Fatalf("json = %#v", got)
	}
	if got := cell(nil, "TEXT"); got != nil {
		t.Fatalf("null = %#v", got)
	}
	if got := cell(when, "TIMESTAMP"); got != "2026-11-20 01:02:03" {
		t.Fatalf("timestamp = %#v", got)
	}
	if got := cell(day, "DATE"); got != "2026-11-20" {
		t.Fatalf("date = %#v", got)
	}
	if got := cell(int64(7), "INT8"); got != int64(7) {
		t.Fatalf("int = %#v", got)
	}
}

func TestDriverConfig(t *testing.T) {
	cfg, err := driverConfig(map[string]string{
		"host": "db.internal", "port": "5433", "database": "orders", "user": "reader",
	}, map[string]string{"password": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "db.internal" || cfg.Port != 5433 || cfg.Database != "orders" || cfg.User != "reader" || cfg.Password != "secret" {
		t.Fatalf("config = %+v", cfg)
	}
	if _, err := driverConfig(map[string]string{"host": "db", "port": "0", "database": "orders", "user": "reader"}, nil); err == nil {
		t.Fatal("expected an invalid port to fail")
	}
	if _, err := driverConfig(map[string]string{"port": "5432", "database": "orders", "user": "reader"}, nil); err == nil {
		t.Fatal("expected a missing host to fail")
	}
}

func TestDriverConfigTLS(t *testing.T) {
	base := map[string]string{"host": "db.internal", "port": "5432", "database": "orders", "user": "reader"}
	cases := map[string]string{"": "sslmode=disable", "off": "sslmode=disable", "on": "sslmode=verify-full", "skip-verify": "sslmode=require"}
	for mode, want := range cases {
		config := map[string]string{"tls": mode}
		for key, value := range base {
			config[key] = value
		}
		cfg, err := driverConfig(config, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(cfg.ConnString(), want) {
			t.Fatalf("tls %q = %s", mode, cfg.ConnString())
		}
		if (mode == "on" || mode == "skip-verify") != (cfg.TLSConfig != nil) {
			t.Fatalf("tls %q config = %v", mode, cfg.TLSConfig)
		}
	}
	base["tls"] = "maybe"
	if _, err := driverConfig(base, nil); err == nil {
		t.Fatal("expected an unknown tls mode to fail")
	}
}

func TestExecuteRequiresCommand(t *testing.T) {
	_, err := Plugin{}.Execute(context.Background(), "  ", nil, nil, cache.New().Handle(1, time.Hour))
	if err == nil || err.Error() != "command is required" {
		t.Fatal(err)
	}
}

func TestLivePostgres(t *testing.T) {
	config := map[string]string{
		"host":     env("GATEKEEP_POSTGRES_HOST", "127.0.0.1"),
		"port":     env("GATEKEEP_POSTGRES_PORT", "5432"),
		"database": env("GATEKEEP_POSTGRES_DATABASE", "postgres"),
		"user":     env("GATEKEEP_POSTGRES_USER", "postgres"),
	}
	password := env("GATEKEEP_POSTGRES_PASSWORD", "postgres")
	secrets := map[string]string{"password": password}
	p := Plugin{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Test(ctx, config, secrets); err != nil {
		t.Skipf("postgres is not available: %v", err)
	}
	wrong := password + "-no"
	if err := p.Test(context.Background(), config, map[string]string{"password": wrong}); err == nil || strings.Contains(err.Error(), wrong) {
		t.Fatalf("bad password error = %v", err)
	}

	mem := cache.New()
	handle := mem.Handle(1, time.Hour)
	result, err := p.Execute(context.Background(), "SELECT 1 AS id, 'Ada' AS name, NULL::text AS note", config, secrets, handle)
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
	if _, err := p.Execute(context.Background(), "CREATE DATABASE "+scratch, config, secrets, handle); err != nil {
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
	where, err := p.Execute(context.Background(), "SELECT current_database() AS db", scratchConfig, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(where)
	if string(raw) != `{"columns":["db"],"rows":[["gatekeep_driver_test"]]}` {
		t.Fatalf("database = %s", raw)
	}
	if _, err := p.Execute(context.Background(), "CREATE TABLE probe (id SERIAL PRIMARY KEY, name VARCHAR(20))", scratchConfig, secrets, handle); err != nil {
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
		_, err := p.Execute(runCtx, "SELECT pg_sleep(30)", config, secrets, handle)
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
		t.Fatal("pg_sleep did not stop when the context was cancelled")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		listed, err := p.Execute(context.Background(), "SELECT query FROM pg_stat_activity WHERE query LIKE 'SELECT pg_sleep(%' AND pid <> pg_backend_pid()", config, secrets, handle)
		if err != nil {
			t.Fatal(err)
		}
		rows, _ := listed.(map[string]any)["rows"].([][]any)
		if len(rows) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pg_sleep still running after cancel: %v", rows)
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

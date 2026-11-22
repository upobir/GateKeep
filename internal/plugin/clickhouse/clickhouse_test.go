package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"gatekeep/internal/cache"
)

func TestCell(t *testing.T) {
	when := time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC)
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	if got := cell("Ada", "String"); got != "Ada" {
		t.Fatalf("text = %#v", got)
	}
	if got := cell(nil, "Nullable(String)"); got != nil {
		t.Fatalf("null = %#v", got)
	}
	if got := cell("\xff\x00", "String"); got != "/wA=" {
		t.Fatalf("binary = %#v", got)
	}
	if got := cell(when, "DateTime"); got != "2026-11-20 01:02:03" {
		t.Fatalf("datetime = %#v", got)
	}
	if got := cell(day, "Nullable(Date)"); got != "2026-11-20" {
		t.Fatalf("date = %#v", got)
	}
	if got := cell(uint64(7), "UInt64"); got != uint64(7) {
		t.Fatalf("int = %#v", got)
	}
	raw, _ := json.Marshal([]any{cell([]uint8{1, 2}, "Array(UInt8)"), cell(*big.NewInt(7), "Int128")})
	if string(raw) != `[[1,2],7]` {
		t.Fatalf("array and int128 = %s", raw)
	}
}

func TestDriverOptions(t *testing.T) {
	opts, err := driverOptions(map[string]string{
		"host": "db.internal", "port": "9001", "database": "events", "user": "reader",
	}, map[string]string{"password": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Addr[0] != "db.internal:9001" || opts.Auth.Database != "events" || opts.Auth.Username != "reader" || opts.Auth.Password != "secret" {
		t.Fatalf("options = %+v", opts)
	}
	if _, err := driverOptions(map[string]string{"host": "db", "port": "0", "database": "events", "user": "reader"}, nil); err == nil {
		t.Fatal("expected an invalid port to fail")
	}
	if _, err := driverOptions(map[string]string{"port": "8123", "database": "events", "user": "reader"}, nil); err == nil {
		t.Fatal("expected a missing host to fail")
	}
	opts, err = driverOptions(map[string]string{"host": "db", "database": "events", "user": "reader"}, nil)
	if err != nil || opts.Addr[0] != "db:8123" {
		t.Fatalf("default port = %v, %v", opts, err)
	}
}

func TestDriverOptionsTLS(t *testing.T) {
	base := map[string]string{"host": "db.internal", "port": "8443", "database": "events", "user": "reader"}
	for _, mode := range []string{"", "off", "on", "skip-verify"} {
		config := map[string]string{"tls": mode}
		for key, value := range base {
			config[key] = value
		}
		opts, err := driverOptions(config, nil)
		if err != nil {
			t.Fatal(err)
		}
		encrypted := mode == "on" || mode == "skip-verify"
		if encrypted != (opts.TLS != nil) || (opts.TLS != nil && opts.TLS.InsecureSkipVerify != (mode == "skip-verify")) {
			t.Fatalf("tls %q = %+v", mode, opts.TLS)
		}
	}
	base["tls"] = "maybe"
	if _, err := driverOptions(base, nil); err == nil {
		t.Fatal("expected an unknown tls mode to fail")
	}
}

func TestExecuteRequiresCommand(t *testing.T) {
	_, err := Plugin{}.Execute(context.Background(), "  ", nil, nil, cache.New().Handle(1, time.Hour))
	if err == nil || err.Error() != "command is required" {
		t.Fatal(err)
	}
}

func TestLiveClickHouse(t *testing.T) {
	config := map[string]string{
		"host":     env("GATEKEEP_CLICKHOUSE_HOST", "127.0.0.1"),
		"port":     env("GATEKEEP_CLICKHOUSE_PORT", "8123"),
		"database": env("GATEKEEP_CLICKHOUSE_DATABASE", "default"),
		"user":     env("GATEKEEP_CLICKHOUSE_USER", "default"),
	}
	password := env("GATEKEEP_CLICKHOUSE_PASSWORD", "example")
	secrets := map[string]string{"password": password}
	p := Plugin{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Test(ctx, config, secrets); err != nil {
		t.Skipf("clickhouse is not available: %v", err)
	}
	wrong := password + "-no"
	if err := p.Test(context.Background(), config, map[string]string{"password": wrong}); err == nil || strings.Contains(err.Error(), wrong) {
		t.Fatalf("bad password error = %v", err)
	}

	mem := cache.New()
	handle := mem.Handle(1, time.Hour)
	result, err := p.Execute(context.Background(), "SELECT toUInt8(1) AS id, 'Ada' AS name, CAST(NULL AS Nullable(String)) AS note", config, secrets, handle)
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
	where, err := p.Execute(context.Background(), "SELECT currentDatabase() AS db", scratchConfig, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(where)
	if string(raw) != `{"columns":["db"],"rows":[["gatekeep_driver_test"]]}` {
		t.Fatalf("database = %s", raw)
	}
	if _, err := p.Execute(context.Background(), "CREATE TABLE probe (id UInt32, name String, at DateTime, day Nullable(Date)) ENGINE = MergeTree ORDER BY id", scratchConfig, secrets, handle); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Execute(context.Background(), "INSERT INTO probe VALUES (1, 'Ada', '2026-11-20 01:02:03', '2026-11-20'), (2, 'Bob', '2026-11-21 00:00:00', NULL)", scratchConfig, secrets, handle); err != nil {
		t.Fatal(err)
	}
	typed, err := p.Execute(context.Background(), `SELECT '{"a":1}'::JSON AS j, toInt128(7) AS big, [1, 2] AS arr, toDecimal64(1.5, 2) AS dec, map('k', 1) AS m`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(typed)
	if string(raw) != `{"columns":["j","big","arr","dec","m"],"rows":[[{"a":1},7,[1,2],"1.5",{"k":1}]]}` {
		t.Fatalf("typed = %s", raw)
	}
	selected, err := p.Execute(context.Background(), "SELECT id, name, at, day FROM probe ORDER BY id", scratchConfig, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(selected)
	if string(raw) != `{"columns":["id","name","at","day"],"rows":[[1,"Ada","2026-11-20 01:02:03","2026-11-20"],[2,"Bob","2026-11-21 00:00:00",null]]}` {
		t.Fatalf("probe = %s", raw)
	}

	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		// sleep is capped at 3s per block, so a long scan stands in for a slow query.
		_, err := p.Execute(runCtx, "SELECT count() FROM numbers(100000000000) WHERE sipHash64(number) = 0 SETTINGS max_threads = 1", config, secrets, handle)
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("query did not stop when the context was cancelled")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		listed, err := p.Execute(context.Background(), "SELECT query_id FROM system.processes WHERE query LIKE '%numbers(100000000000)%' AND query_id LIKE 'gatekeep-%' AND query NOT LIKE '%system.processes%'", config, secrets, handle)
		if err != nil {
			t.Fatal(err)
		}
		rows, _ := listed.(map[string]any)["rows"].([][]any)
		if len(rows) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("query still running after cancel: %v", rows)
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

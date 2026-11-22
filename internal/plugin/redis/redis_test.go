package redis

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"gatekeep/internal/cache"
)

func TestParseCommand(t *testing.T) {
	args, err := parseCommand(`["PING"]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] != "PING" {
		t.Fatalf("args = %#v", args)
	}
	args, err = parseCommand(`["EVAL", "return 1", "0"]`)
	if err != nil || len(args) != 3 {
		t.Fatalf("eval = %#v err=%v", args, err)
	}
	for _, command := range []string{"", "  ", "{}", `[]`, `[1,2]`, `["","x"]`} {
		if _, err := parseCommand(command); err == nil {
			t.Fatalf("expected %q to fail", command)
		}
	}
}

func TestMapReply(t *testing.T) {
	got, err := mapReply([]any{"k", int64(1), []byte("text")})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if string(raw) != `["k",1,"text"]` {
		t.Fatalf("json = %s", raw)
	}
	got, err = mapReply(nil)
	if err != nil || got != nil {
		t.Fatalf("nil = %#v", got)
	}
}

func TestConnSettings(t *testing.T) {
	cfg, err := connSettings(map[string]string{
		"host": "cache.internal", "port": "6380", "database": "2", "user": "reader",
	}, map[string]string{"password": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.addr != "cache.internal:6380" || cfg.database != 2 || cfg.user != "reader" || cfg.password != "secret" {
		t.Fatalf("config = %+v", cfg)
	}
	cfg, err = connSettings(map[string]string{"host": "cache"}, nil)
	if err != nil || cfg.database != 0 || cfg.port != "6379" {
		t.Fatalf("defaults = %+v err=%v", cfg, err)
	}
	if _, err := connSettings(map[string]string{"port": "6379", "database": "0"}, nil); err == nil {
		t.Fatal("expected missing host to fail")
	}
}

func TestExecuteRequiresCommand(t *testing.T) {
	_, err := Plugin{}.Execute(context.Background(), "  ", nil, nil, cache.New().Handle(1, time.Hour))
	if err == nil || err.Error() != "command is required" {
		t.Fatal(err)
	}
}

func TestLiveRedis(t *testing.T) {
	config := map[string]string{
		"host":     env("GATEKEEP_REDIS_HOST", "127.0.0.1"),
		"port":     env("GATEKEEP_REDIS_PORT", "6379"),
		"database": env("GATEKEEP_REDIS_DATABASE", "0"),
	}
	secrets := map[string]string{"password": env("GATEKEEP_REDIS_PASSWORD", "")}
	if user := env("GATEKEEP_REDIS_USER", ""); user != "" {
		config["user"] = user
	}

	p := Plugin{}
	if err := p.Test(context.Background(), config, secrets); err != nil {
		t.Skipf("redis is not available: %v", err)
	}

	mem := cache.New()
	handle := mem.Handle(1, time.Hour)
	key := "gatekeep:probe:" + time.Now().Format("150405.000")

	ping, err := p.Execute(context.Background(), `["PING"]`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	if ping != "PONG" {
		t.Fatalf("PING = %#v", ping)
	}

	_, err = p.Execute(context.Background(), `["SET", "`+key+`", "Ada", "EX", "60"]`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Execute(context.Background(), `["GET", "`+key+`"]`, config, secrets, handle)
	if err != nil || got != "Ada" {
		t.Fatalf("GET = %#v err=%v", got, err)
	}
	got, err = p.Execute(context.Background(), `["HGETALL", "`+key+`"]`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if string(raw) != `[]` {
		t.Fatalf("HGETALL = %s", raw)
	}

	script := `["EVAL", "return redis.call('GET', KEYS[1])", "1", "` + key + `"]`
	got, err = p.Execute(context.Background(), script, config, secrets, handle)
	if err != nil || got != "Ada" {
		t.Fatalf("EVAL = %#v err=%v", got, err)
	}

	missing, err := p.Execute(context.Background(), `["GET", "`+key+`:missing"]`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatalf("missing key = %#v", missing)
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func TestParseCommandRequiresJSON(t *testing.T) {
	_, err := parseCommand("GET key")
	if err == nil || err.Error() != "command must be a JSON array of strings" {
		t.Fatalf("err = %v", err)
	}
}

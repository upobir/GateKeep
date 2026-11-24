package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"gatekeep/internal/cache"
)

func TestParseCommand(t *testing.T) {
	cmd, err := parseCommand(`{"ping":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.doc) != 1 || cmd.doc[0].Key != "ping" {
		t.Fatalf("command = %+v", cmd)
	}
	for _, command := range []string{"", "  ", "db.orders.find({})", "{", "[1]"} {
		if _, err := parseCommand(command); err == nil {
			t.Fatalf("expected %q to fail", command)
		}
	}
}

func TestParseCommandPreservesMaxTimeMS(t *testing.T) {
	for _, tc := range []struct {
		input        string
		hasMaxTimeMS bool
	}{
		{`{"find":"probe","filter":{"maxTimeMS":123},"maxTimeMS":1000,"limit":1}`, true},
		{`{"find":"probe","maxTimeMS":0,"filter":{"maxTimeMS":123},"limit":1}`, true},
		{`{"find":"probe","filter":{"maxTimeMS":123},"limit":1}`, false},
	} {
		cmd, err := parseCommand(tc.input)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := bson.MarshalExtJSON(cmd.doc, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.input || cmd.hasMaxTimeMS != tc.hasMaxTimeMS {
			t.Fatalf("command = %s, hasMaxTimeMS = %v", raw, cmd.hasMaxTimeMS)
		}
	}
	if _, err := parseCommand(`{"find":"probe","maxTimeMS":1000,"maxTimeMS":2000}`); err == nil {
		t.Fatal("expected duplicate maxTimeMS fields to fail")
	}
}

func TestExtJSON(t *testing.T) {
	id, _ := bson.ObjectIDFromHex("507f1f77bcf86cd799439011")
	when := time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC)
	got, err := extJSON(bson.D{
		{Key: "_id", Value: id},
		{Key: "at", Value: when},
		{Key: "n", Value: int32(7)},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if string(raw) != `{"_id":{"$oid":"507f1f77bcf86cd799439011"},"at":{"$date":"2026-11-20T01:02:03Z"},"n":7}` {
		t.Fatalf("json = %s", raw)
	}
	got, err = extJSON(bson.A{"Ada", int32(2)})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(got)
	if string(raw) != `["Ada",2]` {
		t.Fatalf("array = %s", raw)
	}
	reply, _ := bson.Marshal(bson.D{
		{Key: "ok", Value: 1.0},
		{Key: "operationTime", Value: bson.Timestamp{T: 1, I: 1}},
		{Key: "n", Value: int32(2)},
	})
	got, err = commandReply(reply)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(got)
	if strings.Contains(string(raw), "operationTime") || !strings.Contains(string(raw), `"n":2`) {
		t.Fatalf("reply = %s", raw)
	}
}

func TestConnSettings(t *testing.T) {
	cfg, err := connSettings(map[string]string{
		"host": "db.internal", "port": "27018", "database": "orders", "user": "reader",
	}, map[string]string{"password": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.addr != "db.internal:27018" || cfg.database != "orders" || cfg.user != "reader" || cfg.password != "secret" || cfg.authDatabase != "admin" {
		t.Fatalf("config = %+v", cfg)
	}
	if _, err := connSettings(map[string]string{"host": "db", "port": "0", "database": "orders", "user": "reader"}, nil); err == nil {
		t.Fatal("expected an invalid port to fail")
	}
	if _, err := connSettings(map[string]string{"port": "27017", "database": "orders", "user": "reader"}, nil); err == nil {
		t.Fatal("expected a missing host to fail")
	}
}

func TestExecuteRequiresCommand(t *testing.T) {
	_, err := Plugin{}.Execute(context.Background(), "  ", nil, nil, cache.New().Handle(1, time.Hour))
	if err == nil || err.Error() != "command is required" {
		t.Fatal(err)
	}
}

func TestLiveMongoDB(t *testing.T) {
	config := map[string]string{
		"host":         env("GATEKEEP_MONGODB_HOST", "127.0.0.1"),
		"port":         env("GATEKEEP_MONGODB_PORT", "27017"),
		"database":     "gatekeep_driver_test",
		"user":         env("GATEKEEP_MONGODB_USER", "root"),
		"authDatabase": env("GATEKEEP_MONGODB_AUTH_DATABASE", "admin"),
	}
	password := env("GATEKEEP_MONGODB_PASSWORD", "rootpassword")
	secrets := map[string]string{"password": password}
	p := Plugin{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Test(ctx, config, secrets); err != nil {
		t.Skipf("mongodb is not available: %v", err)
	}
	if err := p.Test(context.Background(), config, map[string]string{"password": password + "-no"}); err == nil || strings.Contains(err.Error(), password) {
		t.Fatalf("bad password error = %v", err)
	}

	mem := cache.New()
	handle := mem.Handle(1, time.Hour)
	if _, err := p.Execute(context.Background(), `{"dropDatabase":1}`, config, secrets, handle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = p.Execute(context.Background(), `{"dropDatabase":1}`, config, secrets, handle)
	})

	inserted, err := p.Execute(context.Background(), `{"insert":"probe","documents":[{"_id":"a","name":"Ada"},{"_id":"b","name":"Grace"}]}`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(inserted)
	if !strings.Contains(string(raw), `"n":2`) || !strings.Contains(string(raw), `"ok":1`) {
		t.Fatalf("insert = %s", raw)
	}
	found, err := p.Execute(context.Background(), `{"find":"probe","filter":{"name":{"$regex":"^A"}},"sort":{"_id":1},"limit":1}`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(found)
	if string(raw) != `[{"_id":"a","name":"Ada"}]` {
		t.Fatalf("find = %s", raw)
	}
	if len(mem.List(1, time.Hour, time.Now())) != 1 {
		t.Fatal("connection was not reused from the cache")
	}
	aggregated, err := p.Execute(context.Background(), `{"aggregate":"probe","pipeline":[{"$match":{"_id":"b"}},{"$project":{"_id":0,"name":1}}],"cursor":{}}`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(aggregated)
	if string(raw) != `[{"name":"Grace"}]` {
		t.Fatalf("aggregate = %s", raw)
	}
	updated, err := p.Execute(context.Background(), `{"update":"probe","updates":[{"q":{"_id":"a"},"u":{"$set":{"name":"Ada Lovelace"}}}]}`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(updated)
	if !strings.Contains(string(raw), `"n":1`) || !strings.Contains(string(raw), `"nModified":1`) {
		t.Fatalf("update = %s", raw)
	}
	counted, err := p.Execute(context.Background(), `{"count":"probe","query":{}}`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(counted)
	if !strings.Contains(string(raw), `"n":2`) {
		t.Fatalf("count = %s", raw)
	}
	distinct, err := p.Execute(context.Background(), `{"distinct":"probe","key":"name"}`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(distinct)
	if !strings.Contains(string(raw), `"Ada Lovelace"`) || !strings.Contains(string(raw), `"Grace"`) {
		t.Fatalf("distinct = %s", raw)
	}
	missing, err := p.Execute(context.Background(), `{"find":"probe","filter":{"_id":"missing"},"limit":1}`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(missing)
	if string(raw) != `[]` {
		t.Fatalf("missing = %s", raw)
	}
	if _, err := p.Execute(context.Background(), `{"nope":1}`, config, secrets, handle); err == nil {
		t.Fatal("expected an unknown command to fail")
	}
	ping, err := p.Execute(context.Background(), `{"ping":1}`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(ping)
	if !strings.Contains(string(raw), `"ok":1`) {
		t.Fatalf("ping = %s", raw)
	}

	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Execute(runCtx, `{"aggregate":"probe","pipeline":[{"$match":{"$expr":{"$function":{"body":"function() { sleep(30000); return true }","args":[],"lang":"js"}}}}],"cursor":{}}`, config, secrets, handle)
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
		t.Fatal("aggregate did not stop when the context was cancelled")
	}
	followed, err := p.Execute(context.Background(), `{"find":"probe","filter":{"_id":"b"},"projection":{"_id":0,"name":1}}`, config, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(followed)
	if string(raw) != `[{"name":"Grace"}]` {
		t.Fatalf("query after cancel = %s", raw)
	}
}

func TestLiveMongoDBMaxTimeMS(t *testing.T) {
	config := map[string]string{
		"host":         env("GATEKEEP_MONGODB_HOST", "127.0.0.1"),
		"port":         env("GATEKEEP_MONGODB_PORT", "27017"),
		"database":     "gatekeep_maxtimems_test_" + strings.ReplaceAll(time.Now().Format("20060102150405.000000000"), ".", "_"),
		"user":         env("GATEKEEP_MONGODB_USER", "root"),
		"authDatabase": env("GATEKEEP_MONGODB_AUTH_DATABASE", "admin"),
	}
	secrets := map[string]string{"password": env("GATEKEEP_MONGODB_PASSWORD", "rootpassword")}
	cfg, err := connSettings(config, secrets)
	if err != nil {
		t.Fatal(err)
	}
	sent := make(chan bson.Raw, 16)
	client, err := mongo.Connect(driverOptions(cfg).SetMonitor(&event.CommandMonitor{
		Started: func(ctx context.Context, evt *event.CommandStartedEvent) {
			commandMonitor.Started(ctx, evt)
			if evt.DatabaseName == cfg.database && (evt.CommandName == "find" || evt.CommandName == "aggregate" || evt.CommandName == "count") {
				sent <- append(bson.Raw(nil), evt.Command...)
			}
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { disconnect(client) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		t.Skipf("mongodb is not available: %v", err)
	}
	handle := cache.New().Handle(1, time.Hour)
	handle.Update(poolName, func(any, bool) (any, bool) {
		return &cachedClient{key: cfg.key(), client: client}, true
	})
	p := Plugin{}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := client.Database(cfg.database).Drop(cleanupCtx); err != nil {
			t.Errorf("clean up test database: %v", err)
		}
	})
	if _, err := p.Execute(ctx, `{"insert":"probe","documents":[{"_id":"a","maxTimeMS":123}]}`, config, secrets, handle); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input string
		maxMS int64
	}{
		{`{"find":"probe","filter":{"maxTimeMS":123},"maxTimeMS":1000}`, 1000},
		{`{"aggregate":"probe","pipeline":[{"$match":{"maxTimeMS":123}}],"cursor":{},"maxTimeMS":0}`, 0},
		{`{"count":"probe","query":{"maxTimeMS":123}}`, 5000},
		{`{"count":"probe","query":{"maxTimeMS":123},"maxTimeMS":60000}`, 60000},
	} {
		t.Run(tc.input, func(t *testing.T) {
			runCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			result, err := p.Execute(runCtx, tc.input, config, secrets, handle)
			if err != nil {
				t.Fatal(err)
			}
			raw := <-sent
			fields, err := raw.Elements()
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, field := range fields {
				if field.Key() == "maxTimeMS" {
					count++
				}
			}
			ms, ok := raw.Lookup("maxTimeMS").AsInt64OK()
			if count != 1 || !ok || ms <= tc.maxMS-500 || ms > tc.maxMS {
				t.Fatalf("expected one maxTimeMS, got %s", raw)
			}
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if raw.Lookup("count").Type != 0 {
				if !strings.Contains(string(data), `"n":1`) {
					t.Fatalf("count = %s", data)
				}
			} else if string(data) != `[{"_id":"a","maxTimeMS":123}]` {
				t.Fatalf("documents = %s", data)
			}
			t.Logf("wire maxTimeMS=%d (one field); result=%s", ms, data)
		})
	}
	t.Run("short timeout interrupts slow operation", func(t *testing.T) {
		runCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		start := time.Now()
		_, err := p.Execute(runCtx, `{"aggregate":"probe","pipeline":[{"$match":{"$expr":{"$function":{"body":"function() { sleep(300); return true }","args":[],"lang":"js"}}}}],"cursor":{},"maxTimeMS":100}`, config, secrets, handle)
		<-sent
		var commandErr mongo.CommandError
		if !errors.As(err, &commandErr) || commandErr.Code != 50 {
			t.Fatalf("expected MongoDB MaxTimeMSExpired (50), got %v", err)
		}
		if elapsed := time.Since(start); elapsed >= 2*time.Second {
			t.Fatalf("short timeout took %v", elapsed)
		}
		if runCtx.Err() != nil {
			t.Fatal("outer execution deadline expired")
		}
		t.Logf("short timeout interrupted operation after %v: %v", time.Since(start), err)
	})
	t.Run("Gatekeep deadline remains active", func(t *testing.T) {
		runCtx, stop := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer stop()
		start := time.Now()
		_, err := p.Execute(runCtx, `{"aggregate":"probe","pipeline":[{"$match":{"$expr":{"$function":{"body":"function() { sleep(1000); return true }","args":[],"lang":"js"}}}}],"cursor":{},"maxTimeMS":60000}`, config, secrets, handle)
		if !errors.Is(err, context.DeadlineExceeded) || runCtx.Err() != context.DeadlineExceeded {
			t.Fatalf("expected Gatekeep deadline, got %v", err)
		}
		raw := <-sent
		if ms, ok := raw.Lookup("maxTimeMS").AsInt64OK(); !ok || ms != 60000 {
			t.Fatalf("expected submitted server limit unchanged, got %s", raw)
		}
		t.Logf("Gatekeep deadline cancelled operation after %v", time.Since(start))
	})
	t.Run("Gatekeep cancellation remains active", func(t *testing.T) {
		runCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		done := make(chan error, 1)
		go func() {
			_, err := p.Execute(runCtx, `{"aggregate":"probe","pipeline":[{"$match":{"$expr":{"$function":{"body":"function() { var end = Date.now() + 3000; while (Date.now() < end) {} return true }","args":[],"lang":"js"}}}}],"cursor":{},"maxTimeMS":60000}`, config, secrets, handle)
			done <- err
		}()
		select {
		case <-sent:
			time.Sleep(50 * time.Millisecond)
			stop()
		case err := <-done:
			t.Fatalf("operation ended before cancellation: %v", err)
		case <-runCtx.Done():
			t.Fatal("command was not sent")
		}
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected Gatekeep cancellation, got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Gatekeep cancellation did not stop the operation")
		}
	})
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func TestConnectionTLS(t *testing.T) {
	config := map[string]string{"host": "db.internal", "database": "orders", "user": "reader"}
	keys := make(map[string]string)
	for _, mode := range []string{"", "off", "on", "skip-verify"} {
		config["tls"] = mode
		cfg, err := connSettings(config, nil)
		if err != nil {
			t.Fatal(err)
		}
		options := driverOptions(cfg)
		encrypted := mode == "on" || mode == "skip-verify"
		if encrypted != (options.TLSConfig != nil) {
			t.Fatalf("tls %q: expected encryption %v, config = %+v", mode, encrypted, options.TLSConfig)
		}
		if options.TLSConfig != nil && options.TLSConfig.InsecureSkipVerify != (mode == "skip-verify") {
			t.Fatalf("tls %q: wrong certificate verification setting", mode)
		}
		keys[mode] = cfg.key()
	}
	if keys[""] != keys["off"] {
		t.Fatal("existing resources must retain TLS-off connection identity")
	}
	if keys["off"] == keys["on"] || keys["on"] == keys["skip-verify"] || keys["off"] == keys["skip-verify"] {
		t.Fatal("changing TLS mode must replace the cached connection")
	}
	config["tls"] = "maybe"
	if _, err := connSettings(config, nil); err == nil {
		t.Fatal("expected unknown TLS mode to fail")
	}
}

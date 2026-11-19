package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"gatekeep/internal/db"
)

type recordingCloser struct {
	closed bool
}

func (c *recordingCloser) Close() error {
	c.closed = true
	return nil
}

func TestNextvalKeepsASequenceInTheResourceCache(t *testing.T) {
	store := openMCPStore(t)
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)

	created := postResource(t, client, srv, `{"name":"local","type":"mock","config":{"host":"db.example"},"secrets":{"token":"ok"},"cacheIdleSeconds":60}`)
	if created.CacheIdleSeconds == nil || *created.CacheIdleSeconds != 60 {
		t.Fatalf("idle = %v", created.CacheIdleSeconds)
	}
	stored, err := store.GetResource(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CacheIdleMs == nil || *stored.CacheIdleMs != 60000 {
		t.Fatalf("stored idle = %v", stored.CacheIdleMs)
	}

	bad, err := http.NewRequest(http.MethodPut, srv.URL()+"/api/resources/"+strconv.FormatInt(created.ID, 10), strings.NewReader(`{"name":"local","type":"mock","config":{"host":"db.example"},"secrets":{},"cacheIdleSeconds":0}`))
	if err != nil {
		t.Fatal(err)
	}
	bad.Header.Set("Content-Type", "application/json")
	badResp, err := client.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("zero idle = %d", badResp.StatusCode)
	}

	session := mcpSessionID(t, srv)
	first := approveMock(t, client, srv, store, session, "nextval")
	second := approveMock(t, client, srv, store, session, "nextval")
	if second != first+1 {
		t.Fatalf("sequence = %v then %v", first, second)
	}

	before := cacheObjects(t, client, srv, created.ID)
	if len(before) != 1 || before[0].Name != "sequence" {
		t.Fatalf("cache = %+v", before)
	}
	createdAt := parseCacheTime(t, before[0].CreatedAt)
	updatedAt := parseCacheTime(t, before[0].UpdatedAt)
	expiresAt := parseCacheTime(t, before[0].ExpiresAt)
	if !expiresAt.Equal(updatedAt.Add(60*time.Second)) || createdAt.After(updatedAt) {
		t.Fatalf("times = %+v", before[0])
	}

	time.Sleep(5 * time.Millisecond)
	third := approveMock(t, client, srv, store, session, "nextval")
	if third != second+1 {
		t.Fatalf("third = %v after %v", third, second)
	}
	after := cacheObjects(t, client, srv, created.ID)
	if len(after) != 1 || !parseCacheTime(t, after[0].CreatedAt).Equal(createdAt) || !parseCacheTime(t, after[0].UpdatedAt).After(updatedAt) {
		t.Fatalf("refreshed = %+v", after)
	}

	clearReq, err := http.NewRequest(http.MethodDelete, srv.URL()+"/api/resources/"+strconv.FormatInt(created.ID, 10)+"/cache/sequence", nil)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := client.Do(clearReq)
	if err != nil {
		t.Fatal(err)
	}
	cleared.Body.Close()
	if cleared.StatusCode != http.StatusOK || len(cacheObjects(t, client, srv, created.ID)) != 0 {
		t.Fatalf("clear = %d", cleared.StatusCode)
	}
	approveMock(t, client, srv, store, session, "nextval")
	if len(cacheObjects(t, client, srv, created.ID)) != 1 {
		t.Fatal("nextval did not start a new sequence")
	}
}

func TestCacheDropsWhenIdleAndOnShutdown(t *testing.T) {
	store := openMCPStore(t)
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	created := postResource(t, client, srv, `{"name":"local","type":"mock","config":{"host":"db.example"},"secrets":{"token":"ok"},"cacheIdleSeconds":1}`)
	session := mcpSessionID(t, srv)

	approveMock(t, client, srv, store, session, "nextval")
	if len(cacheObjects(t, client, srv, created.ID)) != 1 {
		t.Fatal("sequence was not cached")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(cacheObjects(t, client, srv, created.ID)) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(cacheObjects(t, client, srv, created.ID)) != 0 {
		t.Fatal("idle cache was not dropped")
	}

	approveMock(t, client, srv, store, session, "nextval")
	held := &recordingCloser{}
	srv.caches.Handle(created.ID, time.Hour).Update("db", func(any, bool) (any, bool) {
		return held, true
	})
	deleted, err := http.NewRequest(http.MethodDelete, srv.URL()+"/api/resources/"+strconv.FormatInt(created.ID, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	gone, err := client.Do(deleted)
	if err != nil {
		t.Fatal(err)
	}
	gone.Body.Close()
	if gone.StatusCode != http.StatusOK || !held.closed || len(srv.caches.ResourceIDs()) != 0 {
		t.Fatalf("delete status = %d closed = %v ids = %v", gone.StatusCode, held.closed, srv.caches.ResourceIDs())
	}

	again := postResource(t, client, srv, `{"name":"local","type":"mock","config":{"host":"db.example"},"secrets":{"token":"ok"},"cacheIdleSeconds":60}`)
	approveMock(t, client, srv, store, session, "nextval")
	if len(srv.caches.ResourceIDs()) != 1 {
		t.Fatalf("ids before shutdown = %v", srv.caches.ResourceIDs())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if len(srv.caches.ResourceIDs()) != 0 {
		t.Fatalf("ids after shutdown = %v", srv.caches.ResourceIDs())
	}
	if again.ID == 0 {
		t.Fatal("resource was not recreated")
	}
}

func postResource(t *testing.T, client *http.Client, srv *Server, body string) resourceView {
	t.Helper()
	resp, err := client.Post(srv.URL()+"/api/resources", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var view resourceView
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d %+v", resp.StatusCode, view)
	}
	return view
}

func approveMock(t *testing.T, client *http.Client, srv *Server, store *db.Store, session, command string) float64 {
	t.Helper()
	done := make(chan callOutcome, 1)
	go func() {
		done <- callMock(srv, session, command, "next value")
	}()
	waitPendingCommand(t, store, command)
	pending, err := store.ListPendingToolCalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, call := range pending {
		if call.Command == command {
			id = call.ID
		}
	}
	if id == 0 {
		t.Fatalf("missing pending %s", command)
	}
	resp, err := client.Post(srv.URL()+"/api/approvals/"+strconv.FormatInt(id, 10)+"/approve", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d", resp.StatusCode)
	}
	outcome := <-done
	if outcome.err != nil || outcome.isError {
		t.Fatalf("call = %+v", outcome)
	}
	decoded := decodeOutcome(t, outcome.text)
	number, ok := objectValue(t, decoded.Result).(float64)
	if !decoded.OK || !ok {
		t.Fatalf("result = %+v", decoded)
	}
	return number
}

func cacheObjects(t *testing.T, client *http.Client, srv *Server, id int64) []cacheObjectView {
	t.Helper()
	resp, err := client.Get(srv.URL() + "/api/resources/" + strconv.FormatInt(id, 10) + "/cache")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Objects []cacheObjectView `json:"objects"`
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("cache body = %s", raw)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cache = %d %s", resp.StatusCode, raw)
	}
	return body.Objects
}

func parseCacheTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

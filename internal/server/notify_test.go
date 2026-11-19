package server

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"gatekeep/internal/db"
)

func TestPendingRequestNotifies(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name:       "local",
		Type:       "mock",
		ConfigJSON: `{"host":"db.example"}`,
	}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)
	notes := recordNotices(srv)

	initResp := mcpPost(t, srv, "", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "Claude Desktop", "version": "1.0.0"},
		},
	})
	session := initResp.Header.Get("Mcp-Session-Id")
	initResp.Body.Close()

	decided := decideWhenPending(srv, store, 1, db.ToolCallRejected, db.DecisionReject, "not now")
	callResp := mcpPost(t, srv, session, "2025-11-25", map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "mock",
			"arguments": map[string]any{
				"resource": "local",
				"command":  "PING",
				"reason":   "check that the resource is reachable",
			},
		},
	})
	if err := <-decided; err != nil {
		t.Fatal(err)
	}
	callResp.Body.Close()
	if callResp.StatusCode != http.StatusOK {
		t.Fatalf("call status = %d", callResp.StatusCode)
	}

	got := notes()
	if len(got) != 1 {
		t.Fatalf("notices = %+v", got)
	}
	if got[0].title != "Approval needed" {
		t.Fatalf("title = %q", got[0].title)
	}
	if got[0].body != "Claude Desktop wants to run a command on local\ncheck that the resource is reachable" {
		t.Fatalf("body = %q", got[0].body)
	}
	if got[0].url != srv.URL() {
		t.Fatalf("url = %q", got[0].url)
	}
}

func TestExpiredRequestDoesNotNotify(t *testing.T) {
	store := openMCPStore(t)
	srv := startMCPServer(t, store)
	notes := recordNotices(srv)
	call, err := store.CreateToolCall(context.Background(), db.ToolCall{
		ResourceName: "local",
		HarnessName:  "Claude Desktop",
		Command:      "PING",
		Reason:       "check it",
		Status:       db.ToolCallPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.decideApproval(context.Background(), call.ID, db.ToolCallExpired, db.DecisionExpire, timeoutReason); err != nil {
		t.Fatal(err)
	}
	if got := notes(); len(got) != 0 {
		t.Fatalf("notices = %+v", got)
	}
}

type notice struct {
	title string
	body  string
	url   string
}

func recordNotices(srv *Server) func() []notice {
	var mu sync.Mutex
	var got []notice
	srv.SetNotifier(func(title, body, url string) {
		mu.Lock()
		got = append(got, notice{title: title, body: body, url: url})
		mu.Unlock()
	})
	return func() []notice {
		mu.Lock()
		defer mu.Unlock()
		out := make([]notice, len(got))
		copy(out, got)
		return out
	}
}

package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
	"testing"
	"time"

	"gatekeep/internal/db"
)

func TestApprovalQueueOrdersAndAnswers(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name: "local", Type: "mock", ConfigJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	sealMockToken(t, srv, store, "local", "ok")

	initResp := mcpPost(t, srv, "", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "Claude Desktop", "version": "1"},
		},
	})
	session := initResp.Header.Get("Mcp-Session-Id")
	initResp.Body.Close()

	firstDone := make(chan callOutcome, 1)
	secondDone := make(chan callOutcome, 1)
	go func() {
		firstDone <- callMock(srv, session, "SELECT 1", "first look")
	}()
	waitPendingCommand(t, store, "SELECT 1")
	go func() {
		secondDone <- callMock(srv, session, "SELECT 2", "second look")
	}()
	waitPendingCommand(t, store, "SELECT 2")

	listResp, err := client.Get(srv.URL() + "/api/approvals")
	if err != nil {
		t.Fatal(err)
	}
	var listed []approvalView
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		listResp.Body.Close()
		t.Fatal(err)
	}
	listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK || len(listed) != 2 {
		t.Fatalf("list status = %d body = %+v", listResp.StatusCode, listed)
	}
	if listed[0].Command != "SELECT 1" || listed[1].Command != "SELECT 2" {
		t.Fatalf("order = %+v", listed)
	}
	if listed[0].ResourceName != "local" || listed[0].HarnessName != "Claude Desktop" || listed[0].Reason != "first look" {
		t.Fatalf("first = %+v", listed[0])
	}

	emptyReject, err := client.Post(srv.URL()+"/api/approvals/"+strconv.FormatInt(listed[1].ID, 10)+"/reject", "application/json", strings.NewReader(`{"reason":"  "}`))
	if err != nil {
		t.Fatal(err)
	}
	emptyReject.Body.Close()
	if emptyReject.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty reject = %d", emptyReject.StatusCode)
	}

	approveResp, err := client.Post(srv.URL()+"/api/approvals/"+strconv.FormatInt(listed[0].ID, 10)+"/approve", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	approveResp.Body.Close()
	if approveResp.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d", approveResp.StatusCode)
	}
	rejectResp, err := client.Post(srv.URL()+"/api/approvals/"+strconv.FormatInt(listed[1].ID, 10)+"/reject", "application/json", strings.NewReader(`{"reason":"too broad"}`))
	if err != nil {
		t.Fatal(err)
	}
	rejectResp.Body.Close()
	if rejectResp.StatusCode != http.StatusOK {
		t.Fatalf("reject = %d", rejectResp.StatusCode)
	}

	first := <-firstDone
	second := <-secondDone
	if first.err != nil || second.err != nil {
		t.Fatalf("first = %v second = %v", first.err, second.err)
	}
	approved := decodeOutcome(t, first.text)
	rejectedOutcome := decodeOutcome(t, second.text)
	if first.isError || !approved.OK || approved.Kind != "tool_call_successful" || approved.Result != true {
		t.Fatalf("approved result = %+v %s", approved, first.text)
	}
	if !second.isError || rejectedOutcome.OK || rejectedOutcome.Kind != "user_rejected" || rejectedOutcome.Reason != "too broad" {
		t.Fatalf("rejected result = %+v %s", rejectedOutcome, second.text)
	}

	again, err := client.Post(srv.URL()+"/api/approvals/"+strconv.FormatInt(listed[0].ID, 10)+"/approve", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	again.Body.Close()
	if again.StatusCode != http.StatusConflict {
		t.Fatalf("second approve = %d", again.StatusCode)
	}

	empty, err := client.Get(srv.URL() + "/api/approvals")
	if err != nil {
		t.Fatal(err)
	}
	var left []approvalView
	if err := json.NewDecoder(empty.Body).Decode(&left); err != nil {
		empty.Body.Close()
		t.Fatal(err)
	}
	empty.Body.Close()
	if len(left) != 0 {
		t.Fatalf("pending = %+v", left)
	}

	missing, err := client.Get(srv.URL() + "/api/approvals/999")
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing = %d", missing.StatusCode)
	}

	settled, err := client.Get(srv.URL() + "/api/approvals/" + strconv.FormatInt(listed[1].ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	var rejected approvalView
	if err := json.NewDecoder(settled.Body).Decode(&rejected); err != nil {
		settled.Body.Close()
		t.Fatal(err)
	}
	settled.Body.Close()
	if rejected.Status != db.ToolCallRejected || rejected.DecisionReason != "too broad" {
		t.Fatalf("settled = %+v", rejected)
	}
}

func TestApprovedMockRejectsAWrongToken(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name: "local", Type: "mock", ConfigJSON: `{"host":"db.example"}`,
	}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	sealMockToken(t, srv, store, "local", "no")

	initResp := mcpPost(t, srv, "", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	})
	session := initResp.Header.Get("Mcp-Session-Id")
	initResp.Body.Close()

	done := make(chan callOutcome, 1)
	go func() {
		done <- callMock(srv, session, "PING", "try the token")
	}()
	waitPendingCommand(t, store, "PING")
	pending, err := store.ListPendingToolCalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	approve, err := client.Post(srv.URL()+"/api/approvals/"+strconv.FormatInt(pending[0].ID, 10)+"/approve", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	approve.Body.Close()
	if approve.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d", approve.StatusCode)
	}
	outcome := <-done
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	got := decodeOutcome(t, outcome.text)
	if !outcome.isError || got.OK || got.Kind != "tool_call_error" || got.Error != "mock token was rejected" {
		t.Fatalf("result = %+v %s", got, outcome.text)
	}
}

func TestApprovalTimeoutAnswersTheHarness(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name: "local", Type: "mock", ConfigJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutSetting(context.Background(), approvalTimeoutKey, "1s"); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)
	initResp := mcpPost(t, srv, "", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	})
	session := initResp.Header.Get("Mcp-Session-Id")
	initResp.Body.Close()

	outcome := callMock(srv, session, "PING", "see if anyone is there")
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	expired := decodeOutcome(t, outcome.text)
	if !outcome.isError || expired.OK || expired.Kind != "approval_expired" || !strings.Contains(expired.Error, "timed out") {
		t.Fatalf("result = %+v %s", expired, outcome.text)
	}
	stored, err := store.GetToolCall(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != db.ToolCallExpired || stored.FinishedAt == nil {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestRestartExpiresPendingApprovals(t *testing.T) {
	store := openMCPStore(t)
	call, err := store.CreateToolCall(context.Background(), db.ToolCall{
		ResourceName: "local",
		HarnessName:  "Claude Desktop",
		Command:      "PING",
		Reason:       "left over from a crash",
		Status:       db.ToolCallPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(0, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.listener.Close() })

	got, err := store.GetToolCall(context.Background(), call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != db.ToolCallExpired || got.DecisionReason == nil || !strings.Contains(*got.DecisionReason, "restarted") {
		t.Fatalf("stored = %+v", got)
	}

	fresh, err := store.CreateToolCall(context.Background(), db.ToolCall{
		ResourceName: "local",
		HarnessName:  "Claude Desktop",
		Command:      "PING",
		Reason:       "still waiting",
		Status:       db.ToolCallPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Status != db.ToolCallPending {
		t.Fatalf("fresh = %+v", fresh)
	}
}

func TestApprovalTimeoutSetting(t *testing.T) {
	store := openMCPStore(t)
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)

	got, err := client.Get(srv.URL() + "/api/settings/approval-timeout")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]int
	if err := json.NewDecoder(got.Body).Decode(&body); err != nil {
		got.Body.Close()
		t.Fatal(err)
	}
	got.Body.Close()
	if body["seconds"] != int(defaultApprovalTimeout/time.Second) {
		t.Fatalf("default = %v", body)
	}

	put, err := http.NewRequest(http.MethodPut, srv.URL()+"/api/settings/approval-timeout", strings.NewReader(`{"seconds":120}`))
	if err != nil {
		t.Fatal(err)
	}
	put.Header.Set("Content-Type", "application/json")
	putResp, err := client.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d", putResp.StatusCode)
	}
	if wait := srv.approvalTimeout(context.Background()); wait != 2*time.Minute {
		t.Fatalf("timeout = %s", wait)
	}

	bad, err := http.NewRequest(http.MethodPut, srv.URL()+"/api/settings/approval-timeout", strings.NewReader(`{"seconds":0}`))
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
		t.Fatalf("zero timeout = %d", badResp.StatusCode)
	}
}

type callOutcome struct {
	text    string
	isError bool
	err     error
}

func sealMockToken(t *testing.T, srv *Server, store *db.Store, name, token string) {
	t.Helper()
	plain, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := srv.vault.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := store.ResourceByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	resource.ConfigJSON = `{"host":"db.example"}`
	resource.SecretsCiphertext = sealed
	if err := store.UpdateResource(context.Background(), resource); err != nil {
		t.Fatal(err)
	}
}

func callMock(srv *Server, session, command, reason string) callOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "mock",
			"arguments": map[string]any{
				"resource": "local",
				"command":  command,
				"reason":   reason,
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return callOutcome{err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL()+"/mcp", strings.NewReader(string(raw)))
	if err != nil {
		return callOutcome{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	req.Header.Set("Mcp-Session-Id", session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return callOutcome{err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return callOutcome{err: err}
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return callOutcome{err: err}
	}
	result, _ := decoded["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return callOutcome{text: string(body)}
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	isError, _ := result["isError"].(bool)
	return callOutcome{text: text, isError: isError}
}

func waitPendingCommand(t *testing.T, store *db.Store, command string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := store.ListPendingToolCalls(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range pending {
			if call.Command == command {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("command %s was not queued", command)
}

func unlockedClient(t *testing.T, srv *Server) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second, Jar: jar}
	resp, err := client.Post(srv.URL()+"/api/setup", "application/json", strings.NewReader(`{"password":"correct horse","confirm":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d", resp.StatusCode)
	}
	return client
}

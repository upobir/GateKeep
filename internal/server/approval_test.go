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
		firstDone <- callMock(srv, session, "ping", "first look")
	}()
	waitPendingCommand(t, store, "ping")
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
	if listed[0].Command != "ping" || listed[1].Command != "SELECT 2" {
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
	if len(left) != 2 || left[0].Command != "SELECT 2" || left[0].Status != db.ToolCallRejected || left[1].Command != "ping" || left[1].Status != db.ToolCallApproved {
		t.Fatalf("recent = %+v", left)
	}
	historyResp, err := client.Get(srv.URL() + "/api/history")
	if err != nil {
		t.Fatal(err)
	}
	var history []approvalView
	if err := json.NewDecoder(historyResp.Body).Decode(&history); err != nil {
		historyResp.Body.Close()
		t.Fatal(err)
	}
	historyResp.Body.Close()
	if historyResp.StatusCode != http.StatusOK || len(history) != 0 {
		t.Fatalf("history = %d %+v", historyResp.StatusCode, history)
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
	if !outcome.isError || got.OK || got.Kind != "tool_call_error" || got.Error != "wrong token" {
		t.Fatalf("result = %+v %s", got, outcome.text)
	}
}

func TestExecutionTimeoutAnswersTheHarness(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name: "local", Type: "mock", ConfigJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutSetting(context.Background(), executionTimeoutKey, "1"); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	sealMockToken(t, srv, store, "local", "ok")
	session := mcpSessionID(t, srv)

	done := make(chan callOutcome, 1)
	go func() {
		done <- callMock(srv, session, "loop", "wait for the limit")
	}()
	waitPendingCommand(t, store, "loop")
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
	if !outcome.isError || got.OK || got.Kind != "tool_call_timed_out" || got.Error != "Execution timed out." {
		t.Fatalf("result = %+v %s", got, outcome.text)
	}
	stored, err := store.GetToolCall(context.Background(), pending[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != db.ToolCallApproved || stored.ExecutionStatus == nil || *stored.ExecutionStatus != db.ExecutionTimeout || stored.FinishedAt == nil {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestResourceTimeoutOverridesTheGlobalLimit(t *testing.T) {
	ms := int64(1000)
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name: "local", Type: "mock", ConfigJSON: `{}`, ExecutionTimeoutMs: &ms,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutSetting(context.Background(), executionTimeoutKey, "30"); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	sealMockToken(t, srv, store, "local", "ok")
	session := mcpSessionID(t, srv)

	done := make(chan callOutcome, 1)
	go func() {
		done <- callMock(srv, session, "loop", "use the resource limit")
	}()
	waitPendingCommand(t, store, "loop")
	pending, err := store.ListPendingToolCalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	approve, err := client.Post(srv.URL()+"/api/approvals/"+strconv.FormatInt(pending[0].ID, 10)+"/approve", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	approve.Body.Close()
	if approve.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d", approve.StatusCode)
	}
	if time.Since(started) > 4*time.Second {
		t.Fatal("resource timeout did not stop the command")
	}
	outcome := <-done
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	got := decodeOutcome(t, outcome.text)
	if got.Kind != "tool_call_timed_out" || got.Error != "Execution timed out." {
		t.Fatalf("result = %+v %s", got, outcome.text)
	}
}

func TestBreakReturnsAnExecutionError(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name: "local", Type: "mock", ConfigJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	sealMockToken(t, srv, store, "local", "ok")
	session := mcpSessionID(t, srv)

	done := make(chan callOutcome, 1)
	go func() {
		done <- callMock(srv, session, "break", "see the failure")
	}()
	waitPendingCommand(t, store, "break")
	pending, err := store.ListPendingToolCalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	approve, err := client.Post(srv.URL()+"/api/approvals/"+strconv.FormatInt(pending[0].ID, 10)+"/approve", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	approve.Body.Close()
	outcome := <-done
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	got := decodeOutcome(t, outcome.text)
	if !outcome.isError || got.Error != "something went wrong" {
		t.Fatalf("result = %+v %s", got, outcome.text)
	}
	stored, err := store.GetToolCall(context.Background(), pending[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ExecutionStatus == nil || *stored.ExecutionStatus != db.ExecutionError {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestCancelReturnsToTheQueue(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name: "local", Type: "mock", ConfigJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	sealMockToken(t, srv, store, "local", "ok")
	session := mcpSessionID(t, srv)

	done := make(chan callOutcome, 1)
	go func() {
		done <- callMock(srv, session, "loop", "hold until cancelled")
	}()
	waitPendingCommand(t, store, "loop")
	pending, err := store.ListPendingToolCalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(pending[0].ID, 10)

	early, err := client.Post(srv.URL()+"/api/approvals/"+id+"/cancel", "application/json", strings.NewReader(`{"reason":"too soon"}`))
	if err != nil {
		t.Fatal(err)
	}
	early.Body.Close()
	if early.StatusCode != http.StatusConflict {
		t.Fatalf("cancel before running = %d", early.StatusCode)
	}

	approved := make(chan int, 1)
	go func() {
		resp, err := client.Post(srv.URL()+"/api/approvals/"+id+"/approve", "application/json", strings.NewReader(`{}`))
		if err != nil {
			approved <- 0
			return
		}
		resp.Body.Close()
		approved <- resp.StatusCode
	}()
	waitToolStatus(t, store, pending[0].ID, db.ToolCallExecuting)

	cancel, err := client.Post(srv.URL()+"/api/approvals/"+id+"/cancel", "application/json", strings.NewReader(`{"reason":"stop this run"}`))
	if err != nil {
		t.Fatal(err)
	}
	var stopped approvalView
	if err := json.NewDecoder(cancel.Body).Decode(&stopped); err != nil {
		cancel.Body.Close()
		t.Fatal(err)
	}
	cancel.Body.Close()
	if cancel.StatusCode != http.StatusOK || stopped.Status != db.ToolCallApproved || stopped.ExecutionStatus != db.ExecutionCancelled || stopped.Decision != db.DecisionApprove || stopped.DecisionReason != "stop this run" {
		t.Fatalf("cancel = %d %+v", cancel.StatusCode, stopped)
	}
	if status := <-approved; status != http.StatusOK {
		t.Fatalf("approve = %d", status)
	}
	outcome := <-done
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	got := decodeOutcome(t, outcome.text)
	if !outcome.isError || got.Kind != "user_cancelled_mid_tool_call" || got.Reason != "stop this run" {
		t.Fatalf("result = %+v %s", got, outcome.text)
	}
	left, err := store.ListQueueToolCalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("queue = %+v", left)
	}
}

func TestSeeResultsSendAndReject(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name: "local", Type: "mock", ConfigJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	sealMockToken(t, srv, store, "local", "ok")
	session := mcpSessionID(t, srv)

	done := make(chan callOutcome, 1)
	go func() {
		done <- callMock(srv, session, "ping", "look first")
	}()
	waitPendingCommand(t, store, "ping")
	pending, err := store.ListPendingToolCalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(pending[0].ID, 10)

	earlySend, err := client.Post(srv.URL()+"/api/approvals/"+id+"/send", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	earlySend.Body.Close()
	if earlySend.StatusCode != http.StatusConflict {
		t.Fatalf("send before running = %d", earlySend.StatusCode)
	}

	saw, err := client.Post(srv.URL()+"/api/approvals/"+id+"/results", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var held approvalView
	if err := json.NewDecoder(saw.Body).Decode(&held); err != nil {
		saw.Body.Close()
		t.Fatal(err)
	}
	saw.Body.Close()
	if saw.StatusCode != http.StatusOK || held.Status != db.ToolCallReview || held.Decision != "" || held.ExecutionStatus != db.ExecutionOK || held.Result != "true" {
		t.Fatalf("held = %d %+v", saw.StatusCode, held)
	}
	select {
	case early := <-done:
		t.Fatalf("released before send: %+v %s", early, early.text)
	case <-time.After(150 * time.Millisecond):
	}

	send, err := client.Post(srv.URL()+"/api/approvals/"+id+"/send", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	send.Body.Close()
	if send.StatusCode != http.StatusOK {
		t.Fatalf("send = %d", send.StatusCode)
	}
	sent := <-done
	if sent.err != nil {
		t.Fatal(sent.err)
	}
	got := decodeOutcome(t, sent.text)
	if sent.isError || !got.OK || got.Kind != "tool_call_successful" || got.Result != true {
		t.Fatalf("sent = %+v %s", got, sent.text)
	}
	stored, err := store.GetToolCall(context.Background(), pending[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Decision == nil || *stored.Decision != db.DecisionApprove || stored.Result == nil || *stored.Result != "true" {
		t.Fatalf("stored send = %+v", stored)
	}

	go func() {
		done <- callMock(srv, session, "ping", "look again")
	}()
	waitPendingCommand(t, store, "ping")
	pending, err = store.ListPendingToolCalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id = strconv.FormatInt(pending[0].ID, 10)
	saw, err = client.Post(srv.URL()+"/api/approvals/"+id+"/results", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	saw.Body.Close()
	if saw.StatusCode != http.StatusOK {
		t.Fatalf("second look = %d", saw.StatusCode)
	}
	empty, err := client.Post(srv.URL()+"/api/approvals/"+id+"/reject", "application/json", strings.NewReader(`{"reason":" "}`))
	if err != nil {
		t.Fatal(err)
	}
	empty.Body.Close()
	if empty.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty reject = %d", empty.StatusCode)
	}
	reject, err := client.Post(srv.URL()+"/api/approvals/"+id+"/reject", "application/json", strings.NewReader(`{"reason":"do not share"}`))
	if err != nil {
		t.Fatal(err)
	}
	reject.Body.Close()
	if reject.StatusCode != http.StatusOK {
		t.Fatalf("reject = %d", reject.StatusCode)
	}
	hidden := <-done
	if hidden.err != nil {
		t.Fatal(hidden.err)
	}
	got = decodeOutcome(t, hidden.text)
	if !hidden.isError || got.OK || got.Kind != "user_rejected" || got.Reason != "do not share" || got.Result != nil {
		t.Fatalf("hidden = %+v %s", got, hidden.text)
	}
	stored, err = store.GetToolCall(context.Background(), pending[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != db.ToolCallRejected || stored.Decision == nil || *stored.Decision != db.DecisionReject || stored.DecisionReason == nil || *stored.DecisionReason != "do not share" || stored.ExecutionStatus == nil || *stored.ExecutionStatus != db.ExecutionOK || stored.Result == nil || *stored.Result != "true" {
		t.Fatalf("stored reject = %+v", stored)
	}
}

func TestSeeResultsCancelAndTimeout(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name: "local", Type: "mock", ConfigJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	sealMockToken(t, srv, store, "local", "ok")
	session := mcpSessionID(t, srv)

	done := make(chan callOutcome, 1)
	go func() {
		done <- callMock(srv, session, "loop", "stop this")
	}()
	waitPendingCommand(t, store, "loop")
	pending, err := store.ListPendingToolCalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := pending[0].ID
	path := strconv.FormatInt(id, 10)

	started := make(chan int, 1)
	go func() {
		resp, err := client.Post(srv.URL()+"/api/approvals/"+path+"/results", "application/json", strings.NewReader(`{}`))
		if err != nil {
			started <- 0
			return
		}
		resp.Body.Close()
		started <- resp.StatusCode
	}()
	waitToolStatus(t, store, id, db.ToolCallExecuting)
	cancel, err := client.Post(srv.URL()+"/api/approvals/"+path+"/cancel", "application/json", strings.NewReader(`{"reason":"stop this run"}`))
	if err != nil {
		t.Fatal(err)
	}
	var stopped approvalView
	if err := json.NewDecoder(cancel.Body).Decode(&stopped); err != nil {
		cancel.Body.Close()
		t.Fatal(err)
	}
	cancel.Body.Close()
	if cancel.StatusCode != http.StatusOK || stopped.Status != db.ToolCallCancelled || stopped.Decision != "" || stopped.ExecutionStatus != db.ExecutionCancelled || stopped.Result != "" || stopped.DecisionReason != "stop this run" {
		t.Fatalf("cancel = %d %+v", cancel.StatusCode, stopped)
	}
	if status := <-started; status != http.StatusOK {
		t.Fatalf("results = %d", status)
	}
	outcome := <-done
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	got := decodeOutcome(t, outcome.text)
	if !outcome.isError || got.Kind != "user_cancelled_mid_tool_call" || got.Reason != "stop this run" {
		t.Fatalf("cancel result = %+v %s", got, outcome.text)
	}
	stored, err := store.GetToolCall(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Decision != nil || stored.Result != nil || stored.ExecutionStatus == nil || *stored.ExecutionStatus != db.ExecutionCancelled {
		t.Fatalf("stored cancel = %+v", stored)
	}
	if _, err := store.PutSetting(context.Background(), executionTimeoutKey, "1"); err != nil {
		t.Fatal(err)
	}

	go func() {
		done <- callMock(srv, session, "loop", "let it expire")
	}()
	waitPendingCommand(t, store, "loop")
	pending, err = store.ListPendingToolCalls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id = pending[0].ID
	timed, err := client.Post(srv.URL()+"/api/approvals/"+strconv.FormatInt(id, 10)+"/results", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var expired approvalView
	if err := json.NewDecoder(timed.Body).Decode(&expired); err != nil {
		timed.Body.Close()
		t.Fatal(err)
	}
	timed.Body.Close()
	if timed.StatusCode != http.StatusOK || expired.Status != db.ToolCallTimedOut || expired.Decision != "" || expired.ExecutionStatus != db.ExecutionTimeout || expired.Result != "" {
		t.Fatalf("timeout = %d %+v", timed.StatusCode, expired)
	}
	outcome = <-done
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	got = decodeOutcome(t, outcome.text)
	if !outcome.isError || got.Kind != "tool_call_timed_out" || got.Error != "Execution timed out." {
		t.Fatalf("timeout result = %+v %s", got, outcome.text)
	}
	stored, err = store.GetToolCall(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Decision != nil || stored.Result != nil || stored.Status != db.ToolCallTimedOut || stored.ExecutionStatus == nil || *stored.ExecutionStatus != db.ExecutionTimeout {
		t.Fatalf("stored timeout = %+v", stored)
	}
}

func TestExecutionTimeoutSettingAndResourceOverride(t *testing.T) {
	store := openMCPStore(t)
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)

	got, err := client.Get(srv.URL() + "/api/settings/execution-timeout")
	if err != nil {
		t.Fatal(err)
	}
	var setting struct {
		Seconds int `json:"seconds"`
	}
	if err := json.NewDecoder(got.Body).Decode(&setting); err != nil {
		got.Body.Close()
		t.Fatal(err)
	}
	got.Body.Close()
	if got.StatusCode != http.StatusOK || setting.Seconds != 30 {
		t.Fatalf("default timeout = %d %+v", got.StatusCode, setting)
	}

	created, err := client.Post(srv.URL()+"/api/resources", "application/json", strings.NewReader(`{"name":"local","type":"mock","config":{"host":"db.example"},"secrets":{"token":"ok"},"executionTimeoutSeconds":4}`))
	if err != nil {
		t.Fatal(err)
	}
	var view resourceView
	if err := json.NewDecoder(created.Body).Decode(&view); err != nil {
		created.Body.Close()
		t.Fatal(err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated || view.ExecutionTimeoutSeconds == nil || *view.ExecutionTimeoutSeconds != 4 {
		t.Fatalf("create = %d %+v", created.StatusCode, view)
	}
	stored, err := store.GetResource(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ExecutionTimeoutMs == nil || *stored.ExecutionTimeoutMs != 4000 {
		t.Fatalf("stored timeout = %v", stored.ExecutionTimeoutMs)
	}

	kept, err := http.NewRequest(http.MethodPut, srv.URL()+"/api/resources/"+strconv.FormatInt(view.ID, 10), strings.NewReader(`{"name":"local","type":"mock","config":{"host":"db.example"},"secrets":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	kept.Header.Set("Content-Type", "application/json")
	keptResp, err := client.Do(kept)
	if err != nil {
		t.Fatal(err)
	}
	keptResp.Body.Close()
	if keptResp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d", keptResp.StatusCode)
	}
	stored, err = store.GetResource(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ExecutionTimeoutMs == nil || *stored.ExecutionTimeoutMs != 4000 {
		t.Fatalf("timeout was cleared = %v", stored.ExecutionTimeoutMs)
	}

	cleared, err := http.NewRequest(http.MethodPut, srv.URL()+"/api/resources/"+strconv.FormatInt(view.ID, 10), strings.NewReader(`{"name":"local","type":"mock","config":{"host":"db.example"},"secrets":{},"executionTimeoutSeconds":null}`))
	if err != nil {
		t.Fatal(err)
	}
	cleared.Header.Set("Content-Type", "application/json")
	clearedResp, err := client.Do(cleared)
	if err != nil {
		t.Fatal(err)
	}
	clearedResp.Body.Close()
	if clearedResp.StatusCode != http.StatusOK {
		t.Fatalf("clear = %d", clearedResp.StatusCode)
	}
	stored, err = store.GetResource(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ExecutionTimeoutMs != nil {
		t.Fatalf("timeout = %v", stored.ExecutionTimeoutMs)
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

func mcpSessionID(t *testing.T, srv *Server) string {
	t.Helper()
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
	if session == "" {
		t.Fatal("missing session id")
	}
	return session
}

func waitToolStatus(t *testing.T, store *db.Store, id int64, status string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		call, err := store.GetToolCall(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if call.Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("call %d did not become %s", id, status)
}

func callMock(srv *Server, session, command, reason string) callOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

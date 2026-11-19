package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"gatekeep/internal/db"
)

func TestRecentWindowSplitsQueueAndHistory(t *testing.T) {
	store := openMCPStore(t)
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)

	now := time.Now()
	hour, _ := db.RecentWindow{Count: 1, Unit: db.UnitHours}.Start(now)
	minutes, _ := db.RecentWindow{Count: 60, Unit: db.UnitMinutes}.Start(now)
	pending := insertCall(t, store, "waiting", db.ToolCallPending, nil)
	current := insertCall(t, store, "this hour", db.ToolCallApproved, ptr(hour.Add(time.Second)))
	previous := insertCall(t, store, "previous hour", db.ToolCallRejected, ptr(hour.Add(-time.Second)))
	older := insertCall(t, store, "older", db.ToolCallExpired, ptr(hour.Add(-3*time.Hour)))

	window := getWindow(t, client, srv)
	if window.Count != 1 || window.Unit != db.UnitHours {
		t.Fatalf("default window = %+v", window)
	}
	queue := getCalls(t, client, srv, "/api/approvals")
	history := getCalls(t, client, srv, "/api/history")
	if len(queue) != 2 || queue[0].ID != pending.ID || queue[1].ID != current.ID {
		t.Fatalf("hour queue = %+v", queue)
	}
	if !hasIDs(history, previous.ID, older.ID) || history[0].ID != previous.ID {
		t.Fatalf("hour history = %+v", history)
	}

	putWindow(t, client, srv, 60, db.UnitMinutes)
	queue = getCalls(t, client, srv, "/api/approvals")
	history = getCalls(t, client, srv, "/api/history")
	if minutes.Before(hour) {
		if !hasIDs(queue, pending.ID, current.ID, previous.ID) || queue[0].ID != pending.ID {
			t.Fatalf("minute queue = %+v", queue)
		}
		if !hasIDs(history, older.ID) {
			t.Fatalf("minute history = %+v", history)
		}
	} else if !hasIDs(queue, pending.ID, current.ID) || !hasIDs(history, previous.ID, older.ID) {
		t.Fatalf("minute queue = %+v history = %+v", queue, history)
	}

	putWindow(t, client, srv, 2, db.UnitCalls)
	queue = getCalls(t, client, srv, "/api/approvals")
	history = getCalls(t, client, srv, "/api/history")
	if !hasIDs(queue, pending.ID, current.ID, previous.ID) || !hasIDs(history, older.ID) {
		t.Fatalf("calls queue = %+v history = %+v", queue, history)
	}

	cleared, err := http.NewRequest(http.MethodDelete, srv.URL()+"/api/history", nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := client.Do(cleared)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Deleted int64 `json:"deleted"`
	}
	if err := json.NewDecoder(deleted.Body).Decode(&body); err != nil {
		deleted.Body.Close()
		t.Fatal(err)
	}
	deleted.Body.Close()
	if deleted.StatusCode != http.StatusOK || body.Deleted != 1 {
		t.Fatalf("clear = %d %+v", deleted.StatusCode, body)
	}
	if _, err := store.GetToolCall(t.Context(), older.ID); err != db.ErrNotFound {
		t.Fatalf("older call error = %v", err)
	}
	queue = getCalls(t, client, srv, "/api/approvals")
	history = getCalls(t, client, srv, "/api/history")
	if !hasIDs(queue, pending.ID, current.ID, previous.ID) || len(history) != 0 {
		t.Fatalf("after clear queue = %+v history = %+v", queue, history)
	}

	bad, err := http.NewRequest(http.MethodPut, srv.URL()+"/api/settings/recent-window", strings.NewReader(`{"count":0,"unit":"hours"}`))
	if err != nil {
		t.Fatal(err)
	}
	bad.Header.Set("Content-Type", "application/json")
	rejected, err := client.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	rejected.Body.Close()
	if rejected.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid window = %d", rejected.StatusCode)
	}
}

func TestDeleteOneFinishedCall(t *testing.T) {
	store := openMCPStore(t)
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	now := time.Now()
	pending := insertCall(t, store, "waiting", db.ToolCallPending, nil)
	finished := insertCall(t, store, "done", db.ToolCallApproved, &now)

	open, err := http.NewRequest(http.MethodDelete, srv.URL()+"/api/approvals/"+strconv.FormatInt(pending.ID, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := client.Do(open)
	if err != nil {
		t.Fatal(err)
	}
	blocked.Body.Close()
	if blocked.StatusCode != http.StatusConflict {
		t.Fatalf("delete pending = %d", blocked.StatusCode)
	}

	one, err := http.NewRequest(http.MethodDelete, srv.URL()+"/api/approvals/"+strconv.FormatInt(finished.ID, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	gone, err := client.Do(one)
	if err != nil {
		t.Fatal(err)
	}
	gone.Body.Close()
	if gone.StatusCode != http.StatusOK {
		t.Fatalf("delete finished = %d", gone.StatusCode)
	}
	if _, err := store.GetToolCall(t.Context(), finished.ID); err != db.ErrNotFound {
		t.Fatalf("finished call error = %v", err)
	}
	if _, err := store.GetToolCall(t.Context(), pending.ID); err != nil {
		t.Fatal(err)
	}
}

func insertCall(t *testing.T, store *db.Store, command, status string, finished *time.Time) db.ToolCall {
	t.Helper()
	call, err := store.CreateToolCall(t.Context(), db.ToolCall{
		ResourceName: "local",
		HarnessName:  "Claude",
		Command:      command,
		Reason:       command,
		Status:       status,
		FinishedAt:   finished,
	})
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func ptr(value time.Time) *time.Time {
	return &value
}

func getWindow(t *testing.T, client *http.Client, srv *Server) db.RecentWindow {
	t.Helper()
	response, err := client.Get(srv.URL() + "/api/settings/recent-window")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var window db.RecentWindow
	if err := json.NewDecoder(response.Body).Decode(&window); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("window status = %d", response.StatusCode)
	}
	return window
}

func putWindow(t *testing.T, client *http.Client, srv *Server, count int, unit string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"count": count, "unit": unit})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPut, srv.URL()+"/api/settings/recent-window", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save window = %d", response.StatusCode)
	}
}

func getCalls(t *testing.T, client *http.Client, srv *Server, path string) []approvalView {
	t.Helper()
	response, err := client.Get(srv.URL() + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var calls []approvalView
	if err := json.NewDecoder(response.Body).Decode(&calls); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s = %d", path, response.StatusCode)
	}
	return calls
}

func hasIDs(calls []approvalView, want ...int64) bool {
	if len(calls) != len(want) {
		return false
	}
	got := map[int64]bool{}
	for _, call := range calls {
		got[call.ID] = true
	}
	for _, id := range want {
		if !got[id] {
			return false
		}
	}
	return true
}

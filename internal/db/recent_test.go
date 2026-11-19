package db

import (
	"testing"
	"time"
)

func TestRecentWindowSnapsToTheCurrentUnit(t *testing.T) {
	loc := time.FixedZone("UTC+6", 6*60*60)
	now := time.Date(2026, 11, 19, 20, 52, 40, 123, loc)

	checks := []struct {
		count int
		unit  string
		want  time.Time
	}{
		{1, UnitHours, time.Date(2026, 11, 19, 20, 0, 0, 0, loc)},
		{2, UnitHours, time.Date(2026, 11, 19, 19, 0, 0, 0, loc)},
		{1, UnitMinutes, time.Date(2026, 11, 19, 20, 52, 0, 0, loc)},
		{60, UnitMinutes, time.Date(2026, 11, 19, 19, 53, 0, 0, loc)},
		{30, UnitSeconds, time.Date(2026, 11, 19, 20, 52, 11, 0, loc)},
		{1, UnitDays, time.Date(2026, 11, 19, 0, 0, 0, 0, loc)},
		{2, UnitDays, time.Date(2026, 11, 18, 0, 0, 0, 0, loc)},
	}
	for _, check := range checks {
		window := RecentWindow{Count: check.count, Unit: check.unit}
		got, ok := window.Start(now)
		if !ok || !got.Equal(check.want) {
			t.Fatalf("%d %s start = %s ok=%v, want %s", check.count, check.unit, got, ok, check.want)
		}
	}

	if _, ok := (RecentWindow{Count: 5, Unit: UnitCalls}).Start(now); ok {
		t.Fatal("tool-call window returned a time")
	}
	if _, err := ParseRecentWindow("0 hours"); err == nil {
		t.Fatal("zero count was accepted")
	}
	if got, err := ParseRecentWindow("60 minutes"); err != nil || got.Count != 60 || got.Unit != UnitMinutes {
		t.Fatalf("parse = %+v %v", got, err)
	}
}

func TestRecentSplitClearAndReopen(t *testing.T) {
	ctx := t.Context()
	path := t.TempDir() + "/gatekeep.db"
	store := openStore(t, path)
	now := time.Date(2026, 11, 19, 20, 52, 40, 0, time.Local)
	hour := RecentWindow{Count: 1, Unit: UnitHours}
	start, ok := hour.Start(now)
	if !ok {
		t.Fatal("hour window has no start")
	}

	pending := finishedCall(t, store, "waiting", ToolCallPending, nil)
	inside := finishedCall(t, store, "inside", ToolCallApproved, ptrTime(start.Add(time.Second)))
	outside := finishedCall(t, store, "outside", ToolCallRejected, ptrTime(start.Add(-time.Second)))

	queue, err := store.ListApprovalToolCalls(ctx, hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 2 || queue[0].ID != pending.ID || queue[1].ID != inside.ID {
		t.Fatalf("queue = %+v", commands(queue))
	}
	history, err := store.ListHistoryToolCalls(ctx, hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(history, outside.ID) {
		t.Fatalf("history = %+v", commands(history))
	}

	minutes := RecentWindow{Count: 60, Unit: UnitMinutes}
	queue, err = store.ListApprovalToolCalls(ctx, minutes, now)
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(queue, pending.ID, inside.ID, outside.ID) || queue[0].ID != pending.ID {
		t.Fatalf("60 minutes queue = %+v", commands(queue))
	}

	calls := RecentWindow{Count: 1, Unit: UnitCalls}
	queue, err = store.ListApprovalToolCalls(ctx, calls, now)
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(queue, pending.ID, inside.ID) {
		t.Fatalf("last call queue = %+v", commands(queue))
	}
	history, err = store.ListHistoryToolCalls(ctx, calls, now)
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(history, outside.ID) {
		t.Fatalf("last call history = %+v", commands(history))
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openStore(t, path)
	defer reopened.Close()
	history, err = reopened.ListHistoryToolCalls(ctx, calls, now)
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(history, outside.ID) {
		t.Fatalf("reopened history = %+v", commands(history))
	}
	deleted, err := reopened.ClearHistoryToolCalls(ctx, calls, now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d", deleted)
	}
	if _, err := reopened.GetToolCall(ctx, outside.ID); err != ErrNotFound {
		t.Fatalf("cleared call error = %v", err)
	}
	queue, err = reopened.ListApprovalToolCalls(ctx, calls, now)
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(queue, pending.ID, inside.ID) {
		t.Fatalf("queue after clear = %+v", commands(queue))
	}
	history, err = reopened.ListHistoryToolCalls(ctx, calls, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("history after clear = %+v", commands(history))
	}
}

func finishedCall(t *testing.T, store *Store, command, status string, finished *time.Time) ToolCall {
	t.Helper()
	call, err := store.CreateToolCall(t.Context(), ToolCall{
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

func ptrTime(value time.Time) *time.Time {
	return &value
}

func sameIDs(calls []ToolCall, want ...int64) bool {
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

func commands(calls []ToolCall) []string {
	names := make([]string, len(calls))
	for i, call := range calls {
		names[i] = call.Command
	}
	return names
}

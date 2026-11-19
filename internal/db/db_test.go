package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRecordsPersistAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "Application Support", "gatekeep.db")

	store := openStore(t, path)
	timeout := int64(15000)
	resource, err := store.CreateResource(ctx, Resource{
		Name:               "orders",
		Type:               "mysql",
		ConfigJSON:         `{"host":"127.0.0.1"}`,
		SecretsCiphertext:  []byte("ciphertext"),
		ExecutionTimeoutMs: &timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	setting, err := store.PutSetting(ctx, "approval_timeout", "5m")
	if err != nil {
		t.Fatal(err)
	}
	harness, err := store.CreateHarness(ctx, "Claude Desktop")
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateHarnessToken(ctx, harness.ID, "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	call, err := store.CreateToolCall(ctx, ToolCall{
		ResourceID:   &resource.ID,
		ResourceName: resource.Name,
		HarnessID:    &harness.ID,
		HarnessName:  harness.Name,
		Command:      "SELECT id FROM orders",
		Reason:       "count open orders",
		Status:       "pending",
	})
	if err != nil {
		t.Fatal(err)
	}

	resource.Name = "orders-prod"
	resource.ConfigJSON = `{"host":"db.internal"}`
	if err := store.UpdateResource(ctx, resource); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutSetting(ctx, setting.Key, "10m"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateHarness(ctx, harness.ID, "Claude"); err != nil {
		t.Fatal(err)
	}
	decision := "reject"
	reason := "too broad"
	call.Status = "rejected"
	call.Decision = &decision
	call.DecisionReason = &reason
	if err := store.UpdateToolCall(ctx, call); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openStore(t, path)
	defer reopened.Close()

	gotResource, err := reopened.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotResource.Name != "orders-prod" || string(gotResource.SecretsCiphertext) != "ciphertext" {
		t.Fatalf("resource = %+v", gotResource)
	}
	gotSetting, err := reopened.GetSetting(ctx, "approval_timeout")
	if err != nil {
		t.Fatal(err)
	}
	if gotSetting.Value != "10m" {
		t.Fatalf("setting = %+v", gotSetting)
	}
	gotHarness, err := reopened.GetHarness(ctx, harness.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotHarness.Name != "Claude" {
		t.Fatalf("harness = %+v", gotHarness)
	}
	gotCall, err := reopened.GetToolCall(ctx, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotCall.Status != "rejected" || gotCall.Decision == nil || *gotCall.Decision != "reject" {
		t.Fatalf("tool call = %+v", gotCall)
	}
	if gotCall.ResourceName != "orders" {
		t.Fatalf("resource snapshot = %s", gotCall.ResourceName)
	}

	if err := reopened.DeleteHarnessToken(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if err := reopened.DeleteToolCall(ctx, call.ID); err != nil {
		t.Fatal(err)
	}
	if err := reopened.DeleteResource(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}
	if err := reopened.DeleteHarness(ctx, harness.ID); err != nil {
		t.Fatal(err)
	}
	if err := reopened.DeleteSetting(ctx, setting.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.GetToolCall(ctx, call.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted tool call error = %v", err)
	}
}

func TestDeletingResourceKeepsToolCallSnapshot(t *testing.T) {
	ctx := context.Background()
	store := openStore(t, filepath.Join(t.TempDir(), "gatekeep.db"))
	defer store.Close()

	resource, err := store.CreateResource(ctx, Resource{Name: "cache", Type: "redis"})
	if err != nil {
		t.Fatal(err)
	}
	harness, err := store.CreateHarness(ctx, "Cursor")
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateHarnessToken(ctx, harness.ID, "hash-2")
	if err != nil {
		t.Fatal(err)
	}
	call, err := store.CreateToolCall(ctx, ToolCall{
		ResourceID:   &resource.ID,
		ResourceName: resource.Name,
		HarnessID:    &harness.ID,
		HarnessName:  harness.Name,
		Command:      "GET user:1",
		Reason:       "read a cached user",
		Status:       "pending",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteResource(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetToolCall(ctx, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResourceID != nil || got.ResourceName != "cache" {
		t.Fatalf("tool call = %+v", got)
	}

	if err := store.DeleteHarness(ctx, harness.ID); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetToolCall(ctx, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.HarnessID != nil || got.HarnessName != "Cursor" {
		t.Fatalf("tool call = %+v", got)
	}
	if err := store.DeleteHarnessToken(ctx, token.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("token delete error = %v", err)
	}
}

func TestDefaultPath(t *testing.T) {
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "gatekeep.db" || filepath.Base(filepath.Dir(path)) != "GateKeep" {
		t.Fatalf("path = %s", path)
	}
}

func openStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

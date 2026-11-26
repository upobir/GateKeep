package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestResourceMigrationPreservesReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	script, err := migrationFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(legacy, 1, string(script)); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`
 INSERT INTO resources(id, name, type, created_at, updated_at) VALUES(7, 'test', 'mysql', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
 INSERT INTO resource_secrets(resource_id, generation, ciphertext) VALUES(7, 0, X'0102');
 INSERT INTO tool_calls(id, resource_id, resource_name, harness_name, command, reason, status, created_at, updated_at)
 VALUES(1, 7, 'test', 'agent', 'SELECT 1', 'test', 'pending', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
 `); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store := openStore(t, path)
	ctx := context.Background()
	got, err := store.GetResource(ctx, 7)
	if err != nil || got.Name != "test" || got.ResourceText != "" || string(got.SecretsCiphertext) != "\x01\x02" {
		t.Fatalf("migrated = %+v, %v", got, err)
	}
	call, err := store.GetToolCall(ctx, 1)
	if err != nil || call.ResourceID == nil || *call.ResourceID != 7 {
		t.Fatalf("call = %+v, %v", call, err)
	}
	if err := store.UpdateResourceText(ctx, 7, "persisted context"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateResource(ctx, Resource{Name: "test", Type: "http"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateResource(ctx, Resource{Name: "test", Type: "mysql"}); err == nil {
		t.Fatal("duplicate type/name accepted")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openStore(t, path)
	defer reopened.Close()
	got, err = reopened.GetResource(ctx, 7)
	if err != nil || got.ResourceText != "persisted context" {
		t.Fatalf("reopened = %+v, %v", got, err)
	}
	if err := reopened.DeleteResource(ctx, 7); err != nil {
		t.Fatal(err)
	}
	call, err = reopened.GetToolCall(ctx, 1)
	if err != nil || call.ResourceID != nil {
		t.Fatalf("foreign key enforcement was not restored: %+v, %v", call, err)
	}
}

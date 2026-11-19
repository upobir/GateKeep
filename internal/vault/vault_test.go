package vault

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gatekeep/internal/db"
)

func TestSecretSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gatekeep.db")
	store := openStore(t, path)
	svc := New(store)

	if _, err := svc.Setup(ctx, "short", "short"); !errors.Is(err, ErrPasswordShort) {
		t.Fatalf("short password error = %v", err)
	}
	if _, err := svc.Setup(ctx, "correct horse", "other horse"); !errors.Is(err, ErrPasswordMatch) {
		t.Fatalf("mismatch error = %v", err)
	}

	if _, err := svc.Setup(ctx, "correct horse", "correct horse"); err != nil {
		t.Fatal(err)
	}
	sealed, err := svc.Seal([]byte("db password"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutSetting(ctx, "resource_secret", base64.StdEncoding.EncodeToString(sealed)); err != nil {
		t.Fatal(err)
	}
	store.Close()

	reopened := openStore(t, path)
	svc = New(reopened)
	if _, err := svc.Open(sealed); !errors.Is(err, ErrLocked) {
		t.Fatalf("open before unlock = %v", err)
	}
	if _, err := svc.Unlock(ctx, "wrong password"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password = %v", err)
	}
	if _, err := svc.Unlock(ctx, "correct horse"); err != nil {
		t.Fatal(err)
	}
	setting, err := reopened.GetSetting(ctx, "resource_secret")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := base64.StdEncoding.DecodeString(setting.Value)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := svc.Open(stored)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "db password" {
		t.Fatalf("plain = %q", plain)
	}

	svc.Lock()
	if _, err := svc.Open(stored); !errors.Is(err, ErrLocked) {
		t.Fatalf("open after lock = %v", err)
	}
}

func TestSessionExpires(t *testing.T) {
	ctx := context.Background()
	store := openStore(t, filepath.Join(t.TempDir(), "gatekeep.db"))
	svc := New(store)
	start := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return start }

	session, err := svc.Setup(ctx, "correct horse", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !svc.Valid(session.Token) {
		t.Fatal("session should be valid")
	}
	svc.now = func() time.Time { return start.Add(defaultUnlock) }
	if !svc.Expire() {
		t.Fatal("expected expiry")
	}
	if svc.Valid(session.Token) {
		t.Fatal("expired session was accepted")
	}
}

func TestChangePasswordReencryptsSecrets(t *testing.T) {
	ctx := context.Background()
	store := openStore(t, filepath.Join(t.TempDir(), "gatekeep.db"))
	svc := New(store)
	if _, err := svc.Setup(ctx, "correct horse", "correct horse"); err != nil {
		t.Fatal(err)
	}
	sealed, err := svc.Seal([]byte("db password"))
	if err != nil {
		t.Fatal(err)
	}
	resource, err := store.CreateResource(ctx, db.Resource{
		Name:              "orders",
		Type:              "mysql",
		SecretsCiphertext: sealed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateResource(ctx, db.Resource{Name: "plain", Type: "mysql"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangePassword(ctx, "wrong password", "new password", "new password"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password = %v", err)
	}
	if err := svc.ChangePassword(ctx, "correct horse", "short", "short"); !errors.Is(err, ErrPasswordShort) {
		t.Fatalf("short password = %v", err)
	}
	if err := svc.ChangePassword(ctx, "correct horse", "new password", "other"); !errors.Is(err, ErrPasswordMatch) {
		t.Fatalf("mismatch = %v", err)
	}
	if err := svc.ChangePassword(ctx, "correct horse", "new password", "new password"); err != nil {
		t.Fatal(err)
	}

	svc.Lock()
	if _, err := svc.Unlock(ctx, "correct horse"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("old password = %v", err)
	}
	if _, err := svc.Unlock(ctx, "new password"); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := svc.Open(got.SecretsCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "db password" {
		t.Fatalf("plain = %q", plain)
	}
	record, err := store.GetVault(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if record.Generation != 2 {
		t.Fatalf("generation = %d", record.Generation)
	}
	retired, err := store.SecretsForGeneration(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 0 {
		t.Fatalf("retired copies = %d", len(retired))
	}
}

func TestInterruptedPasswordChangeKeepsOldPassword(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gatekeep.db")
	store := openStore(t, path)
	svc := New(store)
	if _, err := svc.Setup(ctx, "correct horse", "correct horse"); err != nil {
		t.Fatal(err)
	}
	firstSeal, err := svc.Seal([]byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	secondSeal, err := svc.Seal([]byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateResource(ctx, db.Resource{Name: "one", Type: "mysql", SecretsCiphertext: firstSeal})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateResource(ctx, db.Resource{Name: "two", Type: "mysql", SecretsCiphertext: secondSeal})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.GetVault(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pending := record
	pending.Generation++
	if err := store.CreatePendingVault(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSecretGeneration(ctx, first.ID, pending.Generation, []byte("partial-copy")); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateGeneration(ctx, pending.Generation); err == nil {
		t.Fatal("expected activation to wait for every secret copy")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openStore(t, path)
	active, err := reopened.GetVault(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.Generation != record.Generation {
		t.Fatalf("generation = %d", active.Generation)
	}
	leftover, err := reopened.SecretsForGeneration(ctx, pending.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftover) != 0 {
		t.Fatalf("pending copies = %d", len(leftover))
	}
	svc = New(reopened)
	if _, err := svc.Unlock(ctx, "correct horse"); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.GetResource(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := svc.Open(got.SecretsCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "two" {
		t.Fatalf("plain = %q", plain)
	}
}

func TestRetiredSecretsAreDroppedOnOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gatekeep.db")
	store := openStore(t, path)
	svc := New(store)
	if _, err := svc.Setup(ctx, "correct horse", "correct horse"); err != nil {
		t.Fatal(err)
	}
	sealed, err := svc.Seal([]byte("db password"))
	if err != nil {
		t.Fatal(err)
	}
	resource, err := store.CreateResource(ctx, db.Resource{Name: "orders", Type: "mysql", SecretsCiphertext: sealed})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangePassword(ctx, "correct horse", "new password", "new password"); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSecretGeneration(ctx, resource.ID, 1, []byte("old-copy")); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetVault(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pending := record
	pending.Generation++
	if err := store.CreatePendingVault(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openStore(t, path)
	retired, err := reopened.SecretsForGeneration(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 0 {
		t.Fatalf("retired copies = %d", len(retired))
	}
	active, err := reopened.GetVault(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.Generation != 2 {
		t.Fatalf("generation = %d", active.Generation)
	}
	svc = New(reopened)
	if _, err := svc.Unlock(ctx, "new password"); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := svc.Open(got.SecretsCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "db password" {
		t.Fatalf("plain = %q", plain)
	}
}

func openStore(t *testing.T, path string) *db.Store {
	t.Helper()
	store, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

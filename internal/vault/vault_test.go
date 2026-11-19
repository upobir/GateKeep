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

func openStore(t *testing.T, path string) *db.Store {
	t.Helper()
	store, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

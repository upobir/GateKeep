package mock

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConnection(t *testing.T) {
	p := Plugin{}
	ctx := context.Background()
	if err := p.Test(ctx, map[string]string{"host": "db.example"}, map[string]string{"token": "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Test(ctx, map[string]string{"host": "db.example"}, map[string]string{"token": "no"}); err == nil || err.Error() != "wrong token" {
		t.Fatal(err)
	}
}

func TestExecute(t *testing.T) {
	p := Plugin{}
	ctx := context.Background()
	secrets := map[string]string{"token": "ok"}
	result, err := p.Execute(ctx, "PING", nil, secrets)
	if err != nil || result != true {
		t.Fatalf("ping = %v err = %v", result, err)
	}
	if _, err := p.Execute(ctx, "PING", nil, map[string]string{"token": "no"}); err == nil || err.Error() != "wrong token" {
		t.Fatal(err)
	}
	if _, err := p.Execute(ctx, "break", nil, secrets); err == nil || err.Error() != "something went wrong" {
		t.Fatal(err)
	}
	if _, err := p.Execute(ctx, "nope", nil, secrets); err == nil {
		t.Fatal("expected an unknown command to fail")
	}

	loopCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Execute(loopCtx, "loop", nil, secrets)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("loop = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("loop did not stop when the context was cancelled")
	}
}

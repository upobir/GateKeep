package mock

import (
	"context"
	"errors"
	"testing"
	"time"

	"gatekeep/internal/cache"
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
	mem := cache.New()
	handle := mem.Handle(1, time.Hour)
	result, err := p.Execute(ctx, "PING", nil, secrets, handle)
	if err != nil || result != true {
		t.Fatalf("ping = %v err = %v", result, err)
	}
	if _, err := p.Execute(ctx, "PING", nil, map[string]string{"token": "no"}, handle); err == nil || err.Error() != "wrong token" {
		t.Fatal(err)
	}
	if _, err := p.Execute(ctx, "break", nil, secrets, handle); err == nil || err.Error() != "something went wrong" {
		t.Fatal(err)
	}
	if _, err := p.Execute(ctx, "nope", nil, secrets, handle); err == nil {
		t.Fatal("expected an unknown command to fail")
	}
	first, err := p.Execute(ctx, "nextval", nil, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Execute(ctx, "nextval", nil, secrets, handle)
	if err != nil {
		t.Fatal(err)
	}
	start, ok := first.(int64)
	next, nextOK := second.(int64)
	if !ok || !nextOK || start < 0 || start >= 1_000_000_000 || next != start+1 {
		t.Fatalf("nextval = %v then %v", first, second)
	}
	mem.Expire(1, time.Nanosecond, time.Now().Add(time.Hour))
	if len(mem.List(1, time.Hour, time.Now())) != 0 {
		t.Fatal("idle sequence was not dropped")
	}
	if _, err := p.Execute(ctx, "nextval", nil, secrets, handle); err != nil {
		t.Fatal(err)
	}
	if len(mem.List(1, time.Hour, time.Now())) != 1 {
		t.Fatal("nextval did not start a new sequence")
	}

	loopCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Execute(loopCtx, "loop", nil, secrets, handle)
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

package mock

import (
	"context"
	"testing"
)

func TestConnection(t *testing.T) {
	p := Plugin{}
	ctx := context.Background()
	if err := p.Test(ctx, map[string]string{"host": "db.example"}, map[string]string{"token": "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Test(ctx, map[string]string{"host": "db.example"}, map[string]string{"token": "no"}); err == nil {
		t.Fatal("expected the mock token to be rejected")
	}
	result, err := p.Execute(ctx, "PING", map[string]string{"host": "db.example"}, map[string]string{"token": "ok"})
	if err != nil || result != true {
		t.Fatalf("result = %v err = %v", result, err)
	}
	if _, err := p.Execute(ctx, "PING", map[string]string{"host": "db.example"}, map[string]string{"token": "no"}); err == nil {
		t.Fatal("expected the mock token to be rejected")
	}
}

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
}

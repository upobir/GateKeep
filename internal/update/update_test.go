package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheck(t *testing.T) {
	for _, tt := range []struct {
		name    string
		current string
		status  int
		body    string
		want    string
		wantErr bool
	}{
		{"new patch", "v1.2.3", 200, `{"tag_name":"v1.2.4"}`, "v1.2.4", false},
		{"numeric minor", "v1.9.0", 200, `{"tag_name":"v1.10.0"}`, "v1.10.0", false},
		{"new major", "v1.99.99", 200, `{"tag_name":"v2.0.0"}`, "v2.0.0", false},
		{"equal", "v1.2.3", 200, `{"tag_name":"v1.2.3"}`, "", false},
		{"older", "v2.0.0", 200, `{"tag_name":"v1.9.9"}`, "", false},
		{"draft", "v1.2.3", 200, `{"tag_name":"v2.0.0","draft":true}`, "", false},
		{"prerelease", "v1.2.3", 200, `{"tag_name":"v2.0.0","prerelease":true}`, "", false},
		{"invalid tag", "v1.2.3", 200, `{"tag_name":"v2.0.0-rc.1"}`, "", false},
		{"leading zeros", "v1.2.3", 200, `{"tag_name":"v02.0.0"}`, "", false},
		{"no releases", "v1.2.3", 404, ``, "", false},
		{"rate limited", "v1.2.3", 403, ``, "", true},
		{"server failure", "v1.2.3", 500, ``, "", true},
		{"bad JSON", "v1.2.3", 200, `{`, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "GateKeep/"+tt.current {
					t.Error("unexpected release request")
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			got, err := check(context.Background(), srv.Client(), srv.URL, tt.current)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want error %v", err, tt.wantErr)
			}
			if tt.want == "" {
				if got != nil {
					t.Fatalf("unexpected update: %+v", got)
				}
			} else if got == nil || got.Version != tt.want || got.URL != "https://github.com/upobir/GateKeep/releases/tag/"+tt.want {
				t.Fatalf("release = %+v, want %s", got, tt.want)
			}
		})
	}
}

func TestDevelopmentBuildSkipsRequest(t *testing.T) {
	for _, version := range []string{"dev", "dev-abc123-dirty", "unknown", ""} {
		// An invalid endpoint would fail if a request were attempted.
		got, err := check(context.Background(), http.DefaultClient, ":invalid", version)
		if got != nil || err != nil {
			t.Fatalf("check(%q) = %v, %v", version, got, err)
		}
	}
}

func TestCheckCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Check(ctx, "v1.2.3")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

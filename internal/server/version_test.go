package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gatekeep/internal/buildinfo"
)

func TestVersionUsesBinaryMetadataWithoutSession(t *testing.T) {
	srv, err := New(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.listener.Close()
	response := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("version must not be cached")
	}
	var got map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != buildinfo.Version || got["commit"] != buildinfo.Commit {
		t.Fatalf("version metadata = %v", got)
	}
}

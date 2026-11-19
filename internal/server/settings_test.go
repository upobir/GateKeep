package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"gatekeep/internal/mcp"
)

func TestInstructionsSetting(t *testing.T) {
	store := openMCPStore(t)
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)

	body := getJSON(t, client, srv.URL()+"/api/settings/instructions")
	fallback := mcp.Instructions(srv.URL(), defaultApprovalTimeout, defaultExecutionTimeout)
	if body["text"] != fallback || body["default"] != fallback {
		t.Fatalf("instructions = %#v", body)
	}
	if got := initializeInstructions(t, srv); got != fallback {
		t.Fatalf("initialize = %q", got)
	}

	put := putJSON(t, client, srv.URL()+"/api/settings/instructions", `{"text":"Ask first."}`)
	if put["text"] != "Ask first." || put["default"] != fallback {
		t.Fatalf("saved = %#v", put)
	}
	if got := initializeInstructions(t, srv); got != "Ask first." {
		t.Fatalf("initialize = %q", got)
	}

	resetReq, err := http.NewRequest(http.MethodDelete, srv.URL()+"/api/settings/instructions", nil)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := client.Do(resetReq)
	if err != nil {
		t.Fatal(err)
	}
	resetBody := decodeObject(t, reset)
	if resetBody["text"] != fallback || resetBody["default"] != fallback {
		t.Fatalf("reset = %#v", resetBody)
	}
	if got := initializeInstructions(t, srv); got != fallback {
		t.Fatalf("initialize after reset = %q", got)
	}
}

func TestListenPortSetting(t *testing.T) {
	store := openMCPStore(t)
	if ListenPort(store) != defaultListenPort {
		t.Fatalf("default = %d", ListenPort(store))
	}
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)

	body := getJSON(t, client, srv.URL()+"/api/settings/port")
	if body["port"] != float64(defaultListenPort) || body["current"] != float64(srv.currentPort()) {
		t.Fatalf("port = %#v", body)
	}

	saved := putJSON(t, client, srv.URL()+"/api/settings/port", `{"port":8791}`)
	if saved["port"] != float64(8791) || saved["current"] != float64(srv.currentPort()) {
		t.Fatalf("saved = %#v", saved)
	}
	if ListenPort(store) != 8791 || srv.currentPort() == 8791 {
		t.Fatalf("stored = %d current = %d", ListenPort(store), srv.currentPort())
	}

	bad, err := http.NewRequest(http.MethodPut, srv.URL()+"/api/settings/port", strings.NewReader(`{"port":0}`))
	if err != nil {
		t.Fatal(err)
	}
	bad.Header.Set("Content-Type", "application/json")
	rejected, err := client.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	rejected.Body.Close()
	if rejected.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid port = %d", rejected.StatusCode)
	}
}

func initializeInstructions(t *testing.T, srv *Server) string {
	t.Helper()
	resp := mcpPost(t, srv, "", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "Claude Desktop", "version": "1.0.0"},
		},
	})
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	result, _ := payload["result"].(map[string]any)
	text, _ := result["instructions"].(string)
	return text
}

func getJSON(t *testing.T, client *http.Client, rawURL string) map[string]any {
	t.Helper()
	resp, err := client.Get(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return decodeObject(t, resp)
}

func putJSON(t *testing.T, client *http.Client, rawURL, body string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, rawURL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("put %s = %d %s", rawURL, resp.StatusCode, raw)
	}
	return decodeObject(t, resp)
}

func decodeObject(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %#v", resp.StatusCode, body)
	}
	return body
}

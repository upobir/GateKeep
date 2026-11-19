package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gatekeep/internal/db"
)

func TestMCPLegacyMockTool(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name:              "local",
		Type:              "mock",
		ConfigJSON:        `{"host":"db.example"}`,
		SecretsCiphertext: []byte("super-secret-token"),
	}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)

	initResp := mcpPost(t, srv, "", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "Claude Desktop", "version": "1.0.0"},
		},
	})
	if initResp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d", initResp.StatusCode)
	}
	session := initResp.Header.Get("Mcp-Session-Id")
	if session == "" {
		t.Fatal("missing session id")
	}
	initBody := decodeRPC(t, initResp)
	result := initBody["result"].(map[string]any)
	if result["protocolVersion"] != "2025-11-25" {
		t.Fatalf("protocol = %v", result["protocolVersion"])
	}
	instructions, _ := result["instructions"].(string)
	if !strings.Contains(instructions, "approve") || !strings.Contains(instructions, srv.URL()) || !strings.Contains(instructions, "person can read it") {
		t.Fatalf("instructions = %q", instructions)
	}

	note := mcpPost(t, srv, session, "2025-11-25", map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
	if note.StatusCode != http.StatusAccepted {
		t.Fatalf("initialized status = %d", note.StatusCode)
	}
	note.Body.Close()

	listResp := mcpPost(t, srv, session, "2025-11-25", map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	listBody := decodeRPC(t, listResp)
	mock := findMockTool(t, listBody)

	callResp := mcpPost(t, srv, session, "2025-11-25", map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "mock",
			"arguments": map[string]any{
				"resource": "local",
				"command":  "PING",
				"reason":   "check that the resource is reachable",
			},
		},
	})
	if callResp.StatusCode != http.StatusOK {
		t.Fatalf("call status = %d", callResp.StatusCode)
	}
	callBody := decodeRPC(t, callResp)
	text := toolText(t, callBody)
	if strings.Contains(text, "super-secret-token") || strings.Contains(string(mustJSON(callBody)), "super-secret-token") {
		t.Fatal("response exposed a secret")
	}
	if !strings.Contains(text, srv.URL()+"/approval/") {
		t.Fatalf("result = %q", text)
	}
	if !strings.Contains(text, "has not executed") {
		t.Fatalf("result = %q", text)
	}

	stored, err := store.GetToolCall(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "pending" || stored.Command != "PING" || stored.Reason != "check that the resource is reachable" {
		t.Fatalf("stored = %+v", stored)
	}
	if stored.ResourceName != "local" || stored.HarnessName != "Claude Desktop" {
		t.Fatalf("stored = %+v", stored)
	}
	if mock["name"] != "mock" {
		t.Fatalf("tool = %v", mock["name"])
	}
}

func TestMCPModernMockTool(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name:       "local",
		Type:       "mock",
		ConfigJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)

	discover := mcpPostModern(t, srv, "server/discover", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      "discover-1",
		"method":  "server/discover",
		"params":  modernParams("", nil),
	})
	body := decodeRPC(t, discover)
	result := body["result"].(map[string]any)
	if result["instructions"] == "" {
		t.Fatal("missing instructions")
	}
	versions, _ := result["supportedVersions"].([]any)
	if len(versions) != 1 || versions[0] != "2026-07-28" {
		t.Fatalf("versions = %v", versions)
	}

	listResp := mcpPostModern(t, srv, "tools/list", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  modernParams("", nil),
	})
	findMockTool(t, decodeRPC(t, listResp))

	callResp := mcpPostModern(t, srv, "tools/call", "mock", map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": modernParams("mock", map[string]any{
			"resource": "local",
			"command":  "PING",
			"reason":   "check the mock resource",
		}),
	})
	text := toolText(t, decodeRPC(t, callResp))
	if !strings.Contains(text, srv.URL()+"/approval/1") {
		t.Fatalf("result = %q", text)
	}
	stored, err := store.GetToolCall(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if stored.HarnessName != "Example Client" || stored.Command != "PING" {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestMCPToolsFollowSavedResources(t *testing.T) {
	store := openMCPStore(t)
	srv := startMCPServer(t, store)
	initResp := mcpPost(t, srv, "", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	})
	session := initResp.Header.Get("Mcp-Session-Id")
	initResp.Body.Close()

	empty := listedTools(t, mcpPost(t, srv, session, "2025-11-25", map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}))
	if len(empty) != 0 {
		t.Fatalf("tools = %v", empty)
	}

	for _, name := range []string{"other", "local"} {
		if _, err := store.CreateResource(context.Background(), db.Resource{
			Name: name, Type: "mock", ConfigJSON: `{}`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name: "orders", Type: "mysql", ConfigJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}

	tools := listedTools(t, mcpPost(t, srv, session, "2025-11-25", map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/list",
	}))
	if len(tools) != 1 || tools[0]["name"] != "mock" {
		t.Fatalf("tools = %v", tools)
	}
	properties := tools[0]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	enum := properties["resource"].(map[string]any)["enum"].([]any)
	if len(enum) != 2 || enum[0] != "local" || enum[1] != "other" {
		t.Fatalf("resource enum = %v", enum)
	}
}

func TestMCPCodexDiscoverWithoutMethodHeader(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name: "local", Type: "mock", ConfigJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)

	discover := mcpPost(t, srv, "", "2026-07-28", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "server/discover",
		"params":  modernParams("", nil),
	})
	if discover.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(discover.Body)
		discover.Body.Close()
		t.Fatalf("discover status = %d body = %s", discover.StatusCode, body)
	}
	discovered := decodeRPC(t, discover)
	versions := discovered["result"].(map[string]any)["supportedVersions"].([]any)
	if len(versions) != 1 || versions[0] != "2026-07-28" {
		t.Fatalf("versions = %v", versions)
	}

	listed := mcpPost(t, srv, "", "2026-07-28", map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  modernParams("", nil),
	})
	if listed.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listed.Body)
		listed.Body.Close()
		t.Fatalf("list status = %d body = %s", listed.StatusCode, body)
	}
	findMockTool(t, decodeRPC(t, listed))
}

func TestMCPRejectsUnknownResourceAndOrigin(t *testing.T) {
	store := openMCPStore(t)
	if _, err := store.CreateResource(context.Background(), db.Resource{
		Name:       "orders",
		Type:       "mysql",
		ConfigJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPServer(t, store)
	initResp := mcpPost(t, srv, "", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	})
	session := initResp.Header.Get("Mcp-Session-Id")
	initResp.Body.Close()

	missing := mcpPost(t, srv, session, "2025-06-18", map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "mock",
			"arguments": map[string]any{"resource": "missing", "command": "PING", "reason": "look it up"},
		},
	})
	text := toolText(t, decodeRPC(t, missing))
	if !strings.Contains(text, "unknown resource missing") {
		t.Fatalf("result = %q", text)
	}

	wrongType := mcpPost(t, srv, session, "2025-06-18", map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "mock",
			"arguments": map[string]any{"resource": "orders", "command": "SELECT 1", "reason": "peek"},
		},
	})
	text = toolText(t, decodeRPC(t, wrongType))
	if !strings.Contains(text, "not mock") {
		t.Fatalf("result = %q", text)
	}
	if _, err := store.GetToolCall(context.Background(), 1); err == nil {
		t.Fatal("rejected call was queued")
	}

	noSession := mcpPost(t, srv, "", "2025-06-18", map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/list",
	})
	if noSession.StatusCode != http.StatusOK {
		t.Fatalf("no session status = %d", noSession.StatusCode)
	}
	noSession.Body.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL()+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"x","version":"0"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("origin status = %d", resp.StatusCode)
	}

	getReq, err := http.NewRequest(http.MethodGet, srv.URL()+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", getResp.StatusCode)
	}
}

func openMCPStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "gatekeep.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func startMCPServer(t *testing.T, store *db.Store) *Server {
	t.Helper()
	srv, err := New(0, store)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	return srv
}

func mcpPost(t *testing.T, srv *Server, session, version string, payload map[string]any) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL()+"/mcp", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	if version != "" {
		req.Header.Set("MCP-Protocol-Version", version)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func mcpPostModern(t *testing.T, srv *Server, method, name string, payload map[string]any) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL()+"/mcp", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", method)
	if name != "" {
		req.Header.Set("Mcp-Name", name)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func modernParams(name string, arguments map[string]any) map[string]any {
	params := map[string]any{
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion": "2026-07-28",
			"io.modelcontextprotocol/clientInfo": map[string]any{
				"name":    "Example Client",
				"version": "1.0.0",
			},
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		},
	}
	if name != "" {
		params["name"] = name
	}
	if arguments != nil {
		params["arguments"] = arguments
	}
	return params
}

func decodeRPC(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body = %s", raw)
	}
	if body["error"] != nil {
		t.Fatalf("rpc error = %s", raw)
	}
	return body
}

func listedTools(t *testing.T, resp *http.Response) []map[string]any {
	t.Helper()
	result := decodeRPC(t, resp)["result"].(map[string]any)
	raw, _ := result["tools"].([]any)
	tools := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		tools = append(tools, item.(map[string]any))
	}
	return tools
}

func findMockTool(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	result := body["result"].(map[string]any)
	tools := result["tools"].([]any)
	for _, item := range tools {
		tool := item.(map[string]any)
		if tool["name"] != "mock" {
			continue
		}
		description, _ := tool["description"].(string)
		if !strings.Contains(description, "Mock") || strings.Contains(strings.ToLower(description), "approv") {
			t.Fatalf("description = %q", description)
		}
		schema := tool["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		command := properties["command"].(map[string]any)
		if command["description"] != "One Mock command, such as PING. Put it on a single line." {
			t.Fatalf("command description = %v", command["description"])
		}
		resource := properties["resource"].(map[string]any)
		enum, _ := resource["enum"].([]any)
		if len(enum) != 1 || enum[0] != "local" {
			t.Fatalf("resource enum = %v", enum)
		}
		required := schema["required"].([]any)
		got := map[string]bool{}
		for _, field := range required {
			got[field.(string)] = true
		}
		for _, field := range []string{"resource", "command", "reason"} {
			if !got[field] {
				t.Fatalf("required = %v", required)
			}
		}
		return tool
	}
	t.Fatal("mock tool was not listed")
	return nil
}

func toolText(t *testing.T, body map[string]any) string {
	t.Helper()
	result := body["result"].(map[string]any)
	content := result["content"].([]any)
	first := content[0].(map[string]any)
	return first["text"].(string)
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

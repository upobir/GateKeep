package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"gatekeep/internal/db"
)

func TestMCPResourceText(t *testing.T) {
	store := openMCPStore(t)
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	created := postResource(t, client, srv, `{"name":"test","type":"mock","config":{"host":"db.example"},"secrets":{"token":"ok"}}`)
	// The same connection name is valid under another type.
	_, err := store.CreateResource(context.Background(), db.Resource{Name: "test", Type: "mysql", ResourceText: "MySQL context", ConfigJSON: `{"host":"private-host"}`, SecretsCiphertext: []byte("private-secret")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateResource(context.Background(), db.Resource{Name: "test", Type: "mysql"}); err == nil {
		t.Fatal("duplicate type/name accepted")
	}
	var emptyIDs []int64
	for _, text := range []string{"", " \t\n"} {
		empty, err := store.CreateResource(context.Background(), db.Resource{Name: fmt.Sprintf("empty-%d", len(emptyIDs)), Type: "mock", ResourceText: text})
		if err != nil {
			t.Fatal(err)
		}
		emptyIDs = append(emptyIDs, empty.ID)
	}
	before, err := store.GetResource(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	text := "Orders schema\nUse the read replica. বাংলা"
	updateText := func(body string, want int) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/resources/%d/text", srv.URL(), created.ID), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("save text status = %d, want %d", resp.StatusCode, want)
		}
	}
	raw, _ := json.Marshal(map[string]string{"text": text})
	updateText(string(raw), http.StatusOK)
	updateText(`{}`, http.StatusBadRequest)
	after, err := store.GetResource(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ResourceText != text || before.ConfigJSON != after.ConfigJSON || string(before.SecretsCiphertext) != string(after.SecretsCiphertext) {
		t.Fatalf("text update changed connection: %+v", after)
	}

	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		t.Run(version, func(t *testing.T) {
			rpc := func(method string, params any) map[string]any {
				t.Helper()
				return decodeResourceRPC(t, mcpPost(t, srv, "", version, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}))
			}
			method := "initialize"
			params := any(map[string]any{"protocolVersion": version})
			if version == "2026-07-28" {
				method = "server/discover"
				params = map[string]any{}
			}
			caps := rpc(method, params)["result"].(map[string]any)["capabilities"].(map[string]any)
			if _, ok := caps["resources"]; !ok {
				t.Fatal("missing resources capability")
			}
			listed := rpc("resources/list", map[string]any{})
			data := string(mustJSON(listed))
			if strings.Contains(data, "private-host") || strings.Contains(data, "private-secret") || strings.Contains(data, "read replica") {
				t.Fatalf("list exposed connection data: %s", data)
			}
			resources := listed["result"].(map[string]any)["resources"].([]any)
			names := map[string]bool{}
			for _, entry := range resources {
				names[entry.(map[string]any)["name"].(string)] = true
			}
			if len(resources) != 2 || !names["mysql/test"] || !names["mock/test"] {
				t.Fatalf("resources = %v", resources)
			}
			read := rpc("resources/read", map[string]any{"uri": resourceTextURI(created.ID)})
			content := read["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
			if content["text"] != text || content["mimeType"] != "text/plain" {
				t.Fatalf("content = %v", content)
			}

			for _, uri := range []string{"http://example.com", "gatekeep://resources/01/context", resourceTextURI(99999), resourceTextURI(emptyIDs[0]), resourceTextURI(emptyIDs[1])} {
				got := rpc("resources/read", map[string]any{"uri": uri})
				if got["error"].(map[string]any)["code"] != float64(-32002) {
					t.Fatalf("missing resource = %v", got)
				}
			}
			for _, method := range []string{"resources/list", "resources/read"} {
				got := rpc(method, map[string]any{"cursor": false})
				if got["error"].(map[string]any)["code"] != float64(-32602) {
					t.Fatalf("invalid params = %v", got)
				}
			}
			rpc("resources/templates/list", map[string]any{})
		})
	}
	// A connection edit retains text and its stable URI.
	after.Name = "prod"
	if err := store.UpdateResource(context.Background(), after); err != nil {
		t.Fatal(err)
	}
	fetched, err := store.ResourceByName(context.Background(), "mock", "prod")
	if err != nil || fetched.ResourceText != text || fetched.ID != created.ID {
		t.Fatalf("renamed = %+v, %v", fetched, err)
	}
	updateText(`{"text":""}`, http.StatusOK)
	fetched, err = store.GetResource(context.Background(), created.ID)
	if err != nil || fetched.ResourceText != "" {
		t.Fatal("text was not cleared")
	}
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		listed := decodeResourceRPC(t, mcpPost(t, srv, "", version, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "resources/list"}))
		resources := listed["result"].(map[string]any)["resources"].([]any)
		if len(resources) != 1 || resources[0].(map[string]any)["name"] != "mysql/test" {
			t.Fatalf("list after clearing = %v", listed)
		}
		read := decodeResourceRPC(t, mcpPost(t, srv, "", version, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "resources/read", "params": map[string]any{"uri": resourceTextURI(created.ID)}}))
		if read["error"].(map[string]any)["code"] != float64(-32002) {
			t.Fatalf("read after clearing = %v", read)
		}
	}
}

func TestMCPResourcePagination(t *testing.T) {
	store := openMCPStore(t)
	for i := 0; i < 101; i++ {
		if _, err := store.CreateResource(context.Background(), db.Resource{Name: fmt.Sprintf("test-%03d", i), Type: "mock", ResourceText: "context"}); err != nil {
			t.Fatal(err)
		}
		// Empty documents must not consume slots in a page.
		if _, err := store.CreateResource(context.Background(), db.Resource{Name: fmt.Sprintf("test-%03d-empty", i), Type: "mock", ResourceText: " \t\n"}); err != nil {
			t.Fatal(err)
		}
	}
	srv := startMCPServer(t, store)
	list := func(cursor string) map[string]any {
		return decodeResourceRPC(t, mcpPost(t, srv, "", "2025-11-25", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "resources/list", "params": map[string]any{"cursor": cursor}}))
	}
	first := list("")["result"].(map[string]any)
	if len(first["resources"].([]any)) != 100 {
		t.Fatalf("first page = %v", first)
	}
	second := list(first["nextCursor"].(string))["result"].(map[string]any)
	if len(second["resources"].([]any)) != 1 || second["nextCursor"] != nil {
		t.Fatalf("second page = %v", second)
	}
	for _, cursor := range []string{"-1", "no", "102", "9223372036854775807"} {
		if list(cursor)["error"] == nil {
			t.Fatalf("accepted invalid cursor %s", cursor)
		}
	}
}

// Resource error cases need to inspect JSON-RPC errors as well as results.
func decodeResourceRPC(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

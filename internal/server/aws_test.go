package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gatekeep/internal/db"
	"gatekeep/internal/plugin"
)

func TestAWSResourceAvailabilityAndVault(t *testing.T) {
	store := openMCPStore(t)
	srv := startMCPServer(t, store)
	client := unlockedClient(t, srv)
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	checkAvailability := func(want bool) {
		t.Helper()
		resp, err := client.Get(srv.URL() + "/api/resource-types")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var types []plugin.Info
		if err := json.NewDecoder(resp.Body).Decode(&types); err != nil {
			t.Fatal(err)
		}
		for _, item := range types {
			if item.Type != "aws" {
				continue
			}
			if item.Available != want || (!want && item.UnavailableReason == "") {
				t.Fatalf("AWS availability = %+v", item)
			}
			return
		}
		t.Fatal("AWS type missing")
	}
	checkAvailability(false)
	const payload = `{"name":"aws-test","type":"aws","config":{"region":"us-east-1"},"secrets":{"accessKeyId":"example-access-key","secretAccessKey":"example-secret-key","sessionToken":"example-session-token"}}`
	resp, err := client.Post(srv.URL()+"/api/resources", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unavailable creation = %d", resp.StatusCode)
	}

	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte("#!/bin/sh\nprintf '{\"Account\":\"123456789012\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	checkAvailability(true)
	resource := postResource(t, client, srv, payload)
	stored, err := store.GetResource(context.Background(), resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"example-access-key", "example-secret-key", "example-session-token"} {
		if strings.Contains(stored.ConfigJSON, secret) || strings.Contains(string(stored.SecretsCiphertext), secret) {
			t.Fatal("credential stored as plaintext")
		}
	}
	resp, err = client.Get(srv.URL() + "/api/resources")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "example-") {
		t.Fatal("resource list exposed credentials")
	}

	input := `{"id":` + strconv.FormatInt(resource.ID, 10) + `,"type":"aws","config":{"region":"us-east-1"},"secrets":{}}`
	resp, err = client.Post(srv.URL()+"/api/resources/test", "application/json", strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test saved credentials = %d", resp.StatusCode)
	}

	result, err := srv.runApproved(context.Background(), db.ToolCall{ResourceID: &resource.ID, Command: `["sts","get-caller-identity"]`})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["Account"] != "123456789012" {
		t.Fatalf("result = %#v", result)
	}

	if err := os.Remove(filepath.Join(dir, "aws")); err != nil {
		t.Fatal(err)
	}
	checkAvailability(false)
	if _, err := srv.runApproved(context.Background(), db.ToolCall{ResourceID: &resource.ID, Command: `["sts","get-caller-identity"]`}); err == nil {
		t.Fatal("execution accepted missing CLI")
	}
}

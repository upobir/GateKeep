package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gatekeep/internal/db"
)

func TestServerBindsLoopbackAndStops(t *testing.T) {
	srv, err := New(0, nil)
	if err != nil {
		t.Fatal(err)
	}

	host, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("host = %s", host)
	}
	if port == "" || port == "0" {
		t.Fatalf("port = %s", port)
	}

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve()
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(srv.URL())
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "GateKeep") {
		t.Fatalf("body = %s", body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}

	conn, err := net.DialTimeout("tcp", host+":"+port, 200*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("port still accepting connections")
	}
}

func TestHealthAndEvents(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "gatekeep.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

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

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(srv.URL() + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	var status healthStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if status.Server != "up" || status.Database != "up" {
		t.Fatalf("health = %+v", status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL()+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content-type = %s", stream.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(stream.Body)
	var lines []string
	for len(lines) < 2 {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.TrimRight(line, "\r\n"))
	}
	if lines[0] != "event: status" {
		t.Fatalf("event = %q", lines[0])
	}
	if !strings.Contains(lines[1], `"database":"up"`) {
		t.Fatalf("data = %q", lines[1])
	}

	closed, err := New(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	go closed.Serve()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		closed.Shutdown(ctx)
	})
	downResp, err := client.Get(closed.URL() + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer downResp.Body.Close()
	var down healthStatus
	if err := json.NewDecoder(downResp.Body).Decode(&down); err != nil {
		t.Fatal(err)
	}
	if down.Database != "down" {
		t.Fatalf("database = %s", down.Database)
	}
}

func TestNewRejectsInvalidPort(t *testing.T) {
	if _, err := New(-1, nil); err == nil {
		t.Fatal("expected error for negative port")
	}
	if _, err := New(65536, nil); err == nil {
		t.Fatal("expected error for port above 65535")
	}
}

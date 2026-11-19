package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServerBindsLoopbackAndStops(t *testing.T) {
	srv, err := New(0)
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

func TestNewRejectsInvalidPort(t *testing.T) {
	if _, err := New(-1); err == nil {
		t.Fatal("expected error for negative port")
	}
	if _, err := New(65536); err == nil {
		t.Fatal("expected error for port above 65535")
	}
}

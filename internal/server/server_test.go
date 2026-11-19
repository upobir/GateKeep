package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
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

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 15 * time.Second, Jar: jar}
	locked, err := http.Get(srv.URL() + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	locked.Body.Close()
	if locked.StatusCode != http.StatusUnauthorized {
		t.Fatalf("locked health = %d", locked.StatusCode)
	}

	setupBody := strings.NewReader(`{"password":"correct horse","confirm":"correct horse"}`)
	setup, err := client.Post(srv.URL()+"/api/setup", "application/json", setupBody)
	if err != nil {
		t.Fatal(err)
	}
	setup.Body.Close()
	if setup.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d", setup.StatusCode)
	}

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
	stream, err := client.Do(req)
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
	downResp.Body.Close()
	if downResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("health without a database = %d", downResp.StatusCode)
	}

	lockResp, err := client.Post(srv.URL()+"/api/lock", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	lockResp.Body.Close()
	if lockResp.StatusCode != http.StatusOK {
		t.Fatalf("lock = %d", lockResp.StatusCode)
	}
	wrong, err := client.Post(srv.URL()+"/api/unlock", "application/json", strings.NewReader(`{"password":"nope nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", wrong.StatusCode)
	}
	after, err := client.Get(srv.URL() + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	after.Body.Close()
	if after.StatusCode != http.StatusUnauthorized {
		t.Fatalf("health after a bad password = %d", after.StatusCode)
	}
}

func TestResourceCRUDAndConnection(t *testing.T) {
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

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 15 * time.Second, Jar: jar}
	setup, err := client.Post(srv.URL()+"/api/setup", "application/json", strings.NewReader(`{"password":"correct horse","confirm":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	setup.Body.Close()
	if setup.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d", setup.StatusCode)
	}

	rejected, err := client.Post(srv.URL()+"/api/resources/test", "application/json", strings.NewReader(`{"type":"mock","config":{"host":"db.example"},"secrets":{"token":"no"}}`))
	if err != nil {
		t.Fatal(err)
	}
	rejected.Body.Close()
	if rejected.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("rejected test = %d", rejected.StatusCode)
	}

	created, err := client.Post(srv.URL()+"/api/resources", "application/json", strings.NewReader(`{"name":"local","type":"mock","config":{"host":"db.example"},"secrets":{"token":"ok"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var view resourceView
	if err := json.NewDecoder(created.Body).Decode(&view); err != nil {
		created.Body.Close()
		t.Fatal(err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", created.StatusCode)
	}
	if view.Config["host"] != "db.example" || !view.HasSecrets {
		t.Fatalf("view = %+v", view)
	}

	list, err := client.Get(srv.URL() + "/api/resources")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(list.Body)
	list.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "ok") {
		t.Fatalf("secret leaked in list: %s", body)
	}

	one, err := client.Get(srv.URL() + fmt.Sprintf("/api/resources/%d", view.ID))
	if err != nil {
		t.Fatal(err)
	}
	oneBody, err := io.ReadAll(one.Body)
	one.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if one.StatusCode != http.StatusOK || !strings.Contains(string(oneBody), `"token":"ok"`) {
		t.Fatalf("edit resource = %d %s", one.StatusCode, oneBody)
	}

	stored, err := store.GetResource(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.SecretsCiphertext), "token") {
		t.Fatal("secret was stored in plaintext")
	}

	kept, err := client.Post(srv.URL()+"/api/resources/test", "application/json", strings.NewReader(fmt.Sprintf(`{"id":%d,"type":"mock","config":{"host":"db.example"},"secrets":{}}`, view.ID)))
	if err != nil {
		t.Fatal(err)
	}
	kept.Body.Close()
	if kept.StatusCode != http.StatusOK {
		t.Fatalf("test with stored secret = %d", kept.StatusCode)
	}

	updated, err := http.NewRequest(http.MethodPut, srv.URL()+fmt.Sprintf("/api/resources/%d", view.ID), strings.NewReader(`{"name":"renamed","type":"mock","config":{"host":"other.example"},"secrets":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	updated.Header.Set("Content-Type", "application/json")
	updateResp, err := client.Do(updated)
	if err != nil {
		t.Fatal(err)
	}
	updateResp.Body.Close()
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d", updateResp.StatusCode)
	}

	deleted, err := http.NewRequest(http.MethodDelete, srv.URL()+fmt.Sprintf("/api/resources/%d", view.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	deleteResp, err := client.Do(deleted)
	if err != nil {
		t.Fatal(err)
	}
	deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d", deleteResp.StatusCode)
	}
}

func TestShutdownClosesEventStream(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "gatekeep.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	srv, err := New(0, store)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve()
	}()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	setup, err := client.Post(srv.URL()+"/api/setup", "application/json", strings.NewReader(`{"password":"correct horse","confirm":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	setup.Body.Close()
	if setup.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d", setup.StatusCode)
	}

	streamReq, err := http.NewRequest(http.MethodGet, srv.URL()+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.Do(streamReq)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, stream.Body)
		readDone <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("shutdown took %s", time.Since(started))
	}

	select {
	case err := <-readDone:
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("event stream stayed open after shutdown")
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}

func TestPasswordChangeKeepsSecrets(t *testing.T) {
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

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 15 * time.Second, Jar: jar}
	setup, err := client.Post(srv.URL()+"/api/setup", "application/json", strings.NewReader(`{"password":"correct horse","confirm":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	setup.Body.Close()
	created, err := client.Post(srv.URL()+"/api/resources", "application/json", strings.NewReader(`{"name":"local","type":"mock","config":{"host":"db.example"},"secrets":{"token":"ok"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var view resourceView
	if err := json.NewDecoder(created.Body).Decode(&view); err != nil {
		created.Body.Close()
		t.Fatal(err)
	}
	created.Body.Close()

	changed, err := client.Post(srv.URL()+"/api/password", "application/json", strings.NewReader(`{"current":"correct horse","password":"new password","confirm":"new password"}`))
	if err != nil {
		t.Fatal(err)
	}
	changed.Body.Close()
	if changed.StatusCode != http.StatusOK {
		t.Fatalf("change = %d", changed.StatusCode)
	}
	lock, err := client.Post(srv.URL()+"/api/lock", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	lock.Body.Close()
	old, err := client.Post(srv.URL()+"/api/unlock", "application/json", strings.NewReader(`{"password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	old.Body.Close()
	if old.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password = %d", old.StatusCode)
	}
	unlocked, err := client.Post(srv.URL()+"/api/unlock", "application/json", strings.NewReader(`{"password":"new password"}`))
	if err != nil {
		t.Fatal(err)
	}
	unlocked.Body.Close()
	if unlocked.StatusCode != http.StatusOK {
		t.Fatalf("new password = %d", unlocked.StatusCode)
	}
	one, err := client.Get(srv.URL() + fmt.Sprintf("/api/resources/%d", view.ID))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(one.Body)
	one.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if one.StatusCode != http.StatusOK || !strings.Contains(string(body), `"token":"ok"`) {
		t.Fatalf("resource = %d %s", one.StatusCode, body)
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

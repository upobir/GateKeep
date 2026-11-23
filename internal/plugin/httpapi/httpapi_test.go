package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	call, err := parseCommand(`{"method":"post","path":"/v1/orders","params":{"a":"1"},"headers":{"X-Id":"7"},"body":{"x":1}}`)
	if err != nil {
		t.Fatal(err)
	}
	if call.Method != "POST" || call.Path != "/v1/orders" || call.Params["a"] != "1" || string(call.Body) != `{"x":1}` {
		t.Fatalf("call = %+v", call)
	}
	bad := []string{
		``, `[]`, `{"path":"/"}`, `{"method":"GET"}`, `{"method":"GET","path":"v1"}`,
		`{"method":"GET","path":"/a?b=1"}`, `{"method":"GET","path":"/a#x"}`,
		`{"method":"TRACE","path":"/"}`, `{"method":"GET","path":"/","query":{}}`,
		`{"method":"GET","path":"/"} {}`, `{"method":"GET","path":"/","params":{"a":1}}`,
	}
	for _, text := range bad {
		if _, err := parseCommand(text); err == nil {
			t.Fatalf("expected %q to fail", text)
		}
	}
}

func TestCleanHost(t *testing.T) {
	for in, want := range map[string]string{"API.example.com": "api.example.com", " svc:8443 ": "svc:8443", "[::1]:9000": "[::1]:9000"} {
		got, err := cleanHost(in)
		if err != nil || got != want {
			t.Fatalf("cleanHost(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "https://api.example.com", "a/b", "user@host", "host:0", "host:99999", "host?x"} {
		if _, err := cleanHost(in); err == nil {
			t.Fatalf("expected %q to fail", in)
		}
	}
}

func TestBuildKeepsHost(t *testing.T) {
	tg := target{scheme: "https", host: "api.example.com"}
	for _, path := range []string{"//evil.com/x", "/@evil.com", "/../../x"} {
		call, err := parseCommand(`{"method":"GET","path":` + quote(path) + `}`)
		if err != nil {
			continue
		}
		req, err := tg.build(context.Background(), call)
		if err == nil && req.URL.Host != "api.example.com" {
			t.Fatalf("path %q reached host %q", path, req.URL.Host)
		}
	}
}

func TestBuildHeadersAndBody(t *testing.T) {
	tg := target{scheme: "https", host: "api.example.com", header: "Authorization", value: "Bearer s3cret"}
	build := func(text string) (*http.Request, error) {
		call, err := parseCommand(text)
		if err != nil {
			t.Fatal(err)
		}
		return tg.build(context.Background(), call)
	}
	for _, text := range []string{
		`{"method":"GET","path":"/","headers":{"authorization":"x"}}`,
		`{"method":"GET","path":"/","headers":{"Host":"evil.com"}}`,
		`{"method":"GET","path":"/","headers":{"X-A":"a\r\nX-B: b"}}`,
		`{"method":"POST","path":"/","body":{"a":1}}`,
		`{"method":"POST","path":"/","headers":{"Content-Type":"application/x-www-form-urlencoded"},"body":{"a":1}}`,
		`{"method":"POST","path":"/","headers":{"Content-Type":"application/xml"},"body":{"a":"1"}}`,
	} {
		if _, err := build(text); err == nil {
			t.Fatalf("expected %s to fail", text)
		}
	}

	req, err := build(`{"method":"POST","path":"/v2/a%2Fb","params":{"q":"a b&c"},"headers":{"Content-Type":"application/x-www-form-urlencoded"},"body":{"To":"+1555","Channel":"sms"}}`)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(req.Body)
	if req.URL.String() != "https://api.example.com/v2/a%2Fb?q=a+b%26c" {
		t.Fatalf("url = %s", req.URL)
	}
	if string(body) != "Channel=sms&To=%2B1555" || req.Header.Get("Authorization") != "Bearer s3cret" {
		t.Fatalf("body = %s headers = %v", body, req.Header)
	}

	req, err = build(`{"method":"POST","path":"/","body":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(req.Body)
	if string(body) != "hello" || !strings.HasPrefix(req.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("text body = %q %v", body, req.Header)
	}
}

func TestReadTarget(t *testing.T) {
	tg, err := readTarget(map[string]string{"tls": "on", "host": "api.example.com", "auth": "api-key", "apiKeyHeader": "x-api-key"}, map[string]string{"apiKey": "k"})
	if err != nil || tg.scheme != "https" || tg.header != "X-Api-Key" || tg.value != "k" {
		t.Fatalf("api key = %+v %v", tg, err)
	}
	tg, err = readTarget(map[string]string{"host": "svc", "auth": "basic", "username": "u"}, map[string]string{"password": "p"})
	if err != nil || tg.scheme != "http" || tg.value != "Basic dTpw" {
		t.Fatalf("basic = %+v %v", tg, err)
	}
	for _, config := range []map[string]string{
		{"host": "svc", "auth": "bearer"},
		{"host": "svc", "auth": "api-key", "apiKeyHeader": "X-Key"},
		{"host": "svc", "auth": "basic"},
		{"host": "svc", "auth": "oauth"},
		{"host": "svc", "tls": "maybe"},
	} {
		if _, err := readTarget(config, nil); err == nil {
			t.Fatalf("expected %v to fail", config)
		}
	}
}

func TestExecute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/echo":
			raw, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"auth": r.Header.Get("Authorization"), "body": string(raw), "q": r.URL.Query().Get("q")})
		case "/redirect":
			http.Redirect(w, r, "https://evil.example.com/", http.StatusFound)
		case "/binary":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 0x50, 0xff, 0xfe})
		default:
			_, _ = io.WriteString(w, "plain")
		}
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	config := map[string]string{"host": host, "auth": "bearer"}
	secrets := map[string]string{"token": "s3cret"}

	got := run(t, `{"method":"POST","path":"/echo","params":{"q":"x"},"headers":{"Content-Type":"application/json"},"body":{"a":1}}`, config, secrets)
	if got.Status != 200 {
		t.Fatalf("status = %d", got.Status)
	}
	raw, _ := json.Marshal(got.Body)
	if string(raw) != `{"auth":"Bearer *****","body":"{\"a\":1}","q":"x"}` {
		t.Fatalf("echo body = %s", raw)
	}

	got = run(t, `{"method":"GET","path":"/redirect"}`, config, secrets)
	if got.Status != http.StatusFound || got.Headers["Location"] != "https://evil.example.com/" {
		t.Fatalf("redirect = %+v", got)
	}

	got = run(t, `{"method":"GET","path":"/binary"}`, config, secrets)
	if got.Body != nil || !strings.Contains(got.Note, "binary") {
		t.Fatalf("binary = %+v", got)
	}

	got = run(t, `{"method":"GET","path":"/plain"}`, config, secrets)
	if got.Body != "plain" {
		t.Fatalf("plain = %+v", got)
	}

	if err := (Plugin{}).Test(context.Background(), config, secrets); err != nil {
		t.Fatalf("test = %v", err)
	}
}

func run(t *testing.T, command string, config, secrets map[string]string) response {
	t.Helper()
	value, err := Plugin{}.Execute(context.Background(), command, config, secrets, nil)
	if err != nil {
		t.Fatal(err)
	}
	return value.(response)
}

func quote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

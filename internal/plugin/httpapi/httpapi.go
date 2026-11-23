// Package httpapi sends one HTTP request to a saved host. The resource holds
// the host and the credentials, so the harness never sees them.
package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gatekeep/internal/plugin"
)

// maxResponseBytes caps how much of a response body is read and returned.
const maxResponseBytes = 1 << 20

// Plugin is the HTTP resource type.
type Plugin struct{}

func (Plugin) Info() plugin.Info {
	return plugin.Info{
		Type:  "http",
		Label: "HTTP",
		Fields: []plugin.Field{
			{Name: "tls", Label: "TLS", Optional: true},
			{Name: "host", Label: "Host"},
			{Name: "auth", Label: "Authentication", Optional: true},
			{Name: "apiKeyHeader", Label: "API key header", Optional: true},
			{Name: "apiKey", Label: "API key", Secret: true, Optional: true},
			{Name: "token", Label: "Bearer token", Secret: true, Optional: true},
			{Name: "username", Label: "Username", Optional: true},
			{Name: "password", Label: "Password", Secret: true, Optional: true},
		},
		ToolDescription: "Sends one HTTP request to this resource's host. The resource adds its own authentication, so never put credentials in the request. Redirects are not followed. ok: true means the request completed and the user shared its result, even for HTTP error responses such as 400, 401 or 500. Inspect result.status to determine HTTP success before reporting success to the user.",
		CommandDescription: `A JSON object: {"method":"POST","path":"/v1/orders","params":{"limit":"10"},"headers":{"Content-Type":"application/json"},"body":{"item":"x"}}. ` +
			`method and path are required. path starts with / and has no query string; put query values in params (an object of strings). ` +
			`headers is an object of strings. The Content-Type header decides what body is: application/json takes any JSON value; ` +
			`application/x-www-form-urlencoded takes an object of strings; any other type, or no Content-Type (sent as text/plain), takes a string. ` +
			`The result is {"status":200,"headers":{...},"body":...}. body is JSON when the response is JSON, otherwise text. ` +
			`A binary or oversized body is left out and "note" says why.`,
	}
}

// Test opens a connection to the host, with a TLS handshake when TLS is on.
// There is no standard endpoint to call, so it sends no request.
func (Plugin) Test(ctx context.Context, config, secrets map[string]string) error {
	target, err := readTarget(config, secrets)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var conn net.Conn
	if target.scheme == "https" {
		dialer := &tls.Dialer{Config: target.tlsConfig()}
		conn, err = dialer.DialContext(ctx, "tcp", target.address())
	} else {
		var dialer net.Dialer
		conn, err = dialer.DialContext(ctx, "tcp", target.address())
	}
	if err != nil {
		return err
	}
	return conn.Close()
}

// Execute sends one request and returns the response. Secret values are
// replaced in the response and in errors.
func (Plugin) Execute(ctx context.Context, command string, config, secrets map[string]string, _ plugin.Cache) (any, error) {
	target, err := readTarget(config, secrets)
	if err != nil {
		return nil, err
	}
	call, err := parseCommand(command)
	if err != nil {
		return nil, err
	}
	req, err := target.build(ctx, call)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSClientConfig:     target.tlsConfig(),
		TLSHandshakeTimeout: 10 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		// A redirect could carry the credentials to another host. The harness
		// gets the 3xx response and can ask again.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, target.redactError(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, target.redactError(err)
	}
	return target.result(resp, raw), nil
}

// target is the resource's host and credentials.
type target struct {
	scheme     string
	host       string
	skipVerify bool
	auth       string
	header     string // header the auth goes in
	value      string // full header value
	secrets    []string
}

func readTarget(config, secrets map[string]string) (target, error) {
	t := target{}
	switch config["tls"] {
	case "", "off":
		t.scheme = "http"
	case "on":
		t.scheme = "https"
	case "skip-verify":
		t.scheme = "https"
		t.skipVerify = true
	default:
		return target{}, errors.New("tls must be off, on or skip-verify")
	}
	host, err := cleanHost(config["host"])
	if err != nil {
		return target{}, err
	}
	t.host = host
	t.auth = config["auth"]
	switch t.auth {
	case "", "none":
		t.auth = "none"
	case "api-key":
		name := http.CanonicalHeaderKey(strings.TrimSpace(config["apiKeyHeader"]))
		if name == "" {
			return target{}, errors.New("API key header is required")
		}
		if !validHeaderName(name) {
			return target{}, fmt.Errorf("API key header %q is not a valid header name", name)
		}
		if secrets["apiKey"] == "" {
			return target{}, errors.New("API key is required")
		}
		t.header, t.value = name, secrets["apiKey"]
		t.secrets = []string{secrets["apiKey"]}
	case "bearer":
		if secrets["token"] == "" {
			return target{}, errors.New("bearer token is required")
		}
		t.header, t.value = "Authorization", "Bearer "+secrets["token"]
		t.secrets = []string{secrets["token"]}
	case "basic":
		user := config["username"]
		if user == "" {
			return target{}, errors.New("username is required")
		}
		req := &http.Request{Header: http.Header{}}
		req.SetBasicAuth(user, secrets["password"])
		t.header, t.value = "Authorization", req.Header.Get("Authorization")
		t.secrets = []string{strings.TrimPrefix(t.value, "Basic ")}
		if secrets["password"] != "" {
			t.secrets = append(t.secrets, secrets["password"])
		}
	default:
		return target{}, errors.New("authentication must be none, api-key, bearer or basic")
	}
	return t, nil
}

// cleanHost accepts a host name with an optional port, and nothing else.
func cleanHost(text string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(text))
	if host == "" {
		return "", errors.New("host is required")
	}
	if strings.Contains(host, "://") {
		return "", errors.New("host is a name like api.example.com, without http:// or https://")
	}
	parsed, err := url.Parse("http://" + host)
	if err != nil || parsed.Host != host || parsed.User != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("host %q must be a host name with an optional port, like api.example.com:8443", text)
	}
	if port := parsed.Port(); port != "" {
		var number int
		if _, err := fmt.Sscanf(port, "%d", &number); err != nil || number < 1 || number > 65535 {
			return "", errors.New("port must be between 1 and 65535")
		}
	}
	return host, nil
}

func (t target) address() string {
	parsed := &url.URL{Host: t.host}
	if parsed.Port() != "" {
		return t.host
	}
	if t.scheme == "https" {
		return net.JoinHostPort(parsed.Hostname(), "443")
	}
	return net.JoinHostPort(parsed.Hostname(), "80")
}

func (t target) tlsConfig() *tls.Config {
	parsed := &url.URL{Host: t.host}
	return &tls.Config{ServerName: parsed.Hostname(), InsecureSkipVerify: t.skipVerify}
}

func (t target) redact(text string) string {
	for _, secret := range t.secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "*****")
		}
	}
	return text
}

func (t target) redactError(err error) error {
	text := t.redact(err.Error())
	if text == err.Error() {
		return err
	}
	return errors.New(text)
}

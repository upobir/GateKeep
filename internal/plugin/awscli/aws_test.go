package awscli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOutputLimit(t *testing.T) {
	var b limitedBuffer
	text := strings.Repeat("x", 2<<20)
	n, err := io.Copy(&b, strings.NewReader(text))
	if err != nil || n != int64(len(text)) || b.buffer.Len() != 1<<20 || !b.overflow {
		t.Fatalf("copy = %d, %v, length=%d, overflow=%v", n, err, b.buffer.Len(), b.overflow)
	}
}

func fakeAWS(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return filepath.Join(dir, "aws")
}

func credentials() map[string]string {
	return map[string]string{"accessKeyId": "test-access-id", "secretAccessKey": "test-secret-key", "sessionToken": "test-session-token"}
}

func TestConfiguredCLIPath(t *testing.T) {
	p := Plugin{}
	t.Setenv("PATH", t.TempDir())
	if err := p.Available(); err != nil {
		t.Fatal("AWS form must remain available for configuring a path")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "aws custom")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '{\"Account\":\"custom\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := map[string]string{"cliPath": path}
	// A configured path wins even if PATH contains another executable.
	fakeAWS(t, "exit 1\n")
	result, err := p.Execute(context.Background(), `["sts","get-caller-identity"]`, config, credentials(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["Account"] != "custom" {
		t.Fatalf("result = %#v", result)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	for _, config := range []map[string]string{nil, {"cliPath": ""}, {"cliPath": "  "}, {"cliPath": dir}, {"cliPath": filepath.Join(dir, "missing")}, {"cliPath": path}} {
		if _, err := p.Execute(context.Background(), `["sts","get-caller-identity"]`, config, credentials(), nil); err == nil || !strings.Contains(err.Error(), "AWS CLI failed:") {
			t.Fatalf("execution with invalid path = %v", err)
		}
		if err := p.Test(context.Background(), config, credentials()); err == nil || !strings.Contains(err.Error(), "AWS CLI failed:") {
			t.Fatalf("connection test with invalid path = %v", err)
		}
	}
}

func TestExecuteUsesOnlyResourceCredentials(t *testing.T) {
	t.Setenv("AWS_PROFILE", "wrong-profile")
	t.Setenv("AWS_ACCESS_KEY_ID", "wrong-key")
	t.Setenv("AWS_ENDPOINT_URL", "https://wrong.example")
	cliPath := fakeAWS(t, `
set -eu
[ "$AWS_ACCESS_KEY_ID" = test-access-id ]
[ "$AWS_SECRET_ACCESS_KEY" = test-secret-key ]
[ "$AWS_SESSION_TOKEN" = test-session-token ]
[ "$AWS_REGION" = us-east-1 ]
[ "$AWS_DEFAULT_REGION" = us-east-1 ]
[ "$AWS_CONFIG_FILE" = /dev/null ]
[ "$AWS_SHARED_CREDENTIALS_FILE" = /dev/null ]
[ "${AWS_PROFILE-unset}" = unset ]
[ "${AWS_ENDPOINT_URL-unset}" = unset ]
[ -z "$AWS_PAGER" ]
[ "$AWS_CLI_AUTO_PROMPT" = off ]
[ "$AWS_DEFAULT_OUTPUT" = json ]
[ "$#" = 3 ]
[ "$1" = sts ] && [ "$2" = get-caller-identity ]
[ "$3" = 'literal; $(echo should-not-run)' ]
printf '{"Account":"123456789012","Nested":["%s"],"%s":"masked","Number":1234567890123456789}' "$AWS_SECRET_ACCESS_KEY" "$AWS_ACCESS_KEY_ID"
`)
	result, err := (Plugin{}).Execute(context.Background(), `["sts","get-caller-identity","literal; $(echo should-not-run)"]`, map[string]string{"region": "us-east-1", "cliPath": cliPath}, credentials(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "test-") || !strings.Contains(string(raw), "1234567890123456789") {
		t.Fatalf("result = %s", raw)
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") != "wrong-key" {
		t.Fatal("parent environment changed")
	}
}

func TestEnvironmentClearsOptionalAndInheritedProviders(t *testing.T) {
	secrets := credentials()
	delete(secrets, "sessionToken")
	env := strings.Join(environment([]string{"PATH=/bin", "AWS_SESSION_TOKEN=stale", "AWS_REGION=stale", "AWS_ROLE_ARN=wrong", "AWS_WEB_IDENTITY_TOKEN_FILE=wrong", "AWS_CONTAINER_CREDENTIALS_FULL_URI=wrong", "BOTO_CONFIG=wrong"}, nil, secrets), "\n")
	if strings.Contains(env, "stale") || strings.Contains(env, "wrong") || !strings.Contains(env, "AWS_SESSION_TOKEN=\n") || !strings.Contains(env, "PATH=/bin") {
		t.Fatalf("environment = %s", env)
	}
}

func TestCommandValidation(t *testing.T) {
	for _, command := range []string{
		`null`, `[]`, `["sts"]`, `["sts",null]`, `["sts",1]`, `["sts","get-caller-identity",null]`, `["","get-caller-identity"]`,
		`["sts","get-caller-identity","--profile=other"]`,
		`["sts","get-caller-identity","--prof","other"]`,
		`["sts","get-caller-identity","--debug"]`,
		`["sts","get-caller-identity","--endpoint-url=https://other.example"]`,
		`["sts","get-caller-identity","--no-sign-request"]`,
		`["sts","get-caller-identity","--cli-auto-prompt"]`,
		`["configure","get","aws_secret_access_key"]`, `["sso","login"]`, `["sts","assume-role"]`,
	} {
		t.Run(command, func(t *testing.T) {
			if _, err := parseCommand(command); err == nil {
				t.Fatal("accepted invalid command")
			}
		})
	}
	if _, err := parseCommand(`["ec2","describe-instances","--region","us-east-1"]`); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteResultsAndErrors(t *testing.T) {
	for _, tc := range []struct{ name, body, wantErr string }{
		{"empty", "exit 0\n", ""},
		{"array", `printf '[{"Name":"test"}]'`, ""},
		{"error", `printf '%s' "$AWS_SECRET_ACCESS_KEY" >&2; exit 1`, "*****"},
		{"non-json", `printf 'not json'`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cliPath := fakeAWS(t, tc.body)
			_, err := (Plugin{}).Execute(context.Background(), `["sts","get-caller-identity"]`, map[string]string{"cliPath": cliPath}, credentials(), nil)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || strings.Contains(err.Error(), "test-secret-key") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	cliPath := fakeAWS(t, "exec /bin/sleep 10\n")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (Plugin{}).Execute(ctx, `["sts","get-caller-identity"]`, map[string]string{"cliPath": cliPath}, credentials(), nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("cancellation = %v after %s", err, time.Since(start))
	}
}

func TestOutputOverrideReturnsRawText(t *testing.T) {
	for _, flag := range []string{"--output", "--output=text", "--out"} {
		t.Run(flag, func(t *testing.T) {
			cliPath := fakeAWS(t, `
set -eu
[ "$AWS_DEFAULT_OUTPUT" = json ]
[ "$3" = '`+flag+`' ]
printf '  account\t%s\n\n' "$AWS_SECRET_ACCESS_KEY"
`)
			args := []string{"sts", "get-caller-identity", flag}
			if !strings.Contains(flag, "=") {
				args = append(args, "text")
			}
			command, _ := json.Marshal(args)
			result, err := (Plugin{}).Execute(context.Background(), string(command), map[string]string{"cliPath": cliPath}, credentials(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if result != "  account\t*****\n\n" {
				t.Fatalf("raw text = %q", result)
			}
		})
	}
}

// Package awscli runs the user's installed AWS CLI with vault-held credentials.
package awscli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gatekeep/internal/plugin"
)

type Plugin struct{}

func (Plugin) Info() plugin.Info {
	return plugin.Info{
		Type: "aws", Label: "AWS (CLI)",
		Fields: []plugin.Field{
			{Name: "accessKeyId", Label: "Access key ID", Secret: true},
			{Name: "secretAccessKey", Label: "Secret access key", Secret: true},
			{Name: "sessionToken", Label: "Session token", Secret: true, Optional: true},
			{Name: "region", Label: "Region", Optional: true},
			{Name: "cliPath", Label: "AWS CLI path"},
		},
		ToolDescription:    "Runs the installed AWS CLI using this resource's saved credentials. GateKeep disables the pager and interactive prompts. Do not supply --color, --no-cli-pager, --cli-auto-prompt or --no-cli-auto-prompt. Output defaults to JSON; --output can override it. Non-JSON output is returned as text. Never include credentials in commands. Local AWS profiles are not used.",
		CommandDescription: `A JSON array of AWS CLI arguments, for example ["sts","get-caller-identity"] or ["ec2","describe-instances","--region","us-east-1"]. Omit the aws executable. GateKeep sets AWS_PAGER="" and AWS_CLI_AUTO_PROMPT=off, and defaults AWS_DEFAULT_OUTPUT=json. Do not supply --color, --no-cli-pager, --cli-auto-prompt or --no-cli-auto-prompt. Authentication is managed by GateKeep; do not include --profile or credentials. --output is allowed when a different format is needed. Interactive login, configuration and credential-issuing commands are not supported.`,
	}
}

// The executable is configured per resource, so AWS is always selectable.
func (Plugin) Available() error { return nil }

func (p Plugin) Test(ctx context.Context, config, secrets map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := p.Execute(ctx, `["sts","get-caller-identity"]`, config, secrets, nil)
	return err
}

func (p Plugin) Execute(ctx context.Context, command string, config, secrets map[string]string, _ plugin.Cache) (any, error) {
	args, err := parseCommand(command)
	if err != nil {
		return nil, err
	}
	if secrets["accessKeyId"] == "" || secrets["secretAccessKey"] == "" {
		return nil, errors.New("access key ID and secret access key are required")
	}
	cmd := exec.CommandContext(ctx, strings.TrimSpace(config["cliPath"]), args...)
	cmd.Env = environment(os.Environ(), config, secrets)
	cmd.WaitDelay = time.Second
	stdout, stderr := &limitedBuffer{}, &limitedBuffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		message := strings.TrimSpace(redact(stderr.String(), secrets))
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("AWS CLI failed: %s", message)
	}
	if stdout.overflow {
		return nil, errors.New("AWS CLI output exceeds 1 MiB; narrow the command with filters or --query")
	}
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return map[string]any{}, nil
	}
	var result any
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.UseNumber()
	if !json.Valid([]byte(output)) || decoder.Decode(&result) != nil {
		return redact(stdout.String(), secrets), nil
	}
	// Redact after decoding so escaped secret characters cannot bypass masking.
	return redactValue(result, secrets), nil
}

func parseCommand(command string) ([]string, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(command), &raw); err != nil || len(raw) < 2 {
		return nil, errors.New("command must be a JSON array of strings containing an AWS service and operation")
	}
	args := make([]string, len(raw))
	for i, value := range raw {
		if string(value) == "null" || json.Unmarshal(value, &args[i]) != nil {
			return nil, errors.New("every AWS argument must be a string")
		}
	}
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("AWS arguments cannot contain NUL characters")
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		flag, _, _ := strings.Cut(arg, "=")
		// The CLI accepts abbreviated options; reject those forms as well.
		for _, blocked := range []string{"--profile", "--debug", "--endpoint-url", "--no-sign-request", "--no-verify-ssl", "--cli-auto-prompt", "--no-cli-auto-prompt", "--cli-pager", "--no-cli-pager"} {
			if flag == "--" || strings.HasPrefix(blocked, flag) {
				return nil, fmt.Errorf("%s is managed by GateKeep and cannot be supplied", flag)
			}
		}
	}
	if strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") || args[0] == "" || args[1] == "" {
		return nil, errors.New("start the argument array with the AWS service and operation")
	}
	switch args[0] {
	case "configure", "login", "logout", "sso", "sso-oidc", "sso-login", "history", "help":
		return nil, errors.New("AWS configuration and interactive authentication commands are not supported")
	case "sts":
		switch args[1] {
		case "assume-role", "assume-role-with-saml", "assume-role-with-web-identity", "get-session-token", "get-federation-token":
			return nil, errors.New("credential-issuing STS commands are not supported")
		}
	}
	return args, nil
}

func environment(inherited []string, config, secrets map[string]string) []string {
	env := make([]string, 0, len(inherited)+12)
	for _, entry := range inherited {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(name), "AWS_") || name == "BOTO_CONFIG" {
			continue
		}
		env = append(env, entry)
	}
	env = append(env,
		"AWS_ACCESS_KEY_ID="+secrets["accessKeyId"],
		"AWS_SECRET_ACCESS_KEY="+secrets["secretAccessKey"],
		"AWS_SESSION_TOKEN="+secrets["sessionToken"],
		"AWS_CONFIG_FILE="+os.DevNull,
		"AWS_SHARED_CREDENTIALS_FILE="+os.DevNull,
		"BOTO_CONFIG="+os.DevNull,
		"AWS_EC2_METADATA_DISABLED=true",
		"AWS_PAGER=",
		"AWS_DEFAULT_OUTPUT=json",
		"AWS_CLI_AUTO_PROMPT=off",
	)
	if region := strings.TrimSpace(config["region"]); region != "" {
		env = append(env, "AWS_REGION="+region, "AWS_DEFAULT_REGION="+region)
	}
	return env
}

func redact(text string, secrets map[string]string) string {
	for _, key := range []string{"secretAccessKey", "sessionToken", "accessKeyId"} {
		if value := secrets[key]; value != "" {
			text = strings.ReplaceAll(text, value, "*****")
		}
	}
	return text
}

func redactValue(value any, secrets map[string]string) any {
	switch v := value.(type) {
	case string:
		return redact(v, secrets)
	case []any:
		for i := range v {
			v[i] = redactValue(v[i], secrets)
		}
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[redact(key, secrets)] = redactValue(item, secrets)
		}
		return out
	}
	return value
}

// Keep draining output after reaching the limit so the process cannot block.
type limitedBuffer struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (1 << 20) - b.buffer.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.overflow = true
	}
	b.buffer.Write(p)
	return n, nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }

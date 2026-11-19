// Package mock is the sample resource plugin. Copy this package when adding a type.
package mock

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gatekeep/internal/plugin"
)

type Plugin struct{}

func (Plugin) Info() plugin.Info {
	return plugin.Info{
		Type:  "mock",
		Label: "Mock",
		Fields: []plugin.Field{
			{Name: "host", Label: "Host"},
			{Name: "token", Label: "Token", Secret: true},
		},
		ToolDescription:    "Runs one Mock command.",
		CommandDescription: "One Mock command: ping, loop, or break.",
	}
}

func (Plugin) Test(_ context.Context, config, secrets map[string]string) error {
	if config["host"] == "" {
		return errors.New("host is required")
	}
	if secrets["token"] != "ok" {
		return errors.New("wrong token")
	}
	return nil
}

// Execute runs ping, loop, or break.
// loop blocks on ctx so the execution engine can time it out or cancel it.
func (Plugin) Execute(ctx context.Context, command string, _ map[string]string, secrets map[string]string) (any, error) {
	if secrets["token"] != "ok" {
		return nil, errors.New("wrong token")
	}
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "ping":
		return true, nil
	case "loop":
		<-ctx.Done()
		return nil, ctx.Err()
	case "break":
		return nil, errors.New("something went wrong")
	default:
		return nil, fmt.Errorf("unknown command %q", strings.TrimSpace(command))
	}
}

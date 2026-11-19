// Package mock is the sample resource plugin. Copy this package when adding a type.
package mock

import (
	"context"
	"errors"

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
		ToolDescription:    "Runs one Mock command and returns sucess.",
		CommandDescription: "One Mock command, such as PING. Put it on a single line.",
	}
}

func (Plugin) Test(_ context.Context, config, secrets map[string]string) error {
	if config["host"] == "" {
		return errors.New("host is required")
	}
	if secrets["token"] != "ok" {
		return errors.New("mock token was rejected")
	}
	return nil
}

// Execute returns true when the saved token is ok.
func (Plugin) Execute(_ context.Context, _ string, _ map[string]string, secrets map[string]string) (any, error) {
	if secrets["token"] != "ok" {
		return nil, errors.New("mock token was rejected")
	}
	return true, nil
}

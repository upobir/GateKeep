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

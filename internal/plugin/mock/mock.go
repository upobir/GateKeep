// Package mock is the sample resource plugin. Copy this package when adding a type.
package mock

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
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
		CommandDescription: "One Mock command: ping, loop, break, or nextval. nextval returns the next integer from a sequence that starts at a random seed.",
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

const sequenceObject = "sequence"

// Execute runs ping, loop, break, or nextval.
// loop blocks on ctx so the execution engine can time it out or cancel it.
// nextval keeps an integer named sequence in the resource cache. The first
// call stores a random seed. Each later call returns the next integer.
// After the cache drops the sequence, the next call starts from a new seed.
func (Plugin) Execute(ctx context.Context, command string, _ map[string]string, secrets map[string]string, cache plugin.Cache) (any, error) {
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
	case "nextval":
		return nextValue(cache), nil
	default:
		return nil, fmt.Errorf("unknown command %q", strings.TrimSpace(command))
	}
}

func nextValue(cache plugin.Cache) int64 {
	value, _ := cache.Update(sequenceObject, func(current any, ok bool) (any, bool) {
		if ok {
			if number, isInt := current.(int64); isInt {
				return number + 1, true
			}
		}
		return rand.Int64N(1_000_000_000), true
	})
	number, _ := value.(int64)
	return number
}

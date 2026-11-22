package redis

import (
	"encoding/json"
	"errors"
	"strings"
)

func parseCommand(text string) ([]string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("command is required")
	}
	var args []string
	if err := json.Unmarshal([]byte(text), &args); err != nil {
		return nil, errors.New("command must be a JSON array of strings")
	}
	if len(args) == 0 {
		return nil, errors.New("command must not be empty")
	}
	if strings.TrimSpace(args[0]) == "" {
		return nil, errors.New("command name is required")
	}
	return args, nil
}

func toArgs(argv []string) []any {
	out := make([]any, len(argv))
	for i, arg := range argv {
		out[i] = arg
	}
	return out
}

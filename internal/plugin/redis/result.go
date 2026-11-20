package redis

import (
	"encoding/base64"
	"fmt"
	"unicode/utf8"
)

func mapReply(value any) (any, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return bulk(typed), nil
	case []byte:
		return bulk(string(typed)), nil
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case float64:
		return typed, nil
	case bool:
		return typed, nil
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			mapped, err := mapReply(item)
			if err != nil {
				return nil, err
			}
			out = append(out, mapped)
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			name, ok := key.(string)
			if !ok {
				name = anyString(key)
				if name == "" {
					name = fmt.Sprint(key)
				}
			}
			mapped, err := mapReply(item)
			if err != nil {
				return nil, err
			}
			out[name] = mapped
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			mapped, err := mapReply(item)
			if err != nil {
				return nil, err
			}
			out[key] = mapped
		}
		return out, nil
	default:
		return fmt.Sprint(typed), nil
	}
}

func anyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

func bulk(text string) any {
	if utf8.ValidString(text) {
		return text
	}
	return base64.StdEncoding.EncodeToString([]byte(text))
}
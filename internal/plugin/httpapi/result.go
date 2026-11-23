package httpapi

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

// response is what the harness gets back. Note is set only when body is left out.
type response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    any               `json:"body"`
	Note    string            `json:"note,omitempty"`
}

func (t target) result(resp *http.Response, raw []byte) response {
	out := response{Status: resp.StatusCode, Headers: map[string]string{}}
	for name, values := range resp.Header {
		out.Headers[name] = t.redact(strings.Join(values, ", "))
	}
	contentType := resp.Header.Get("Content-Type")
	switch {
	case len(raw) > maxResponseBytes:
		out.Note = fmt.Sprintf("body left out: larger than %d bytes", maxResponseBytes)
	case len(raw) == 0:
	case !utf8.Valid(raw):
		out.Note = fmt.Sprintf("body left out: binary (%s, %d bytes)", contentTypeOr(contentType), len(raw))
	default:
		text := t.redact(string(raw))
		mediaType, _, _ := mime.ParseMediaType(contentType)
		if isJSON(mediaType) && json.Valid([]byte(text)) {
			out.Body = json.RawMessage(text)
		} else {
			out.Body = text
		}
	}
	return out
}

func contentTypeOr(contentType string) string {
	if contentType == "" {
		return "no Content-Type"
	}
	return contentType
}

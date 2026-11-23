package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// request is the command the harness sends.
type request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Params  map[string]string `json:"params"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

var methods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// Headers the transport sets itself.
var reservedHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true,
}

func parseCommand(text string) (request, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	var call request
	if err := dec.Decode(&call); err != nil {
		return request{}, fmt.Errorf("command must be a JSON object with method, path, params, headers and body: %w", err)
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return request{}, errors.New("command must be one JSON object")
	}
	call.Method = strings.ToUpper(strings.TrimSpace(call.Method))
	if call.Method == "" {
		return request{}, errors.New("method is required")
	}
	if !methods[call.Method] {
		return request{}, fmt.Errorf("method %s is not supported", call.Method)
	}
	if !strings.HasPrefix(call.Path, "/") {
		return request{}, errors.New("path must start with /")
	}
	if strings.ContainsAny(call.Path, "?#") {
		return request{}, errors.New("path must not contain ? or #; put query values in params")
	}
	if string(call.Body) == "null" {
		call.Body = nil
	}
	return call, nil
}

// build turns the command into a request for this target. The URL is put
// together from parts, so nothing in path can change the host.
func (t target) build(ctx context.Context, call request) (*http.Request, error) {
	parsed, err := url.Parse(call.Path)
	if err != nil {
		return nil, fmt.Errorf("path is not valid: %w", err)
	}
	if parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" {
		return nil, errors.New("path must be a path on this host, like /v1/orders")
	}
	query := url.Values{}
	for name, value := range call.Params {
		query.Set(name, value)
	}
	target := &url.URL{
		Scheme:   t.scheme,
		Host:     t.host,
		Path:     parsed.Path,
		RawPath:  parsed.RawPath,
		RawQuery: query.Encode(),
	}

	header := http.Header{}
	for name, value := range call.Headers {
		key := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if !validHeaderName(key) {
			return nil, fmt.Errorf("header %q is not a valid header name", name)
		}
		if reservedHeaders[key] {
			return nil, fmt.Errorf("header %s is set automatically", key)
		}
		if t.header != "" && key == t.header {
			return nil, fmt.Errorf("header %s is set by the resource's authentication", key)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("header %s must not contain line breaks", key)
		}
		header.Set(key, value)
	}

	body, err := encodeBody(call.Body, header)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, call.Method, target.String(), reader)
	if err != nil {
		return nil, err
	}
	if req.URL.Host != t.host {
		return nil, errors.New("path must be a path on this host, like /v1/orders")
	}
	req.Header = header
	if t.header != "" {
		req.Header.Set(t.header, t.value)
	}
	return req, nil
}

// encodeBody reads body according to the Content-Type header. With a body and
// no Content-Type, the body is text and the header is set to text/plain.
func encodeBody(body json.RawMessage, header http.Header) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	contentType := header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
		header.Set("Content-Type", contentType)
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("Content-Type %q is not valid", contentType)
	}
	switch {
	case isJSON(mediaType):
		return body, nil
	case mediaType == "application/x-www-form-urlencoded":
		var fields map[string]string
		if err := json.Unmarshal(body, &fields); err != nil {
			return nil, errors.New("with Content-Type application/x-www-form-urlencoded, body must be an object of strings")
		}
		form := url.Values{}
		for name, value := range fields {
			form.Set(name, value)
		}
		return []byte(form.Encode()), nil
	default:
		var text string
		if err := json.Unmarshal(body, &text); err != nil {
			return nil, fmt.Errorf("with Content-Type %s, body must be a string; use application/json to send JSON", mediaType)
		}
		return []byte(text), nil
	}
}

func isJSON(mediaType string) bool {
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// validHeaderName reports whether name is an HTTP token.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r > 126 || r <= 32 || strings.ContainsRune(`"(),/:;<=>?@[\]{}`, r) {
			return false
		}
	}
	return true
}

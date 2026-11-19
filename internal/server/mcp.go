package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"gatekeep/internal/db"
	"gatekeep/internal/mcp"
)

const mcpBodyLimit = 1 << 20

// mcpSession is one legacy initialize handshake. Modern requests are stateless.
type mcpSession struct {
	protocolVersion string
	clientName      string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (r rpcRequest) notification() bool {
	return len(r.ID) == 0
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	ResultType        string        `json:"resultType,omitempty"`
	Content           []toolContent `json:"content"`
	StructuredContent any           `json:"structuredContent,omitempty"`
	IsError           bool          `json:"isError"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if !mcpOriginAllowed(origin) {
		writeRPCError(w, http.StatusForbidden, nil, mcp.CodeInvalidRequest, "origin is not allowed", nil)
		return
	}
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
	}

	switch r.Method {
	case http.MethodOptions:
		w.Header().Set("Access-Control-Allow-Methods", "POST, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, MCP-Protocol-Version, Mcp-Method, Mcp-Name, Mcp-Session-Id")
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		s.handleMCPPost(w, r)
	case http.MethodDelete:
		s.handleMCPDelete(w, r)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMCPDelete(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("Mcp-Session-Id")
	if id == "" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.sessionsMu.Lock()
	_, ok := s.sessions[id]
	delete(s.sessions, id)
	s.sessionsMu.Unlock()
	if !ok {
		writeRPCError(w, http.StatusNotFound, nil, mcp.CodeInvalidRequest, "session not found", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMCPPost(w http.ResponseWriter, r *http.Request) {
	if accept := r.Header.Get("Accept"); accept != "" && !mcpAccepts(accept) {
		writeRPCError(w, http.StatusNotAcceptable, nil, mcp.CodeInvalidRequest, "accept application/json and text/event-stream", nil)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mcpBodyLimit))
	if err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, mcp.CodeInvalidRequest, "request body is too large", nil)
		return
	}
	if len(body) > 0 && body[0] == '[' {
		writeRPCError(w, http.StatusBadRequest, nil, mcp.CodeInvalidRequest, "json-rpc batches are not supported", nil)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, mcp.CodeParseError, "parse error", nil)
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		writeRPCError(w, http.StatusBadRequest, req.ID, mcp.CodeInvalidRequest, "invalid request", nil)
		return
	}

	version := r.Header.Get("MCP-Protocol-Version")
	metaVersion := mcp.ProtocolVersion(req.Params)
	if version != "" && metaVersion != "" && version != metaVersion {
		writeRPCError(w, http.StatusBadRequest, req.ID, mcp.CodeHeaderMismatch, "MCP-Protocol-Version does not match _meta", nil)
		return
	}
	if version == "" {
		version = metaVersion
	}

	switch {
	case req.Method == "initialize" || strings.HasPrefix(req.Method, "notifications/"):
		s.handleLegacyMCP(w, r, req)
	case version == mcp.ModernVersion:
		s.handleModernMCP(w, r, req)
	case version == "" || mcp.LegacyVersions[version]:
		s.handleLegacyMCP(w, r, req)
	default:
		writeRPCError(w, http.StatusBadRequest, req.ID, mcp.CodeUnsupportedVersion, "unsupported protocol version", map[string]any{
			"supported": []string{mcp.ModernVersion},
			"requested": version,
		})
	}
}

func (s *Server) handleModernMCP(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	if req.notification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if message := modernHeaderMismatch(r, req); message != "" {
		writeRPCError(w, http.StatusBadRequest, req.ID, mcp.CodeHeaderMismatch, message, nil)
		return
	}
	switch req.Method {
	case "server/discover":
		writeRPCResult(w, http.StatusOK, req.ID, map[string]any{
			"resultType":        "complete",
			"supportedVersions": []string{mcp.ModernVersion},
			"capabilities":      map[string]any{"tools": map[string]any{"listChanged": false}},
			"instructions":      s.instructions(r.Context()),
			"_meta": map[string]any{
				"io.modelcontextprotocol/serverInfo": map[string]any{
					"name":    mcp.ServerName,
					"version": mcp.ServerVersion,
				},
			},
		})
	case "ping":
		writeRPCResult(w, http.StatusOK, req.ID, map[string]any{})
	case "tools/list":
		s.writeToolList(w, r, req.ID, true)
	case "tools/call":
		s.queueToolCall(w, r, req, mcp.HarnessName(mcp.ClientName(req.Params)), true)
	default:
		writeRPCError(w, http.StatusNotFound, req.ID, mcp.CodeMethodNotFound, "method not found", nil)
	}
}

func (s *Server) handleLegacyMCP(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	if strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if req.notification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		s.mcpInitialize(w, req)
	case "ping", "tools/list", "tools/call":
		// A session is optional. Codex and other harnesses keep working when they
		// do not send Mcp-Session-Id. An unknown id still means that session is gone.
		session, status := s.legacySession(r)
		if status == http.StatusNotFound {
			writeRPCError(w, status, req.ID, mcp.CodeInvalidRequest, "session not found", nil)
			return
		}
		switch req.Method {
		case "ping":
			writeRPCResult(w, http.StatusOK, req.ID, map[string]any{})
		case "tools/list":
			s.writeToolList(w, r, req.ID, false)
		default:
			s.queueToolCall(w, r, req, session.clientName, false)
		}
	default:
		writeRPCError(w, http.StatusOK, req.ID, mcp.CodeMethodNotFound, "method not found", nil)
	}
}

func (s *Server) mcpInitialize(w http.ResponseWriter, req rpcRequest) {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
		ClientInfo      struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
	}
	if len(req.Params) == 0 || string(req.Params) == "null" || json.Unmarshal(req.Params, &params) != nil {
		writeRPCError(w, http.StatusBadRequest, req.ID, mcp.CodeInvalidParams, "invalid initialize params", nil)
		return
	}
	version := params.ProtocolVersion
	if version == "" {
		version = mcp.PreferredLegacy
	}
	id, err := newSessionID()
	if err != nil {
		writeRPCError(w, http.StatusInternalServerError, req.ID, mcp.CodeInternal, "could not start a session", nil)
		return
	}
	s.sessionsMu.Lock()
	s.sessions[id] = mcpSession{
		protocolVersion: version,
		clientName:      mcp.HarnessName(params.ClientInfo.Name),
	}
	s.sessionsMu.Unlock()

	w.Header().Set("Mcp-Session-Id", id)
	writeRPCResult(w, http.StatusOK, req.ID, map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo": map[string]any{
			"name":    mcp.ServerName,
			"version": mcp.ServerVersion,
		},
		"instructions": s.instructions(context.Background()),
	})
}

func (s *Server) legacySession(r *http.Request) (mcpSession, int) {
	id := r.Header.Get("Mcp-Session-Id")
	if id == "" {
		return mcpSession{clientName: "unknown"}, 0
	}
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return mcpSession{}, http.StatusNotFound
	}
	return session, 0
}

func (s *Server) queueToolCall(w http.ResponseWriter, r *http.Request, req rpcRequest, harnessName string, modern bool) {
	call, err := mcp.DecodeCall(req.Params)
	var rejected *mcp.Reject
	if errors.As(err, &rejected) {
		s.writeToolResult(w, req.ID, rejected.Text, nil, true, modern)
		return
	}
	if err != nil {
		writeRPCError(w, http.StatusOK, req.ID, mcp.CodeInvalidParams, err.Error(), nil)
		return
	}
	if _, ok := s.plugins.Get(call.Name); !ok {
		writeRPCError(w, http.StatusOK, req.ID, mcp.CodeInvalidParams, "unknown tool "+call.Name, nil)
		return
	}
	if s.store == nil {
		writeRPCError(w, http.StatusServiceUnavailable, req.ID, mcp.CodeInternal, "database is unavailable", nil)
		return
	}

	resource, err := s.store.ResourceByName(r.Context(), call.Resource)
	if errors.Is(err, db.ErrNotFound) {
		s.writeToolResult(w, req.ID, "unknown resource "+call.Resource, nil, true, modern)
		return
	}
	if err != nil {
		writeRPCError(w, http.StatusInternalServerError, req.ID, mcp.CodeInternal, "could not look up the resource", nil)
		return
	}
	if resource.Type != call.Name {
		s.writeToolResult(w, req.ID, fmt.Sprintf("resource %s is %s, not %s", resource.Name, resource.Type, call.Name), nil, true, modern)
		return
	}

	harness, err := s.store.EnsureHarness(r.Context(), harnessName)
	if err != nil {
		writeRPCError(w, http.StatusInternalServerError, req.ID, mcp.CodeInternal, "could not record the harness", nil)
		return
	}
	stored, err := s.store.CreateToolCall(r.Context(), db.ToolCall{
		ResourceID:   &resource.ID,
		ResourceName: resource.Name,
		HarnessID:    &harness.ID,
		HarnessName:  harness.Name,
		Command:      call.Command,
		Reason:       call.Reason,
		Status:       db.ToolCallPending,
	})
	if err != nil {
		writeRPCError(w, http.StatusInternalServerError, req.ID, mcp.CodeInternal, "could not queue the request", nil)
		return
	}
	s.notifyPending(stored)
	s.waitForApproval(w, r, req.ID, stored.ID, modern)
}

func (s *Server) writeToolList(w http.ResponseWriter, r *http.Request, id json.RawMessage, modern bool) {
	tools, err := s.mcpTools(r.Context())
	if err != nil {
		writeRPCError(w, http.StatusInternalServerError, id, mcp.CodeInternal, "could not list tools", nil)
		return
	}
	result := map[string]any{"tools": tools}
	if modern {
		result["resultType"] = "complete"
	}
	writeRPCResult(w, http.StatusOK, id, result)
}

func (s *Server) mcpTools(ctx context.Context) ([]mcp.Tool, error) {
	names := map[string][]string{}
	if s.store != nil {
		resources, err := s.store.ListResources(ctx)
		if err != nil {
			return nil, err
		}
		for _, resource := range resources {
			names[resource.Type] = append(names[resource.Type], resource.Name)
		}
	}
	return mcp.Tools(s.plugins.List(), names), nil
}

func (s *Server) approvalURL(id int64) string {
	return s.url + "/approval/" + strconv.FormatInt(id, 10)
}

func (s *Server) writeToolResult(w http.ResponseWriter, id json.RawMessage, text string, structured any, isError, modern bool) {
	result := toolResult{
		Content:           []toolContent{{Type: "text", Text: text}},
		StructuredContent: structured,
		IsError:           isError,
	}
	if modern {
		result.ResultType = "complete"
	}
	writeRPCResult(w, http.StatusOK, id, result)
}

func modernHeaderMismatch(r *http.Request, req rpcRequest) string {
	// Codex sends the protocol version and omits Mcp-Method and Mcp-Name.
	// Reject those headers only when the client sent a value that disagrees
	// with the body. A missing header is not a mismatch.
	method := r.Header.Get("Mcp-Method")
	if method != "" && method != req.Method {
		return "Mcp-Method does not match the request body"
	}
	if req.Method != "tools/call" || r.Header.Get("Mcp-Name") == "" {
		return ""
	}
	headerName, err := decodeHeaderValue(r.Header.Get("Mcp-Name"))
	if err != nil {
		return "Mcp-Name is not valid"
	}
	bodyName, ok := mcp.ParamName(req.Params)
	if !ok || headerName != bodyName {
		return "Mcp-Name does not match the request body"
	}
	return ""
}

func decodeHeaderValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "=?base64?") && strings.HasSuffix(value, "?=") {
		raw := strings.TrimSuffix(strings.TrimPrefix(value, "=?base64?"), "?=")
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	}
	return value, nil
}

func mcpOriginAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	if origin == "null" {
		return false
	}
	lower := strings.ToLower(origin)
	return strings.HasPrefix(lower, "http://127.0.0.1:") ||
		strings.HasPrefix(lower, "http://localhost:") ||
		strings.HasPrefix(lower, "http://[::1]:") ||
		lower == "http://127.0.0.1" ||
		lower == "http://localhost" ||
		lower == "http://[::1]"
}

func mcpAccepts(header string) bool {
	for _, part := range strings.Split(header, ",") {
		media := strings.ToLower(strings.TrimSpace(strings.Split(part, ";")[0]))
		switch media {
		case "application/json", "text/event-stream", "*/*":
			return true
		}
	}
	return false
}

func newSessionID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func writeRPCResult(w http.ResponseWriter, status int, id json.RawMessage, result any) {
	writeRPC(w, status, id, result, nil)
}

func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string, data any) {
	writeRPC(w, status, id, nil, &rpcError{Code: code, Message: message, Data: data})
}

func writeRPC(w http.ResponseWriter, status int, id json.RawMessage, result any, rpcErr *rpcError) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	payload := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id,omitempty"`
		Result  any             `json:"result,omitempty"`
		Error   *rpcError       `json:"error,omitempty"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
		Error:   rpcErr,
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gatekeep/internal/db"
	"gatekeep/internal/mcp"
)

const (
	mcpInstructionsKey = "mcp_instructions"
	listenPortKey      = "listen_port"
	defaultListenPort  = 8765
)

// ListenPort is the port saved in settings. A missing or invalid value is 8765.
func ListenPort(store *db.Store) int {
	if store == nil {
		return defaultListenPort
	}
	setting, err := store.GetSetting(context.Background(), listenPortKey)
	if err != nil {
		return defaultListenPort
	}
	port, err := strconv.Atoi(strings.TrimSpace(setting.Value))
	if err != nil || port < 1 || port > 65535 {
		return defaultListenPort
	}
	return port
}

func (s *Server) instructions(ctx context.Context) string {
	fallback := mcp.Instructions(s.url, s.approvalTimeout(ctx), s.globalExecutionTimeout(ctx))
	if s.store == nil {
		return fallback
	}
	setting, err := s.store.GetSetting(ctx, mcpInstructionsKey)
	if err != nil {
		return fallback
	}
	return setting.Value
}

func (s *Server) handleGetInstructions(w http.ResponseWriter, r *http.Request) {
	text := s.instructions(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"text":    text,
		"default": s.defaultInstructions(r.Context()),
	})
}

func (s *Server) handlePutInstructions(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if _, err := s.store.PutSetting(r.Context(), mcpInstructionsKey, body.Text); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the instructions"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"text":    body.Text,
		"default": s.defaultInstructions(r.Context()),
	})
}

func (s *Server) handleResetInstructions(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	if err := s.store.DeleteSetting(r.Context(), mcpInstructionsKey); err != nil && !errors.Is(err, db.ErrNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not reset the instructions"})
		return
	}
	text := s.defaultInstructions(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"text":    text,
		"default": text,
	})
}

func (s *Server) defaultInstructions(ctx context.Context) string {
	return mcp.Instructions(s.url, s.approvalTimeout(ctx), s.globalExecutionTimeout(ctx))
}

func (s *Server) handleGetPort(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]int{
		"port":    ListenPort(s.store),
		"current": s.currentPort(),
	})
}

func (s *Server) handlePutPort(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	var body struct {
		Port int `json:"port"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Port < 1 || body.Port > 65535 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "port must be between 1 and 65535"})
		return
	}
	if _, err := s.store.PutSetting(r.Context(), listenPortKey, strconv.Itoa(body.Port)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the port"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"port":    body.Port,
		"current": s.currentPort(),
	})
}

func (s *Server) currentPort() int {
	parsed, err := url.Parse(s.url)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		return 0
	}
	return port
}

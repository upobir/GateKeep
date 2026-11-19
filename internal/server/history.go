package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"gatekeep/internal/db"
)

const recentWindowKey = "recent_window"

func (s *Server) recentWindow(ctx context.Context) db.RecentWindow {
	if s.store == nil {
		return db.DefaultRecentWindow()
	}
	setting, err := s.store.GetSetting(ctx, recentWindowKey)
	if err != nil {
		return db.DefaultRecentWindow()
	}
	window, err := db.ParseRecentWindow(setting.Value)
	if err != nil {
		return db.DefaultRecentWindow()
	}
	return window
}

func (s *Server) handleGetRecentWindow(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, recentWindowJSON(s.recentWindow(r.Context())))
}

func (s *Server) handlePutRecentWindow(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	var body struct {
		Count int    `json:"count"`
		Unit  string `json:"unit"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	window := db.RecentWindow{Count: body.Count, Unit: body.Unit}
	if !window.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "recent tool calls must be a positive number of seconds, minutes, hours, days, or tool calls"})
		return
	}
	if _, err := s.store.PutSetting(r.Context(), recentWindowKey, window.String()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the recent window"})
		return
	}
	writeJSON(w, http.StatusOK, recentWindowJSON(window))
}

func recentWindowJSON(window db.RecentWindow) map[string]any {
	return map[string]any{"count": window.Count, "unit": window.Unit}
}

func (s *Server) handleListHistory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	calls, err := s.store.ListHistoryToolCalls(r.Context(), s.recentWindow(r.Context()), time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list history"})
		return
	}
	views := make([]approvalView, 0, len(calls))
	for _, call := range calls {
		views = append(views, s.presentApproval(r.Context(), call))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	deleted, err := s.store.ClearHistoryToolCalls(r.Context(), s.recentWindow(r.Context()), time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not clear history"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
}

func (s *Server) handleDeleteApproval(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	call, err := s.store.GetToolCall(r.Context(), id)
	if errors.Is(err, db.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete the tool call"})
		return
	}
	if call.Status == db.ToolCallPending || call.Status == db.ToolCallExecuting || call.Status == db.ToolCallReview {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this request is still open"})
		return
	}
	if err := s.store.DeleteToolCall(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete the tool call"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": id})
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gatekeep/internal/db"
)

const (
	executionTimeoutKey     = "execution_timeout"
	defaultExecutionTimeout = 30 * time.Second
)

var (
	errNotRunning       = errors.New("this command is not running")
	errExecutionTimeout = errors.New("Execution timed out.")
	errExecutionStopped = errors.New("GateKeep stopped during execution.")
	errExecutionRange   = errors.New("execution timeout must be between 1 second and 24 hours")
)

func (s *Server) globalExecutionTimeout(ctx context.Context) time.Duration {
	if s.store == nil {
		return defaultExecutionTimeout
	}
	setting, err := s.store.GetSetting(ctx, executionTimeoutKey)
	if err != nil {
		return defaultExecutionTimeout
	}
	wait, err := parseApprovalTimeout(setting.Value)
	if err != nil || !validApprovalTimeout(wait) {
		return defaultExecutionTimeout
	}
	return wait
}

func (s *Server) executionLimit(ctx context.Context, resource db.Resource) time.Duration {
	if resource.ExecutionTimeoutMs != nil && *resource.ExecutionTimeoutMs > 0 {
		return time.Duration(*resource.ExecutionTimeoutMs) * time.Millisecond
	}
	return s.globalExecutionTimeout(ctx)
}

func (s *Server) handleGetExecutionTimeout(w http.ResponseWriter, r *http.Request) {
	seconds := int(s.globalExecutionTimeout(r.Context()) / time.Second)
	writeJSON(w, http.StatusOK, map[string]int{"seconds": seconds})
}

func (s *Server) handlePutExecutionTimeout(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	var body struct {
		Seconds int `json:"seconds"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	wait := time.Duration(body.Seconds) * time.Second
	if !validApprovalTimeout(wait) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errExecutionRange.Error()})
		return
	}
	if _, err := s.store.PutSetting(r.Context(), executionTimeoutKey, strconv.Itoa(body.Seconds)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save execution timeout"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"seconds": body.Seconds})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	run, err := s.executionRun(r.Context(), id)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	// Written before byUser so the execution goroutine sees it after Load.
	run.reason = strings.TrimSpace(body.Reason)
	run.byUser.Store(true)
	run.cancel()
	select {
	case <-run.done:
	case <-r.Context().Done():
		return
	}
	call, err := s.store.GetToolCall(r.Context(), id)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.presentApproval(r.Context(), call))
}

func (s *Server) executionRun(ctx context.Context, id int64) (*executionRun, error) {
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.execMu.Lock()
		run := s.execs[id]
		s.execMu.Unlock()
		if run != nil {
			return run, nil
		}
		call, err := s.store.GetToolCall(ctx, id)
		if err != nil {
			return nil, err
		}
		if call.Status != db.ToolCallExecuting {
			return nil, errNotRunning
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errNotRunning
		case <-ticker.C:
		}
	}
}

// executeApproved runs the command that was just approved.
// A user cancel or a timeout finishes the call. The caller delivers that result.
func (s *Server) executeApproved(ctx context.Context, call db.ToolCall) (db.ToolCall, error) {
	limit := defaultExecutionTimeout
	if call.ResourceID != nil && s.store != nil {
		resource, err := s.store.GetResource(ctx, *call.ResourceID)
		if err == nil {
			limit = s.executionLimit(ctx, resource)
		}
	}
	runCtx, cancel := context.WithTimeout(s.baseCtx, limit)
	run := &executionRun{cancel: cancel, done: make(chan struct{})}
	s.execMu.Lock()
	s.execs[call.ID] = run
	s.execMu.Unlock()
	defer func() {
		cancel()
		s.execMu.Lock()
		delete(s.execs, call.ID)
		s.execMu.Unlock()
		close(run.done)
	}()
	s.hub.Publish("approvals", `{"id":`+strconv.FormatInt(call.ID, 10)+`}`)

	if run.byUser.Load() {
		return s.finishCancelled(call, run.reason)
	}
	value, execErr := s.runApproved(runCtx, call)
	if run.byUser.Load() {
		return s.finishCancelled(call, run.reason)
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		execErr = errExecutionTimeout
		value = nil
	} else if errors.Is(runCtx.Err(), context.Canceled) {
		execErr = errExecutionStopped
		value = nil
	}
	return s.finishExecution(call, value, execErr)
}

func (s *Server) finishCancelled(call db.ToolCall, reason string) (db.ToolCall, error) {
	now := time.Now().UTC()
	decision := db.DecisionApprove
	status := db.ExecutionCancelled
	call.Status = db.ToolCallApproved
	call.Decision = &decision
	call.DecisionReason = nil
	if reason != "" {
		call.DecisionReason = &reason
	}
	call.ExecutionStatus = &status
	call.Result = nil
	call.FinishedAt = &now
	if err := s.store.UpdateToolCall(context.Background(), call); err != nil {
		return call, err
	}
	return call, nil
}

func (s *Server) finishExecution(call db.ToolCall, value any, execErr error) (db.ToolCall, error) {
	now := time.Now().UTC()
	call.Status = db.ToolCallApproved
	call.FinishedAt = &now
	status := db.ExecutionOK
	var result string
	switch {
	case errors.Is(execErr, errExecutionTimeout):
		status = db.ExecutionTimeout
		result = execErr.Error()
	case execErr != nil:
		status = db.ExecutionError
		result = execErr.Error()
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			status = db.ExecutionError
			result = err.Error()
		} else {
			result = string(raw)
		}
	}
	call.ExecutionStatus = &status
	call.Result = &result
	if err := s.store.UpdateToolCall(context.Background(), call); err != nil {
		return call, err
	}
	return call, nil
}

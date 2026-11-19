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
	s.execMu.Lock()
	run := s.execs[id]
	s.execMu.Unlock()
	if run != nil {
		return run, nil
	}
	if _, err := s.store.GetToolCall(ctx, id); err != nil {
		return nil, err
	}
	return nil, errNotRunning
}

// armRun registers the run before the call is marked executing.
// A disconnect can then wait on the run instead of polling for it to appear.
func (s *Server) armRun(ctx context.Context, call db.ToolCall) (*executionRun, error) {
	limit := defaultExecutionTimeout
	if call.ResourceID != nil && s.store != nil {
		resource, err := s.store.GetResource(ctx, *call.ResourceID)
		if err == nil {
			limit = s.executionLimit(ctx, resource)
		}
	}
	runCtx, cancel := context.WithTimeout(s.baseCtx, limit)
	run := &executionRun{ctx: runCtx, cancel: cancel, done: make(chan struct{})}
	s.execMu.Lock()
	if _, exists := s.execs[call.ID]; exists {
		s.execMu.Unlock()
		cancel()
		return nil, errNotRunning
	}
	s.execs[call.ID] = run
	s.execMu.Unlock()
	return run, nil
}

// disarmRun removes a run that never started. It ignores a run that was replaced.
func (s *Server) disarmRun(id int64, run *executionRun) {
	s.execMu.Lock()
	current := s.execs[id]
	if current == run {
		delete(s.execs, id)
	}
	s.execMu.Unlock()
	if current != run {
		return
	}
	run.cancel()
	close(run.done)
}

// executeApproved runs a command that the user allowed to start.
// The run must already be armed. hold is See Results: a finished command waits
// to be sent or rejected. A cancel or a timeout still finishes the call, with
// no decision when held. The caller delivers a finished call.
func (s *Server) executeApproved(call db.ToolCall, hold bool, run *executionRun) (db.ToolCall, error) {
	defer func() {
		run.cancel()
		close(run.done)
		s.execMu.Lock()
		if s.execs[call.ID] == run {
			delete(s.execs, call.ID)
		}
		s.execMu.Unlock()
	}()
	s.hub.Publish("approvals", `{"id":`+strconv.FormatInt(call.ID, 10)+`}`)

	if run.byUser.Load() {
		return s.finishStop(call, hold, run.reason, false, false)
	}
	value, execErr := s.runApproved(run.ctx, call)
	if run.byUser.Load() {
		return s.finishStop(call, hold, run.reason, false, false)
	}
	timedOut := errors.Is(run.ctx.Err(), context.DeadlineExceeded)
	stopped := errors.Is(run.ctx.Err(), context.Canceled)
	if timedOut || stopped {
		value = nil
		if timedOut {
			execErr = errExecutionTimeout
		} else {
			execErr = errExecutionStopped
		}
	}
	if hold && (run.byUser.Load() || timedOut || stopped) {
		return s.finishStop(call, true, run.reason, timedOut, stopped)
	}
	if hold {
		return s.holdResult(call, value, execErr)
	}
	return s.finishExecution(call, value, execErr)
}

// finishStop records a cancel, a timeout, or a shutdown.
// A held run keeps the decision empty for cancel and timeout.
func (s *Server) finishStop(call db.ToolCall, hold bool, reason string, timedOut, stopped bool) (db.ToolCall, error) {
	if !hold {
		return s.finishCancelled(call, reason)
	}
	now := time.Now().UTC()
	call.Decision = nil
	call.DecisionReason = nil
	call.Result = nil
	call.FinishedAt = &now
	status := db.ToolCallCancelled
	execution := db.ExecutionCancelled
	switch {
	case timedOut:
		status = db.ToolCallTimedOut
		execution = db.ExecutionTimeout
	case stopped:
		status = db.ToolCallExpired
		execution = db.ExecutionError
		decision := db.DecisionExpire
		call.Decision = &decision
		reason = errExecutionStopped.Error()
		call.Result = &reason
	}
	if reason != "" && !stopped {
		call.DecisionReason = &reason
	}
	call.Status = status
	call.ExecutionStatus = &execution
	if err := s.store.UpdateToolCall(context.Background(), call); err != nil {
		return call, err
	}
	return call, nil
}

// holdResult keeps a finished command on the queue until the user sends or rejects it.
func (s *Server) holdResult(call db.ToolCall, value any, execErr error) (db.ToolCall, error) {
	call.Status = db.ToolCallReview
	call.Decision = nil
	call.DecisionReason = nil
	call.DecidedAt = nil
	call.FinishedAt = nil
	status := db.ExecutionOK
	var result string
	switch {
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

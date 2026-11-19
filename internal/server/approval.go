package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gatekeep/internal/db"
	"gatekeep/internal/mcp"
)

const (
	approvalTimeoutKey     = "approval_timeout"
	defaultApprovalTimeout = 5 * time.Minute
	minApprovalTimeout     = time.Second
	maxApprovalTimeout     = 24 * time.Hour

	restartExpiryReason    = "GateKeep restarted before the user decided."
	restartExecutionReason = "GateKeep restarted during execution."
	stoppedExpiryReason    = "GateKeep stopped before the user decided."
	disconnectReason       = "The harness disconnected before the user decided."
	timeoutReason          = "Approval timed out. The user did not approve this command."
)

var errReasonRequired = errors.New("a rejection reason is required")

type approvalView struct {
	ID                      int64  `json:"id"`
	ResourceName            string `json:"resourceName"`
	HarnessName             string `json:"harnessName"`
	Command                 string `json:"command"`
	Reason                  string `json:"reason"`
	Status                  string `json:"status"`
	Decision                string `json:"decision,omitempty"`
	DecisionReason          string `json:"decisionReason,omitempty"`
	ExecutionStatus         string `json:"executionStatus,omitempty"`
	ExecutionTimeoutSeconds int    `json:"executionTimeoutSeconds,omitempty"`
	Result                  string `json:"result,omitempty"`
	CreatedAt               string `json:"createdAt"`
	DecidedAt               string `json:"decidedAt,omitempty"`
}

func approvalJSON(call db.ToolCall) approvalView {
	view := approvalView{
		ID:           call.ID,
		ResourceName: call.ResourceName,
		HarnessName:  call.HarnessName,
		Command:      call.Command,
		Reason:       call.Reason,
		Status:       call.Status,
		CreatedAt:    call.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if call.Decision != nil {
		view.Decision = *call.Decision
	}
	if call.DecisionReason != nil {
		view.DecisionReason = *call.DecisionReason
	}
	if call.DecidedAt != nil {
		view.DecidedAt = call.DecidedAt.UTC().Format(time.RFC3339Nano)
	}
	if call.ExecutionStatus != nil {
		view.ExecutionStatus = *call.ExecutionStatus
	}
	if call.Result != nil {
		view.Result = *call.Result
	}
	return view
}

func (s *Server) presentApproval(ctx context.Context, call db.ToolCall) approvalView {
	view := approvalJSON(call)
	if call.Status == db.ToolCallExecuting {
		view.ExecutionTimeoutSeconds = int(s.callExecutionLimit(ctx, call) / time.Second)
	}
	return view
}

func (s *Server) callExecutionLimit(ctx context.Context, call db.ToolCall) time.Duration {
	if call.ResourceID != nil && s.store != nil {
		resource, err := s.store.GetResource(ctx, *call.ResourceID)
		if err == nil {
			return s.executionLimit(ctx, resource)
		}
	}
	return s.globalExecutionTimeout(ctx)
}

func (s *Server) approvalTimeout(ctx context.Context) time.Duration {
	if s.store == nil {
		return defaultApprovalTimeout
	}
	setting, err := s.store.GetSetting(ctx, approvalTimeoutKey)
	if err != nil {
		return defaultApprovalTimeout
	}
	wait, err := parseApprovalTimeout(setting.Value)
	if err != nil || !validApprovalTimeout(wait) {
		return defaultApprovalTimeout
	}
	return wait
}

func parseApprovalTimeout(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if wait, err := time.ParseDuration(value); err == nil {
		return wait, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid approval timeout")
	}
	return time.Duration(seconds) * time.Second, nil
}

func validApprovalTimeout(wait time.Duration) bool {
	return wait >= minApprovalTimeout && wait <= maxApprovalTimeout
}

func (s *Server) handleGetApprovalTimeout(w http.ResponseWriter, r *http.Request) {
	seconds := int(s.approvalTimeout(r.Context()) / time.Second)
	writeJSON(w, http.StatusOK, map[string]int{"seconds": seconds})
}

func (s *Server) handlePutApprovalTimeout(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "approval timeout must be between 1 second and 24 hours"})
		return
	}
	if _, err := s.store.PutSetting(r.Context(), approvalTimeoutKey, strconv.Itoa(body.Seconds)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save approval timeout"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"seconds": body.Seconds})
}

func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	calls, err := s.store.ListQueueToolCalls(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list approvals"})
		return
	}
	views := make([]approvalView, 0, len(calls))
	for _, call := range calls {
		views = append(views, s.presentApproval(r.Context(), call))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleGetApproval(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load the approval"})
		return
	}
	writeJSON(w, http.StatusOK, s.presentApproval(r.Context(), call))
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	s.writeDecision(w, r, db.ToolCallExecuting, db.DecisionApprove, "")
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	s.writeDecision(w, r, db.ToolCallRejected, db.DecisionReject, body.Reason)
}

func (s *Server) writeDecision(w http.ResponseWriter, r *http.Request, status, decision, reason string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	call, err := s.decideApproval(r.Context(), id, status, decision, reason)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.presentApproval(r.Context(), call))
}

func writeApprovalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errReasonRequired):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, db.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	case errors.Is(err, db.ErrNotPending):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this request is no longer pending"})
	case errors.Is(err, errNotRunning):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update the approval"})
	}
}

func (s *Server) decideApproval(ctx context.Context, id int64, status, decision, reason string) (db.ToolCall, error) {
	if s.store == nil {
		return db.ToolCall{}, errors.New("database is unavailable")
	}
	if decision == db.DecisionReject && strings.TrimSpace(reason) == "" {
		return db.ToolCall{}, errReasonRequired
	}
	run := decision == db.DecisionApprove
	storedStatus := status
	if run {
		storedStatus = db.ToolCallExecuting
	}
	call, err := s.store.ResolveToolCall(ctx, id, storedStatus, decision, reason, !run)
	if err != nil {
		return db.ToolCall{}, err
	}
	if run {
		call, err = s.executeApproved(ctx, call)
		if err != nil {
			return db.ToolCall{}, err
		}
	}
	s.deliverApproval(call)
	return call, nil
}

func (s *Server) runApproved(ctx context.Context, call db.ToolCall) (any, error) {
	if s.vault == nil || call.ResourceID == nil {
		return nil, errors.New("the resource is no longer available")
	}
	resource, err := s.store.GetResource(ctx, *call.ResourceID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, errors.New("the resource is no longer available")
	}
	if err != nil {
		return nil, err
	}
	item, ok := s.plugins.Get(resource.Type)
	if !ok {
		return nil, fmt.Errorf("unknown resource type %s", resource.Type)
	}
	config := map[string]string{}
	if resource.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(resource.ConfigJSON), &config); err != nil {
			return nil, err
		}
	}
	secrets := map[string]string{}
	if len(resource.SecretsCiphertext) > 0 {
		plain, err := s.vault.Open(resource.SecretsCiphertext)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(plain, &secrets); err != nil {
			return nil, err
		}
	}
	return item.Execute(ctx, call.Command, config, secrets)
}

func (s *Server) registerApproval(id int64) chan db.ToolCall {
	ch := make(chan db.ToolCall, 1)
	s.approvalsMu.Lock()
	s.approvals[id] = ch
	s.approvalsMu.Unlock()
	return ch
}

func (s *Server) unregisterApproval(id int64) {
	s.approvalsMu.Lock()
	delete(s.approvals, id)
	s.approvalsMu.Unlock()
}

func (s *Server) deliverApproval(call db.ToolCall) {
	s.approvalsMu.Lock()
	ch := s.approvals[call.ID]
	s.approvalsMu.Unlock()
	if ch != nil {
		select {
		case ch <- call:
		default:
		}
	}
	s.hub.Publish("approvals", fmt.Sprintf(`{"id":%d}`, call.ID))
}

func (s *Server) waitForApproval(w http.ResponseWriter, r *http.Request, rpcID json.RawMessage, id int64, modern bool) {
	ch := s.registerApproval(id)
	defer s.unregisterApproval(id)
	s.hub.Publish("approvals", fmt.Sprintf(`{"id":%d}`, id))

	if fresh, err := s.store.GetToolCall(r.Context(), id); err == nil && fresh.Status != db.ToolCallPending {
		s.writeToolDecision(w, rpcID, fresh, modern)
		return
	}

	select {
	case call := <-ch:
		s.writeToolDecision(w, rpcID, call, modern)
	case <-r.Context().Done():
		select {
		case call := <-ch:
			s.writeToolDecision(w, rpcID, call, modern)
		default:
			reason := disconnectReason
			if s.baseCtx.Err() != nil {
				reason = stoppedExpiryReason
			}
			call, err := s.store.ResolveToolCall(context.Background(), id, db.ToolCallExpired, db.DecisionExpire, reason, true)
			if err != nil {
				return
			}
			s.hub.Publish("approvals", fmt.Sprintf(`{"id":%d}`, id))
			s.writeToolDecision(w, rpcID, call, modern)
		}
	}
}

func (s *Server) writeToolDecision(w http.ResponseWriter, rpcID json.RawMessage, call db.ToolCall, modern bool) {
	outcome := decisionOutcome(call)
	s.writeToolResult(w, rpcID, outcome.JSON(), outcome, !outcome.OK, modern)
}

func decisionOutcome(call db.ToolCall) mcp.Outcome {
	switch call.Status {
	case db.ToolCallApproved:
		if call.ExecutionStatus != nil && *call.ExecutionStatus == db.ExecutionTimeout {
			return mcp.Failed("tool_call_timed_out", "Execution timed out.")
		}
		if call.ExecutionStatus != nil && *call.ExecutionStatus == db.ExecutionOK && call.Result != nil {
			var value any
			if err := json.Unmarshal([]byte(*call.Result), &value); err == nil {
				return mcp.Ran(value)
			}
		}
		message := "the command failed"
		if call.Result != nil && *call.Result != "" {
			message = *call.Result
		}
		return mcp.Failed("tool_call_error", message)
	case db.ToolCallCancelled:
		reason := ""
		if call.DecisionReason != nil {
			reason = *call.DecisionReason
		}
		return mcp.Outcome{OK: false, Kind: "user_cancelled_mid_tool_call", Reason: reason}
	case db.ToolCallRejected:
		reason := ""
		if call.DecisionReason != nil {
			reason = *call.DecisionReason
		}
		return mcp.Outcome{OK: false, Kind: "user_rejected", Reason: reason}
	default:
		reason := "GateKeep ended the request before the user decided."
		if call.DecisionReason != nil && *call.DecisionReason != "" {
			reason = *call.DecisionReason
		}
		return mcp.Failed("approval_expired", reason)
	}
}

func (s *Server) watchApprovals(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.expireDueApprovals()
		}
	}
}

func (s *Server) expireDueApprovals() {
	if s.store == nil {
		return
	}
	ctx := context.Background()
	pending, err := s.store.ListPendingToolCalls(ctx)
	if err != nil {
		return
	}
	deadline := time.Now().Add(-s.approvalTimeout(ctx))
	for _, call := range pending {
		if call.CreatedAt.After(deadline) {
			continue
		}
		_, _ = s.decideApproval(ctx, call.ID, db.ToolCallExpired, db.DecisionExpire, timeoutReason)
	}
}

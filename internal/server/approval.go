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
	restartReviewReason    = "GateKeep restarted before the user sent the result."
	stoppedExpiryReason    = "GateKeep stopped before the user decided."
	disconnectReason       = "The harness disconnected before the user decided."
	timeoutReason          = "Approval timed out. The user did not approve this command."
)

var errReasonRequired = errors.New("a rejection reason is required")

type approvalView struct {
	ID           int64  `json:"id"`
	ResourceName string `json:"resourceName"`
	ResourceType string `json:"resourceType,omitempty"`
	// ResourceConfig is the resource's non-secret settings, for showing the command.
	ResourceConfig          map[string]string `json:"resourceConfig,omitempty"`
	HarnessName             string            `json:"harnessName"`
	Command                 string            `json:"command"`
	Reason                  string            `json:"reason"`
	Status                  string            `json:"status"`
	Decision                string            `json:"decision,omitempty"`
	DecisionReason          string            `json:"decisionReason,omitempty"`
	ExecutionStatus         string            `json:"executionStatus,omitempty"`
	ExecutionTimeoutSeconds int               `json:"executionTimeoutSeconds,omitempty"`
	Result                  string            `json:"result,omitempty"`
	CreatedAt               string            `json:"createdAt"`
	DecidedAt               string            `json:"decidedAt,omitempty"`
	FinishedAt              string            `json:"finishedAt,omitempty"`
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
	if call.FinishedAt != nil {
		view.FinishedAt = call.FinishedAt.UTC().Format(time.RFC3339Nano)
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
	if call.ResourceID != nil && s.store != nil {
		resource, err := s.store.GetResource(ctx, *call.ResourceID)
		if err == nil {
			view.ResourceType = resource.Type
			config := map[string]string{}
			if json.Unmarshal([]byte(resource.ConfigJSON), &config) == nil {
				view.ResourceConfig = config
			}
		}
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
	calls, err := s.store.ListApprovalToolCalls(r.Context(), s.recentWindow(r.Context()), time.Now())
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

func (s *Server) handleSeeResults(w http.ResponseWriter, r *http.Request) {
	id, err := approvalID(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	current, err := s.store.GetToolCall(r.Context(), id)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	run, err := s.armRun(r.Context(), current)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	call, err := s.store.BeginExecution(r.Context(), id)
	if err != nil {
		s.disarmRun(id, run)
		writeApprovalError(w, err)
		return
	}
	call, err = s.executeApproved(call, true, run)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	if call.Status == db.ToolCallReview {
		s.hub.Publish("approvals", fmt.Sprintf(`{"id":%d}`, call.ID))
	} else {
		s.deliverApproval(call)
	}
	writeJSON(w, http.StatusOK, s.presentApproval(r.Context(), call))
}

func (s *Server) handleSendResults(w http.ResponseWriter, r *http.Request) {
	id, err := approvalID(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	call, err := s.store.FinishReview(r.Context(), id, db.DecisionApprove, "")
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	s.deliverApproval(call)
	writeJSON(w, http.StatusOK, s.presentApproval(r.Context(), call))
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		writeApprovalError(w, errReasonRequired)
		return
	}
	id, err := approvalID(r)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database is unavailable"})
		return
	}
	current, err := s.store.GetToolCall(r.Context(), id)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	if current.Status == db.ToolCallReview {
		call, err := s.store.FinishReview(r.Context(), id, db.DecisionReject, body.Reason)
		if err != nil {
			writeApprovalError(w, err)
			return
		}
		s.deliverApproval(call)
		writeJSON(w, http.StatusOK, s.presentApproval(r.Context(), call))
		return
	}
	s.writeDecision(w, r, db.ToolCallRejected, db.DecisionReject, body.Reason)
}

func approvalID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("not found")
	}
	return id, nil
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
	execute := decision == db.DecisionApprove
	storedStatus := status
	var armed *executionRun
	if execute {
		storedStatus = db.ToolCallExecuting
		current, err := s.store.GetToolCall(ctx, id)
		if err != nil {
			return db.ToolCall{}, err
		}
		armed, err = s.armRun(ctx, current)
		if err != nil {
			return db.ToolCall{}, err
		}
	}
	call, err := s.store.ResolveToolCall(ctx, id, storedStatus, decision, reason, !execute)
	if err != nil {
		if armed != nil {
			s.disarmRun(id, armed)
		}
		return db.ToolCall{}, err
	}
	if execute {
		call, err = s.executeApproved(call, false, armed)
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
	s.secretsMu.Lock()
	resource, err := s.store.GetResource(ctx, *call.ResourceID)
	if err != nil {
		s.secretsMu.Unlock()
		if errors.Is(err, db.ErrNotFound) {
			return nil, errors.New("the resource is no longer available")
		}
		return nil, err
	}
	item, ok := s.plugins.Get(resource.Type)
	if !ok {
		s.secretsMu.Unlock()
		return nil, fmt.Errorf("unknown resource type %s", resource.Type)
	}
	config := map[string]string{}
	if resource.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(resource.ConfigJSON), &config); err != nil {
			s.secretsMu.Unlock()
			return nil, err
		}
	}
	secrets := map[string]string{}
	if len(resource.SecretsCiphertext) > 0 {
		plain, err := s.vault.Open(resource.SecretsCiphertext)
		if err != nil {
			s.secretsMu.Unlock()
			return nil, err
		}
		if err := json.Unmarshal(plain, &secrets); err != nil {
			s.secretsMu.Unlock()
			return nil, err
		}
	}
	cache := s.caches.Handle(resource.ID, cacheIdle(resource))
	command := call.Command
	s.secretsMu.Unlock()
	return item.Execute(ctx, command, config, secrets, cache)
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

func (s *Server) notifyPending(call db.ToolCall) {
	if s.notifier == nil {
		return
	}
	body := call.HarnessName + " wants to run a command on " + call.ResourceName
	if reason := strings.TrimSpace(call.Reason); reason != "" {
		body += "\n" + reason
	}
	s.notifier("Approval needed", body, s.url)
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
			s.abandonApproval(id, reason)
		}
	}
}

// abandonApproval closes a call whose harness is gone.
// A running command is allowed to finish. A withheld result is expired.
// The run is registered before the call is marked executing, so this waits on that run.
func (s *Server) abandonApproval(id int64, reason string) {
	ctx := context.Background()
	missedRun := false
	for {
		call, err := s.store.GetToolCall(ctx, id)
		if err != nil {
			return
		}
		switch call.Status {
		case db.ToolCallPending:
			missedRun = false
			if _, err := s.store.ResolveToolCall(ctx, id, db.ToolCallExpired, db.DecisionExpire, reason, true); err != nil {
				if errors.Is(err, db.ErrNotPending) {
					continue
				}
				return
			}
			s.hub.Publish("approvals", fmt.Sprintf(`{"id":%d}`, id))
			return
		case db.ToolCallReview:
			missedRun = false
			if _, err := s.store.ExpireReviewToolCall(ctx, id, reason); err != nil {
				if errors.Is(err, db.ErrNotPending) {
					continue
				}
				return
			}
			s.hub.Publish("approvals", fmt.Sprintf(`{"id":%d}`, id))
			return
		case db.ToolCallExecuting:
			if s.waitRun(id) {
				missedRun = false
				continue
			}
			// The run finished between the read above and this wait. Look once more.
			if missedRun {
				return
			}
			missedRun = true
		default:
			return
		}
	}
}

func (s *Server) waitRun(id int64) bool {
	s.execMu.Lock()
	run := s.execs[id]
	s.execMu.Unlock()
	if run == nil {
		return false
	}
	<-run.done
	return true
}

func (s *Server) writeToolDecision(w http.ResponseWriter, rpcID json.RawMessage, call db.ToolCall, modern bool) {
	outcome := decisionOutcome(call)
	s.writeToolResult(w, rpcID, outcome.JSON(), outcome, !outcome.OK, modern)
}

func decisionOutcome(call db.ToolCall) mcp.Outcome {
	if call.ExecutionStatus != nil && *call.ExecutionStatus == db.ExecutionCancelled {
		reason := ""
		if call.DecisionReason != nil {
			reason = *call.DecisionReason
		}
		return mcp.Outcome{OK: false, Kind: "user_cancelled_mid_tool_call", Reason: reason}
	}
	if call.ExecutionStatus != nil && *call.ExecutionStatus == db.ExecutionTimeout {
		return mcp.Failed("tool_call_timed_out", "Execution timed out.")
	}
	switch call.Status {
	case db.ToolCallApproved:
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

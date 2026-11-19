package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ToolCallPending   = "pending"
	ToolCallExecuting = "executing"
	ToolCallReview    = "review"
	ToolCallApproved  = "approved"
	ToolCallRejected  = "rejected"
	ToolCallCancelled = "cancelled"
	ToolCallTimedOut  = "timeout"
	ToolCallExpired   = "expired"

	DecisionApprove = "approve"
	DecisionReject  = "reject"
	DecisionCancel  = "cancel"
	DecisionExpire  = "expire"

	ExecutionOK        = "ok"
	ExecutionError     = "error"
	ExecutionTimeout   = "timeout"
	ExecutionCancelled = "cancelled"
)

// ErrNotPending means the tool call was already approved, rejected, or expired.
var ErrNotPending = errors.New("tool call is no longer pending")

type ToolCall struct {
	ID              int64
	ResourceID      *int64
	ResourceName    string
	HarnessID       *int64
	HarnessName     string
	Command         string
	Reason          string
	Status          string
	Decision        *string
	DecisionReason  *string
	ExecutionStatus *string
	Result          *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DecidedAt       *time.Time
	FinishedAt      *time.Time
}

func (s *Store) CreateToolCall(ctx context.Context, call ToolCall) (ToolCall, error) {
	if call.ResourceName == "" || call.HarnessName == "" || call.Command == "" || call.Reason == "" || call.Status == "" {
		return ToolCall{}, fmt.Errorf("tool call is missing required fields")
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO tool_calls(
			resource_id, resource_name, harness_id, harness_name,
			command, reason, status, decision, decision_reason,
			execution_status, result, created_at, updated_at, decided_at, finished_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		call.ResourceID, call.ResourceName, call.HarnessID, call.HarnessName,
		call.Command, call.Reason, call.Status, call.Decision, call.DecisionReason,
		call.ExecutionStatus, call.Result, formatTime(now), formatTime(now), formatTimePtr(call.DecidedAt), formatTimePtr(call.FinishedAt),
	)
	if err != nil {
		return ToolCall{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return ToolCall{}, err
	}
	call.ID = id
	call.CreatedAt = now
	call.UpdatedAt = now
	return call, nil
}

func (s *Store) GetToolCall(ctx context.Context, id int64) (ToolCall, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, resource_id, resource_name, harness_id, harness_name,
			command, reason, status, decision, decision_reason,
			execution_status, result, created_at, updated_at, decided_at, finished_at
		FROM tool_calls WHERE id = ?
	`, id)
	call, err := scanToolCall(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolCall{}, ErrNotFound
	}
	return call, err
}

func (s *Store) UpdateToolCall(ctx context.Context, call ToolCall) error {
	if call.Status == "" {
		return fmt.Errorf("tool call status is empty")
	}
	call.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE tool_calls SET
			status = ?,
			decision = ?,
			decision_reason = ?,
			execution_status = ?,
			result = ?,
			updated_at = ?,
			decided_at = ?,
			finished_at = ?
		WHERE id = ?
	`, call.Status, call.Decision, call.DecisionReason, call.ExecutionStatus, call.Result,
		formatTime(call.UpdatedAt), formatTimePtr(call.DecidedAt), formatTimePtr(call.FinishedAt), call.ID)
	if err != nil {
		return err
	}
	return rowsAffected(result)
}

func (s *Store) ListPendingToolCalls(ctx context.Context) ([]ToolCall, error) {
	return s.listToolCalls(ctx, `
		SELECT id, resource_id, resource_name, harness_id, harness_name,
			command, reason, status, decision, decision_reason,
			execution_status, result, created_at, updated_at, decided_at, finished_at
		FROM tool_calls
		WHERE status = ?
		ORDER BY created_at, id
	`, ToolCallPending)
}

// ListQueueToolCalls returns calls still in front of the user, oldest first.
// Review means the command already ran and the user has not sent or rejected the result.
func (s *Store) ListQueueToolCalls(ctx context.Context) ([]ToolCall, error) {
	return s.listToolCalls(ctx, `
		SELECT id, resource_id, resource_name, harness_id, harness_name,
			command, reason, status, decision, decision_reason,
			execution_status, result, created_at, updated_at, decided_at, finished_at
		FROM tool_calls
		WHERE status IN (?, ?, ?)
		ORDER BY created_at, id
	`, ToolCallPending, ToolCallExecuting, ToolCallReview)
}

// ListApprovalToolCalls returns pending and executing calls, then finished
// calls that are still inside window.
func (s *Store) ListApprovalToolCalls(ctx context.Context, window RecentWindow, now time.Time) ([]ToolCall, error) {
	active, err := s.ListQueueToolCalls(ctx)
	if err != nil {
		return nil, err
	}
	recent, err := s.listFinishedToolCalls(ctx, window, now, false)
	if err != nil {
		return nil, err
	}
	return append(active, recent...), nil
}

// ListHistoryToolCalls returns finished calls outside window, newest first.
func (s *Store) ListHistoryToolCalls(ctx context.Context, window RecentWindow, now time.Time) ([]ToolCall, error) {
	return s.listFinishedToolCalls(ctx, window, now, true)
}

// ClearHistoryToolCalls deletes finished calls outside window.
func (s *Store) ClearHistoryToolCalls(ctx context.Context, window RecentWindow, now time.Time) (int64, error) {
	if !window.Valid() {
		return 0, fmt.Errorf("recent window is invalid")
	}
	var (
		result sql.Result
		err    error
	)
	if start, timed := window.Start(now); timed {
		result, err = s.db.ExecContext(ctx, `
			DELETE FROM tool_calls
			WHERE status NOT IN (?, ?, ?)
				AND (finished_at IS NULL OR finished_at < ?)
		`, ToolCallPending, ToolCallExecuting, ToolCallReview, formatTime(start))
	} else {
		result, err = s.db.ExecContext(ctx, `
			DELETE FROM tool_calls
			WHERE status NOT IN (?, ?, ?)
				AND id NOT IN (
					SELECT id FROM tool_calls
					WHERE status NOT IN (?, ?, ?) AND finished_at IS NOT NULL
					ORDER BY finished_at DESC, id DESC
					LIMIT ?
				)
		`, ToolCallPending, ToolCallExecuting, ToolCallReview, ToolCallPending, ToolCallExecuting, ToolCallReview, window.Count)
	}
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ReopenToolCall puts an executing request back in the queue.
// The approval clock starts again, because the user is deciding once more.
func (s *Store) ReopenToolCall(ctx context.Context, id int64) (ToolCall, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE tool_calls SET
			status = ?,
			decision = NULL,
			decision_reason = NULL,
			execution_status = NULL,
			result = NULL,
			created_at = ?,
			updated_at = ?,
			decided_at = NULL,
			finished_at = NULL
		WHERE id = ? AND status = ?
	`, ToolCallPending, formatTime(now), formatTime(now), id, ToolCallExecuting)
	if err != nil {
		return ToolCall{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ToolCall{}, err
	}
	if count == 0 {
		if _, err := s.GetToolCall(ctx, id); err != nil {
			return ToolCall{}, err
		}
		return ToolCall{}, ErrNotPending
	}
	return s.GetToolCall(ctx, id)
}

// BeginExecution moves a pending call into execution without a decision.
// See Results uses this. Approve records its decision through ResolveToolCall.
func (s *Store) BeginExecution(ctx context.Context, id int64) (ToolCall, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE tool_calls SET
			status = ?,
			decision = NULL,
			decision_reason = NULL,
			execution_status = NULL,
			result = NULL,
			updated_at = ?,
			decided_at = ?,
			finished_at = NULL
		WHERE id = ? AND status = ?
	`, ToolCallExecuting, formatTime(now), formatTime(now), id, ToolCallPending)
	if err != nil {
		return ToolCall{}, err
	}
	if err := s.changed(ctx, result, id); err != nil {
		return ToolCall{}, err
	}
	return s.GetToolCall(ctx, id)
}

// FinishReview records the choice to send or withhold a result already in hand.
// The execution status and result stay as they were.
func (s *Store) FinishReview(ctx context.Context, id int64, decision, reason string) (ToolCall, error) {
	if decision != DecisionApprove && decision != DecisionReject {
		return ToolCall{}, fmt.Errorf("tool call resolution is incomplete")
	}
	status := ToolCallApproved
	if decision == DecisionReject {
		status = ToolCallRejected
	}
	now := time.Now().UTC()
	var decisionReason any
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		decisionReason = trimmed
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE tool_calls SET
			status = ?,
			decision = ?,
			decision_reason = ?,
			updated_at = ?,
			decided_at = ?,
			finished_at = ?
		WHERE id = ? AND status = ?
	`, status, decision, decisionReason, formatTime(now), formatTime(now), formatTime(now), id, ToolCallReview)
	if err != nil {
		return ToolCall{}, err
	}
	if err := s.changed(ctx, result, id); err != nil {
		return ToolCall{}, err
	}
	return s.GetToolCall(ctx, id)
}

// ExpireReviewToolCall closes one withheld result because nobody is left to receive it.
func (s *Store) ExpireReviewToolCall(ctx context.Context, id int64, reason string) (ToolCall, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE tool_calls SET
			status = ?,
			decision = ?,
			decision_reason = ?,
			updated_at = ?,
			decided_at = ?,
			finished_at = ?
		WHERE id = ? AND status = ?
	`, ToolCallExpired, DecisionExpire, reason, formatTime(now), formatTime(now), formatTime(now), id, ToolCallReview)
	if err != nil {
		return ToolCall{}, err
	}
	if err := s.changed(ctx, result, id); err != nil {
		return ToolCall{}, err
	}
	return s.GetToolCall(ctx, id)
}

// ExpireReviewToolCalls closes withheld results after a restart.
// The result stays, so history still shows what ran.
func (s *Store) ExpireReviewToolCalls(ctx context.Context, reason string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE tool_calls SET
			status = ?,
			decision = ?,
			decision_reason = ?,
			updated_at = ?,
			decided_at = ?,
			finished_at = ?
		WHERE status = ?
	`, ToolCallExpired, DecisionExpire, reason, formatTime(now), formatTime(now), formatTime(now), ToolCallReview)
	return err
}

func (s *Store) changed(ctx context.Context, result sql.Result, id int64) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		if _, err := s.GetToolCall(ctx, id); err != nil {
			return err
		}
		return ErrNotPending
	}
	return nil
}

// ResolveToolCall moves a pending tool call to status.
// finish sets finished_at. An executing approval does not finish the call.
func (s *Store) ResolveToolCall(ctx context.Context, id int64, status, decision, reason string, finish bool) (ToolCall, error) {
	if status == "" || status == ToolCallPending || decision == "" {
		return ToolCall{}, fmt.Errorf("tool call resolution is incomplete")
	}
	now := time.Now().UTC()
	var decisionReason any
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		decisionReason = trimmed
	}
	var finishedAt any
	if finish {
		finishedAt = formatTime(now)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE tool_calls SET
			status = ?,
			decision = ?,
			decision_reason = ?,
			updated_at = ?,
			decided_at = ?,
			finished_at = ?
		WHERE id = ? AND status = ?
	`, status, decision, decisionReason, formatTime(now), formatTime(now), finishedAt, id, ToolCallPending)
	if err != nil {
		return ToolCall{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ToolCall{}, err
	}
	if count == 0 {
		if _, err := s.GetToolCall(ctx, id); err != nil {
			return ToolCall{}, err
		}
		return ToolCall{}, ErrNotPending
	}
	return s.GetToolCall(ctx, id)
}

// ExpirePendingToolCalls closes every pending call. A restarted process has no
// harness still waiting on those requests.
func (s *Store) ExpirePendingToolCalls(ctx context.Context, reason string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE tool_calls SET
			status = ?,
			decision = ?,
			decision_reason = ?,
			updated_at = ?,
			decided_at = ?,
			finished_at = ?
		WHERE status = ?
	`, ToolCallExpired, DecisionExpire, reason, formatTime(now), formatTime(now), formatTime(now), ToolCallPending)
	return err
}

// ExpireExecutingToolCalls closes runs that were in progress when the process stopped.
func (s *Store) ExpireExecutingToolCalls(ctx context.Context, reason string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE tool_calls SET
			status = ?,
			decision = ?,
			decision_reason = ?,
			execution_status = ?,
			result = ?,
			updated_at = ?,
			finished_at = ?
		WHERE status = ?
	`, ToolCallExpired, DecisionExpire, reason, ExecutionError, reason, formatTime(now), formatTime(now), ToolCallExecuting)
	return err
}

func (s *Store) DeleteToolCall(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM tool_calls WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(result)
}

func (s *Store) listFinishedToolCalls(ctx context.Context, window RecentWindow, now time.Time, history bool) ([]ToolCall, error) {
	if !window.Valid() {
		return nil, fmt.Errorf("recent window is invalid")
	}
	const columns = `
		SELECT id, resource_id, resource_name, harness_id, harness_name,
			command, reason, status, decision, decision_reason,
			execution_status, result, created_at, updated_at, decided_at, finished_at
		FROM tool_calls`
	if start, timed := window.Start(now); timed {
		if history {
			return s.listToolCalls(ctx, columns+`
				WHERE status NOT IN (?, ?, ?) AND (finished_at IS NULL OR finished_at < ?)
				ORDER BY finished_at IS NULL, finished_at DESC, id DESC
			`, ToolCallPending, ToolCallExecuting, ToolCallReview, formatTime(start))
		}
		return s.listToolCalls(ctx, columns+`
			WHERE status NOT IN (?, ?, ?) AND finished_at >= ?
			ORDER BY finished_at DESC, id DESC
		`, ToolCallPending, ToolCallExecuting, ToolCallReview, formatTime(start))
	}

	if history {
		return s.listToolCalls(ctx, columns+`
			WHERE status NOT IN (?, ?, ?)
				AND id NOT IN (
					SELECT id FROM tool_calls
					WHERE status NOT IN (?, ?, ?) AND finished_at IS NOT NULL
					ORDER BY finished_at DESC, id DESC
					LIMIT ?
				)
			ORDER BY finished_at IS NULL, finished_at DESC, id DESC
		`, ToolCallPending, ToolCallExecuting, ToolCallReview, ToolCallPending, ToolCallExecuting, ToolCallReview, window.Count)
	}
	return s.listToolCalls(ctx, columns+`
		WHERE status NOT IN (?, ?, ?) AND finished_at IS NOT NULL
		ORDER BY finished_at DESC, id DESC
		LIMIT ?
	`, ToolCallPending, ToolCallExecuting, ToolCallReview, window.Count)
}

func (s *Store) listToolCalls(ctx context.Context, query string, args ...any) ([]ToolCall, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var calls []ToolCall
	for rows.Next() {
		call, err := scanToolCall(rows)
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if calls == nil {
		calls = []ToolCall{}
	}
	return calls, nil
}

func scanToolCall(row rowScanner) (ToolCall, error) {
	var call ToolCall
	var resourceID, harnessID sql.NullInt64
	var decision, decisionReason, executionStatus, result sql.NullString
	var createdAt, updatedAt string
	var decidedAt, finishedAt sql.NullString
	err := row.Scan(
		&call.ID, &resourceID, &call.ResourceName, &harnessID, &call.HarnessName,
		&call.Command, &call.Reason, &call.Status, &decision, &decisionReason,
		&executionStatus, &result, &createdAt, &updatedAt, &decidedAt, &finishedAt,
	)
	if err != nil {
		return ToolCall{}, err
	}
	if resourceID.Valid {
		call.ResourceID = &resourceID.Int64
	}
	if harnessID.Valid {
		call.HarnessID = &harnessID.Int64
	}
	call.Decision = nullString(decision)
	call.DecisionReason = nullString(decisionReason)
	call.ExecutionStatus = nullString(executionStatus)
	call.Result = nullString(result)
	call.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return ToolCall{}, err
	}
	call.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return ToolCall{}, err
	}
	call.DecidedAt, err = parseTimePtr(decidedAt)
	if err != nil {
		return ToolCall{}, err
	}
	call.FinishedAt, err = parseTimePtr(finishedAt)
	if err != nil {
		return ToolCall{}, err
	}
	return call, nil
}

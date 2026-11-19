package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

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

func (s *Store) DeleteToolCall(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM tool_calls WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(result)
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

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Harness struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

type HarnessToken struct {
	ID        int64
	HarnessID int64
	TokenHash string
	CreatedAt time.Time
}

func (s *Store) CreateHarness(ctx context.Context, name string) (Harness, error) {
	if name == "" {
		return Harness{}, fmt.Errorf("harness name is empty")
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO harnesses(name, created_at) VALUES(?, ?)`, name, formatTime(now))
	if err != nil {
		return Harness{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Harness{}, err
	}
	return Harness{ID: id, Name: name, CreatedAt: now}, nil
}

func (s *Store) HarnessByName(ctx context.Context, name string) (Harness, error) {
	var harness Harness
	var createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, created_at FROM harnesses WHERE name = ?`, name).
		Scan(&harness.ID, &harness.Name, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Harness{}, ErrNotFound
	}
	if err != nil {
		return Harness{}, err
	}
	harness.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Harness{}, err
	}
	return harness, nil
}

// EnsureHarness returns the harness with this name, creating it when absent.
func (s *Store) EnsureHarness(ctx context.Context, name string) (Harness, error) {
	existing, err := s.HarnessByName(ctx, name)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Harness{}, err
	}
	created, err := s.CreateHarness(ctx, name)
	if err == nil {
		return created, nil
	}
	if strings.Contains(err.Error(), "UNIQUE") {
		return s.HarnessByName(ctx, name)
	}
	return Harness{}, err
}

func (s *Store) GetHarness(ctx context.Context, id int64) (Harness, error) {
	var harness Harness
	var createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, created_at FROM harnesses WHERE id = ?`, id).
		Scan(&harness.ID, &harness.Name, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Harness{}, ErrNotFound
	}
	if err != nil {
		return Harness{}, err
	}
	harness.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Harness{}, err
	}
	return harness, nil
}

func (s *Store) UpdateHarness(ctx context.Context, id int64, name string) error {
	if name == "" {
		return fmt.Errorf("harness name is empty")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE harnesses SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return err
	}
	return rowsAffected(result)
}

func (s *Store) DeleteHarness(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM harnesses WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(result)
}

func (s *Store) CreateHarnessToken(ctx context.Context, harnessID int64, tokenHash string) (HarnessToken, error) {
	if tokenHash == "" {
		return HarnessToken{}, fmt.Errorf("token hash is empty")
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO harness_tokens(harness_id, token_hash, created_at) VALUES(?, ?, ?)
	`, harnessID, tokenHash, formatTime(now))
	if err != nil {
		return HarnessToken{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return HarnessToken{}, err
	}
	return HarnessToken{ID: id, HarnessID: harnessID, TokenHash: tokenHash, CreatedAt: now}, nil
}

func (s *Store) DeleteHarnessToken(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM harness_tokens WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(result)
}

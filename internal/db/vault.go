package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrVaultExists = errors.New("vault exists")

type VaultRecord struct {
	Generation int
	Salt       []byte
	Time       int
	Memory     int
	Threads    int
	Verifier   []byte
	CreatedAt  time.Time
}

func (s *Store) CreateVault(ctx context.Context, record VaultRecord) error {
	if len(record.Salt) == 0 || len(record.Verifier) == 0 {
		return fmt.Errorf("vault record is incomplete")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM vault`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrVaultExists
	}

	createdAt := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO vault(generation, state, salt, time, memory, threads, verifier, created_at)
		VALUES (1, 'active', ?, ?, ?, ?, ?, ?)
	`, record.Salt, record.Time, record.Memory, record.Threads, record.Verifier, formatTime(createdAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetVault(ctx context.Context) (VaultRecord, error) {
	var record VaultRecord
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT generation, salt, time, memory, threads, verifier, created_at
		FROM vault WHERE state = 'active'
	`).Scan(&record.Generation, &record.Salt, &record.Time, &record.Memory, &record.Threads, &record.Verifier, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return VaultRecord{}, ErrNotFound
	}
	if err != nil {
		return VaultRecord{}, err
	}
	record.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return VaultRecord{}, err
	}
	return record, nil
}

func (s *Store) CreatePendingVault(ctx context.Context, record VaultRecord) error {
	if record.Generation < 2 || len(record.Salt) == 0 || len(record.Verifier) == 0 {
		return fmt.Errorf("pending vault record is incomplete")
	}
	createdAt := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO vault(generation, state, salt, time, memory, threads, verifier, created_at)
		VALUES (?, 'pending', ?, ?, ?, ?, ?, ?)
	`, record.Generation, record.Salt, record.Time, record.Memory, record.Threads, record.Verifier, formatTime(createdAt))
	return err
}

// ActivateGeneration makes a fully copied pending generation the active one
// and removes the previous vault row. Secret rows from the previous generation
// stay until DeleteRetiredSecrets, so a crash here still leaves the new copies.
func (s *Store) ActivateGeneration(ctx context.Context, generation int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var active int
	err = tx.QueryRowContext(ctx, `SELECT generation FROM vault WHERE state = 'active'`).Scan(&active)
	if err != nil {
		return err
	}
	var missing int
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM resource_secrets AS old
		WHERE old.generation = ?
		AND NOT EXISTS (
			SELECT 1 FROM resource_secrets AS next
			WHERE next.resource_id = old.resource_id AND next.generation = ?
		)
	`, active, generation).Scan(&missing)
	if err != nil {
		return err
	}
	if missing > 0 {
		return fmt.Errorf("password change is missing %d secret copies", missing)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM vault WHERE state = 'active'`); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE vault SET state = 'active' WHERE generation = ? AND state = 'pending'
	`, generation)
	if err != nil {
		return err
	}
	if err := rowsAffected(result); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteRetiredSecrets removes secret copies that are not for the active generation.
func (s *Store) DeleteRetiredSecrets(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM resource_secrets
		WHERE generation != COALESCE((SELECT generation FROM vault WHERE state = 'active'), 0)
	`)
	return err
}

// RecoverPasswordChange drops an unfinished password change.
// The active vault and its secret copies are left in place.
func (s *Store) RecoverPasswordChange(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM resource_secrets
		WHERE generation != COALESCE((SELECT generation FROM vault WHERE state = 'active'), 0)
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM vault WHERE state != 'active'`); err != nil {
		return err
	}
	return tx.Commit()
}

type SecretCopy struct {
	ResourceID int64
	Ciphertext []byte
}

func (s *Store) SecretsForGeneration(ctx context.Context, generation int) ([]SecretCopy, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT resource_id, ciphertext FROM resource_secrets WHERE generation = ? ORDER BY resource_id
	`, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var copies []SecretCopy
	for rows.Next() {
		var copy SecretCopy
		if err := rows.Scan(&copy.ResourceID, &copy.Ciphertext); err != nil {
			return nil, err
		}
		copies = append(copies, copy)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if copies == nil {
		copies = []SecretCopy{}
	}
	return copies, nil
}

func (s *Store) PutSecretGeneration(ctx context.Context, resourceID int64, generation int, ciphertext []byte) error {
	if resourceID < 1 || generation < 1 || len(ciphertext) == 0 {
		return fmt.Errorf("secret copy is incomplete")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO resource_secrets(resource_id, generation, ciphertext) VALUES(?, ?, ?)
		ON CONFLICT(resource_id, generation) DO UPDATE SET ciphertext = excluded.ciphertext
	`, resourceID, generation, ciphertext)
	return err
}

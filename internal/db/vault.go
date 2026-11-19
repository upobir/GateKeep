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
	Salt      []byte
	Time      int
	Memory    int
	Threads   int
	Verifier  []byte
	CreatedAt time.Time
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
		INSERT INTO vault(id, salt, time, memory, threads, verifier, created_at)
		VALUES (1, ?, ?, ?, ?, ?, ?)
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
		SELECT salt, time, memory, threads, verifier, created_at FROM vault WHERE id = 1
	`).Scan(&record.Salt, &record.Time, &record.Memory, &record.Threads, &record.Verifier, &createdAt)
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

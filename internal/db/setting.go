package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("not found")

type Setting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

func (s *Store) PutSetting(ctx context.Context, key, value string) (Setting, error) {
	if key == "" {
		return Setting{}, fmt.Errorf("setting key is empty")
	}
	updatedAt := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings(key, value, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, key, value, formatTime(updatedAt))
	if err != nil {
		return Setting{}, err
	}
	return Setting{Key: key, Value: value, UpdatedAt: updatedAt}, nil
}

func (s *Store) GetSetting(ctx context.Context, key string) (Setting, error) {
	var setting Setting
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT key, value, updated_at FROM settings WHERE key = ?`, key).
		Scan(&setting.Key, &setting.Value, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Setting{}, ErrNotFound
	}
	if err != nil {
		return Setting{}, err
	}
	setting.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Setting{}, err
	}
	return setting, nil
}

func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	if err != nil {
		return err
	}
	return rowsAffected(result)
}

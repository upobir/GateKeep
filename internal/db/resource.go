package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Resource struct {
	ID                 int64
	Name               string
	Type               string
	ConfigJSON         string
	SecretsCiphertext  []byte
	ExecutionTimeoutMs *int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (s *Store) CreateResource(ctx context.Context, resource Resource) (Resource, error) {
	if resource.Name == "" || resource.Type == "" {
		return Resource{}, fmt.Errorf("resource name and type are required")
	}
	if resource.ConfigJSON == "" {
		resource.ConfigJSON = "{}"
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO resources(
			name, type, config_json, secrets_ciphertext, execution_timeout_ms, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?)
	`, resource.Name, resource.Type, resource.ConfigJSON, resource.SecretsCiphertext, resource.ExecutionTimeoutMs, formatTime(now), formatTime(now))
	if err != nil {
		return Resource{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Resource{}, err
	}
	resource.ID = id
	resource.CreatedAt = now
	resource.UpdatedAt = now
	return resource, nil
}

func (s *Store) ListResources(ctx context.Context) ([]Resource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, type, config_json, secrets_ciphertext, execution_timeout_ms, created_at, updated_at
		FROM resources ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var resources []Resource
	for rows.Next() {
		resource, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if resources == nil {
		resources = []Resource{}
	}
	return resources, nil
}

func (s *Store) GetResource(ctx context.Context, id int64) (Resource, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, type, config_json, secrets_ciphertext, execution_timeout_ms, created_at, updated_at
		FROM resources WHERE id = ?
	`, id)
	resource, err := scanResource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Resource{}, ErrNotFound
	}
	return resource, err
}

func (s *Store) UpdateResource(ctx context.Context, resource Resource) error {
	if resource.Name == "" || resource.Type == "" {
		return fmt.Errorf("resource name and type are required")
	}
	if resource.ConfigJSON == "" {
		resource.ConfigJSON = "{}"
	}
	resource.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE resources SET
			name = ?,
			type = ?,
			config_json = ?,
			secrets_ciphertext = ?,
			execution_timeout_ms = ?,
			updated_at = ?
		WHERE id = ?
	`, resource.Name, resource.Type, resource.ConfigJSON, resource.SecretsCiphertext, resource.ExecutionTimeoutMs, formatTime(resource.UpdatedAt), resource.ID)
	if err != nil {
		return err
	}
	return rowsAffected(result)
}

func (s *Store) DeleteResource(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM resources WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(result)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanResource(row rowScanner) (Resource, error) {
	var resource Resource
	var secrets []byte
	var timeout sql.NullInt64
	var createdAt, updatedAt string
	err := row.Scan(
		&resource.ID,
		&resource.Name,
		&resource.Type,
		&resource.ConfigJSON,
		&secrets,
		&timeout,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return Resource{}, err
	}
	if len(secrets) > 0 {
		resource.SecretsCiphertext = secrets
	}
	if timeout.Valid {
		resource.ExecutionTimeoutMs = &timeout.Int64
	}
	resource.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Resource{}, err
	}
	resource.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Resource{}, err
	}
	return resource, nil
}

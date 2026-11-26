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
	ResourceText       string
	SecretsCiphertext  []byte
	ExecutionTimeoutMs *int64
	CacheIdleMs        *int64
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Resource{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO resources(
			name, type, config_json, resource_text, execution_timeout_ms, cache_idle_ms, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, resource.Name, resource.Type, resource.ConfigJSON, resource.ResourceText, resource.ExecutionTimeoutMs, resource.CacheIdleMs, formatTime(now), formatTime(now))
	if err != nil {
		return Resource{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Resource{}, err
	}
	if err := putResourceSecret(ctx, tx, id, resource.SecretsCiphertext); err != nil {
		return Resource{}, err
	}
	if err := tx.Commit(); err != nil {
		return Resource{}, err
	}
	resource.ID = id
	resource.CreatedAt = now
	resource.UpdatedAt = now
	return resource, nil
}

func (s *Store) ListResources(ctx context.Context) ([]Resource, error) {
	rows, err := s.db.QueryContext(ctx, resourceQuery(`ORDER BY name`))
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

func (s *Store) ResourceByName(ctx context.Context, resourceType, name string) (Resource, error) {
	row := s.db.QueryRowContext(ctx, resourceQuery(`WHERE type = ? AND name = ?`), resourceType, name)
	resource, err := scanResource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Resource{}, ErrNotFound
	}
	return resource, err
}

func (s *Store) GetResource(ctx context.Context, id int64) (Resource, error) {
	row := s.db.QueryRowContext(ctx, resourceQuery(`WHERE id = ?`), id)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE resources SET
			name = ?,
			type = ?,
			config_json = ?,
			resource_text = ?,
			execution_timeout_ms = ?,
			cache_idle_ms = ?,
			updated_at = ?
		WHERE id = ?
	`, resource.Name, resource.Type, resource.ConfigJSON, resource.ResourceText, resource.ExecutionTimeoutMs, resource.CacheIdleMs, formatTime(resource.UpdatedAt), resource.ID)
	if err != nil {
		return err
	}
	if err := rowsAffected(result); err != nil {
		return err
	}
	if err := putResourceSecret(ctx, tx, resource.ID, resource.SecretsCiphertext); err != nil {
		return err
	}
	return tx.Commit()
}

// resourceSecretExpr is the ciphertext for the active password generation.
// Generation 0 holds secrets written before a password exists.
const resourceSecretExpr = `(SELECT ciphertext FROM resource_secrets WHERE resource_id = resources.id AND generation = COALESCE((SELECT generation FROM vault WHERE state = 'active'), 0))`

func resourceQuery(tail string) string {
	return `
		SELECT id, name, type, config_json, resource_text, ` + resourceSecretExpr + `, execution_timeout_ms, cache_idle_ms, created_at, updated_at
		FROM resources ` + tail
}

func putResourceSecret(ctx context.Context, tx *sql.Tx, resourceID int64, ciphertext []byte) error {
	var generation int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE((SELECT generation FROM vault WHERE state = 'active'), 0)
	`).Scan(&generation); err != nil {
		return err
	}
	if len(ciphertext) == 0 {
		_, err := tx.ExecContext(ctx, `
			DELETE FROM resource_secrets WHERE resource_id = ? AND generation = ?
		`, resourceID, generation)
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO resource_secrets(resource_id, generation, ciphertext) VALUES(?, ?, ?)
		ON CONFLICT(resource_id, generation) DO UPDATE SET ciphertext = excluded.ciphertext
	`, resourceID, generation, ciphertext)
	return err
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
	var timeout, cacheIdle sql.NullInt64
	var createdAt, updatedAt string
	err := row.Scan(
		&resource.ID,
		&resource.Name,
		&resource.Type,
		&resource.ConfigJSON,
		&resource.ResourceText,
		&secrets,
		&timeout,
		&cacheIdle,
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
	if cacheIdle.Valid {
		resource.CacheIdleMs = &cacheIdle.Int64
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

// UpdateResourceText changes the MCP document independently of connection settings.
func (s *Store) UpdateResourceText(ctx context.Context, id int64, text string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE resources SET resource_text = ?, updated_at = ? WHERE id = ?`, text, formatTime(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(result)
}

package storepostgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"codereviewer/internal/ports/store"
)

// SettingsStore is the Postgres implementation of store.SettingsStore.
type SettingsStore struct {
	pool *pgxpool.Pool
}

// Get returns the value for key, or found=false if absent.
func (s *SettingsStore) Get(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.pool.QueryRow(ctx,
		`SELECT setting_value FROM app_settings WHERE setting_key = $1`,
		key,
	).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get setting %q: %w", key, err)
	}
	return v, true, nil
}

// GetAll returns every setting, ordered by key.
func (s *SettingsStore) GetAll(ctx context.Context) ([]store.Setting, error) {
	rows, err := s.pool.Query(ctx, `
SELECT setting_key, setting_value, updated_at, updated_by
FROM app_settings
ORDER BY setting_key
`)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()

	var out []store.Setting
	for rows.Next() {
		var st store.Setting
		if err := rows.Scan(&st.Key, &st.Value, &st.UpdatedAt, &st.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Set upserts (key, value, updated_by).
func (s *SettingsStore) Set(ctx context.Context, key, value, updatedBy string) error {
	if updatedBy == "" {
		updatedBy = "system"
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO app_settings (setting_key, setting_value, updated_at, updated_by)
VALUES ($1, $2, now(), $3)
ON CONFLICT (setting_key) DO UPDATE SET
  setting_value = EXCLUDED.setting_value,
  updated_at    = now(),
  updated_by    = EXCLUDED.updated_by
`, key, value, updatedBy)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// Delete removes the row for key. Absent key is not an error.
func (s *SettingsStore) Delete(ctx context.Context, key string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM app_settings WHERE setting_key = $1`, key)
	if err != nil {
		return fmt.Errorf("delete setting %q: %w", key, err)
	}
	return nil
}

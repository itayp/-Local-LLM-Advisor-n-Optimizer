package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Setting reads one key from the settings table (schema v0). ok is false
// when the key has never been set — never a default value guessed on its
// behalf (D-21).
func (s *Store) Setting(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: reading setting %q: %w", key, err)
	}
	return value, true, nil
}

// SetSetting stores key=value, creating the row or replacing it in place —
// the settings table has no history (unlike hardware_profiles or
// installed_models): a setting is the machine's current choice, not
// evidence to keep.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value, Now())
	if err != nil {
		return fmt.Errorf("store: writing setting %q: %w", key, err)
	}
	return nil
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"advisor/internal/backend"
	"advisor/internal/hardware"
)

// BackendRow is one recorded Detect() of a runtime: a row per check, never
// updated or deleted, so "what runtime path did this benchmark actually
// use" stays answerable after Ollama is upgraded or a GPU changes (the
// same history-not-update shape as hardware_profiles).
type BackendRow struct {
	ID               int64
	CreatedAt        string // RFC 3339 UTC
	Name             string // backend.Backend.Name(), e.g. "ollama"
	State            backend.State
	Version          string // reported by the running runtime; "" when not running
	Host             string // the API endpoint checked, e.g. http://127.0.0.1:11434
	InstalledVersion string // what this app's own Install recorded; "" if the user installed it themselves or nothing has installed yet
	RuntimePaths     map[int]hardware.RuntimePath
	Env              map[string]string
	Detail           string
}

// RecordBackend stores one Detect() result as a new row — an insert-only
// history, mirroring AddHardwareProfile: the previous check's row stays
// exactly as it was, so nothing about what a runtime path used to be gets
// silently rewritten out from under a stored benchmark or estimate.
func (s *Store) RecordBackend(ctx context.Context, name string, status backend.Status, installedVersion string) (BackendRow, error) {
	paths, err := json.Marshal(nonNilRuntimePaths(status.RuntimePaths))
	if err != nil {
		return BackendRow{}, fmt.Errorf("store: encoding runtime paths for %s: %w", name, err)
	}
	env, err := json.Marshal(nonNilEnv(status.Env))
	if err != nil {
		return BackendRow{}, fmt.Errorf("store: encoding env for %s: %w", name, err)
	}
	row := BackendRow{
		CreatedAt:        Now(),
		Name:             name,
		State:            status.State,
		Version:          status.Version,
		Host:             status.Host,
		InstalledVersion: installedVersion,
		RuntimePaths:     status.RuntimePaths,
		Env:              status.Env,
		Detail:           status.Detail,
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO backends
		(created_at, name, state, version, host, installed_version, runtime_paths_json, env_json, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.CreatedAt, name, string(status.State), status.Version, status.Host, installedVersion,
		string(paths), string(env), status.Detail)
	if err != nil {
		return BackendRow{}, fmt.Errorf("store: inserting backend row for %s: %w", name, err)
	}
	if row.ID, err = res.LastInsertId(); err != nil {
		return BackendRow{}, fmt.Errorf("store: backend row id: %w", err)
	}
	return row, nil
}

// nonNilRuntimePaths and nonNilEnv keep the JSON columns' NOT NULL DEFAULT
// '{}' meaningful: json.Marshal(nil map) is the four bytes "null", not "{}".
func nonNilRuntimePaths(m map[int]hardware.RuntimePath) map[int]hardware.RuntimePath {
	if m == nil {
		return map[int]hardware.RuntimePath{}
	}
	return m
}

func nonNilEnv(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

const backendColumns = `id, created_at, name, state, version, host, installed_version, runtime_paths_json, env_json, detail`

func scanBackend(sc interface{ Scan(...any) error }) (BackendRow, error) {
	var r BackendRow
	var state, paths, env string
	if err := sc.Scan(&r.ID, &r.CreatedAt, &r.Name, &state, &r.Version, &r.Host,
		&r.InstalledVersion, &paths, &env, &r.Detail); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, ErrNotFound
		}
		return r, err
	}
	r.State = backend.State(state)
	if err := json.Unmarshal([]byte(paths), &r.RuntimePaths); err != nil {
		return r, fmt.Errorf("store: backend row %d: runtime paths: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(env), &r.Env); err != nil {
		return r, fmt.Errorf("store: backend row %d: env: %w", r.ID, err)
	}
	return r, nil
}

// LatestBackend returns the most recently recorded check for name, or
// ErrNotFound if this backend has never been checked.
func (s *Store) LatestBackend(ctx context.Context, name string) (BackendRow, error) {
	return scanBackend(s.db.QueryRowContext(ctx,
		`SELECT `+backendColumns+` FROM backends WHERE name = ? ORDER BY id DESC LIMIT 1`, name))
}

// LatestBackends returns the most recent row for every backend name ever
// recorded, sorted by name — the whole inventory GET /api/backends serves.
func (s *Store) LatestBackends(ctx context.Context) ([]BackendRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+backendColumns+` FROM backends b
		WHERE b.id = (SELECT MAX(id) FROM backends WHERE name = b.name)
		ORDER BY b.name`)
	if err != nil {
		return nil, fmt.Errorf("store: latest backends: %w", err)
	}
	defer rows.Close()
	var out []BackendRow
	for rows.Next() {
		r, err := scanBackend(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"advisor/internal/backend"
)

// InstalledModelRow is one model a runtime has reported, refreshed on
// every daemon start and on demand (step 3, item 4). Present distinguishes
// "the runtime stopped listing this" from "we never heard of it": a row
// that drops out of the runtime's inventory is marked Present = false, not
// deleted, so a benchmark or estimate that still references it by name
// keeps its history.
type InstalledModelRow struct {
	ID            int64
	CreatedAt     string // when this name was first seen for this backend
	BackendName   string
	Name          string // "llama3.1:8b"
	Digest        string
	SizeBytes     uint64
	Quantization  string
	Family        string
	ParameterSize string
	ModifiedAt    string // as the runtime reports it; "" if it does not say
	LastSeenAt    string // updated every time the runtime lists it
	Present       bool

	// How the model maps onto the catalogue (step 4): CatalogMatch is ""
	// until mapped, then "file", "model" or "unknown" (catalog.MatchKind);
	// the ids are 0 when there is nothing to point at.
	CatalogModelID int64
	CatalogFileID  int64
	CatalogMatch   string
	CatalogNote    string
}

// UpsertInstalledModels replaces backendName's inventory with models: every
// existing row for this backend is first marked absent, then every model
// the runtime just reported is inserted (first sighting) or updated back
// to present (a name that was already known). Nothing is deleted — a model
// the user removed from Ollama still shows up in a benchmark's history,
// just no longer as "installed".
func (s *Store) UpsertInstalledModels(ctx context.Context, backendName string, models []backend.Installed) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: installed models for %s: %w", backendName, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	now := Now()
	if _, err := tx.ExecContext(ctx,
		`UPDATE installed_models SET present = 0 WHERE backend_name = ?`, backendName); err != nil {
		return fmt.Errorf("store: marking %s's models absent: %w", backendName, err)
	}
	for _, m := range models {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO installed_models
				(created_at, backend_name, name, digest, size_bytes, quantization, family,
				 parameter_size, modified_at, last_seen_at, present)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
			ON CONFLICT (backend_name, name) DO UPDATE SET
				digest = excluded.digest,
				size_bytes = excluded.size_bytes,
				quantization = excluded.quantization,
				family = excluded.family,
				parameter_size = excluded.parameter_size,
				modified_at = excluded.modified_at,
				last_seen_at = excluded.last_seen_at,
				present = 1`,
			now, backendName, m.Name, m.Digest, int64(m.SizeBytes), m.Quantization, m.Family,
			m.ParameterSize, m.ModifiedAt, now); err != nil {
			return fmt.Errorf("store: recording installed model %s/%s: %w", backendName, m.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: installed models for %s: %w", backendName, err)
	}
	return nil
}

const installedModelColumns = `id, created_at, backend_name, name, digest, size_bytes, quantization, family, parameter_size, modified_at, last_seen_at, present, catalog_model_id, catalog_file_id, catalog_match, catalog_note`

func scanInstalledModel(sc interface{ Scan(...any) error }) (InstalledModelRow, error) {
	var r InstalledModelRow
	var size int64
	var present int
	var modelID, fileID sql.NullInt64
	if err := sc.Scan(&r.ID, &r.CreatedAt, &r.BackendName, &r.Name, &r.Digest, &size,
		&r.Quantization, &r.Family, &r.ParameterSize, &r.ModifiedAt, &r.LastSeenAt, &present,
		&modelID, &fileID, &r.CatalogMatch, &r.CatalogNote); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, ErrNotFound
		}
		return r, err
	}
	r.SizeBytes = uint64(size)
	r.Present = present != 0
	r.CatalogModelID, r.CatalogFileID = modelID.Int64, fileID.Int64
	return r, nil
}

// InstalledModels returns backendName's models currently reported as
// present, name-ordered. "Installed but not running" vs. "not installed"
// is Running() (in-memory, from the backend directly) layered on top of
// this list, not a column here — this table only ever reflects Models().
func (s *Store) InstalledModels(ctx context.Context, backendName string) ([]InstalledModelRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+installedModelColumns+` FROM installed_models WHERE backend_name = ? AND present = 1 ORDER BY name`,
		backendName)
	if err != nil {
		return nil, fmt.Errorf("store: installed models for %s: %w", backendName, err)
	}
	defer rows.Close()
	var out []InstalledModelRow
	for rows.Next() {
		r, err := scanInstalledModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AllInstalledModels returns every present model across every backend,
// backend then name ordered — GET /api/models/installed's whole payload.
func (s *Store) AllInstalledModels(ctx context.Context) ([]InstalledModelRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+installedModelColumns+` FROM installed_models WHERE present = 1 ORDER BY backend_name, name`)
	if err != nil {
		return nil, fmt.Errorf("store: all installed models: %w", err)
	}
	defer rows.Close()
	var out []InstalledModelRow
	for rows.Next() {
		r, err := scanInstalledModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

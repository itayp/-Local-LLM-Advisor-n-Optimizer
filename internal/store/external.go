package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"advisor/internal/catalog"
)

// ExternalScope is the part of one source's stored values a read replaced:
// every value of Source whose source_model_id is SourceModel (when set) and
// whose metric is Metric (when set). Values in the scope that the read did
// not return are marked present = 0, never deleted.
type ExternalScope struct {
	Source      string
	SourceModel string // "" = any
	Metric      string // "" = any
}

// ReplaceExternal stores what one read of a source returned for scope: rows
// are upserted as present, and every other value in scope is marked absent,
// in one transaction. It returns how many values were marked absent that
// were present before.
func (s *Store) ReplaceExternal(ctx context.Context, scope ExternalScope, rows []catalog.External) (int, error) {
	if scope.Source == "" {
		return 0, errors.New("store: replacing public values needs a source")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: public values of %s: %w", scope.Source, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	now := Now()
	const inScope = `source = ? AND (? = '' OR source_model_id = ?) AND (? = '' OR metric = ?)`
	args := []any{scope.Source, scope.SourceModel, scope.SourceModel, scope.Metric, scope.Metric}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_external SET present = 2 WHERE present = 1 AND `+inScope, args...); err != nil {
		return 0, fmt.Errorf("store: public values of %s: %w", scope.Source, err)
	}
	for _, r := range rows {
		if r.Source != scope.Source {
			return 0, fmt.Errorf("store: a %s value in a %s replace", r.Source, scope.Source)
		}
		if r.ModelID == 0 {
			return 0, fmt.Errorf("store: public value %s %s maps to no catalogue size", r.SourceModel, r.Metric)
		}
		detail := []byte("{}")
		if len(r.Detail) > 0 {
			if detail, err = json.Marshal(r.Detail); err != nil {
				return 0, fmt.Errorf("store: public value %s %s detail: %w", r.SourceModel, r.Metric, err)
			}
		}
		fetched := r.FetchedAt
		if fetched == "" {
			fetched = now
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO catalog_external
				(created_at, source, source_model_id, catalog_model_id, metric, value, value_text, fetched_at, license,
				 source_date, source_url, provenance, attribution, detail_json, present, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
			ON CONFLICT (source, source_model_id, metric) DO UPDATE SET
				catalog_model_id = excluded.catalog_model_id, value = excluded.value, value_text = excluded.value_text,
				fetched_at = excluded.fetched_at, license = excluded.license, source_date = excluded.source_date,
				source_url = excluded.source_url, provenance = excluded.provenance, attribution = excluded.attribution,
				detail_json = excluded.detail_json, present = 1, updated_at = excluded.updated_at`,
			now, r.Source, r.SourceModel, r.ModelID, r.Metric, r.Value, r.ValueText, fetched, r.License,
			r.SourceDate, r.SourceURL, r.Provenance, r.Attribution, string(detail), now); err != nil {
			return 0, fmt.Errorf("store: public value %s %s: %w", r.SourceModel, r.Metric, err)
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE catalog_external SET present = 0, updated_at = ? WHERE present = 2 AND `+inScope,
		append([]any{now}, args...)...)
	if err != nil {
		return 0, fmt.Errorf("store: public values of %s: %w", scope.Source, err)
	}
	gone, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: public values of %s: %w", scope.Source, err)
	}
	return int(gone), nil
}

// ExternalValues returns the stored public values, source then size then
// metric ordered. includeAbsent adds the ones that have left their source.
func (s *Store) ExternalValues(ctx context.Context, includeAbsent bool) ([]catalog.External, error) {
	q := `SELECT id, source, source_model_id, COALESCE(catalog_model_id, 0), metric, COALESCE(value, 0), value_text,
		fetched_at, license, source_date, source_url, provenance, attribution, detail_json, present
		FROM catalog_external`
	if !includeAbsent {
		q += ` WHERE present = 1`
	}
	q += ` ORDER BY source, catalog_model_id, metric, source_model_id`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: public values: %w", err)
	}
	defer rows.Close()
	var out []catalog.External
	for rows.Next() {
		var e catalog.External
		var detail string
		var present int
		if err := rows.Scan(&e.ID, &e.Source, &e.SourceModel, &e.ModelID, &e.Metric, &e.Value, &e.ValueText,
			&e.FetchedAt, &e.License, &e.SourceDate, &e.SourceURL, &e.Provenance, &e.Attribution, &detail, &present); err != nil {
			return nil, fmt.Errorf("store: public values: %w", err)
		}
		e.Present = present == 1
		if detail != "" && detail != "{}" {
			if err := json.Unmarshal([]byte(detail), &e.Detail); err != nil {
				return nil, fmt.Errorf("store: catalog_external %d detail_json: %w", e.ID, err)
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetReleasedAt records when a catalogue size's original model was released
// (its repo's creation date, YYYY-MM-DD).
func (s *Store) SetReleasedAt(ctx context.Context, modelID int64, date string) error {
	v := sql.NullString{String: date, Valid: date != ""}
	if _, err := s.db.ExecContext(ctx, `UPDATE catalog_models SET released_at = ? WHERE id = ?`, v, modelID); err != nil {
		return fmt.Errorf("store: release date of model %d: %w", modelID, err)
	}
	return nil
}

// ExternalState is one row of external_state: a source's run state (Key
// "") or one request's validators (Key = its URL).
type ExternalState struct {
	Source       string
	Key          string
	ETag         string
	LastModified string
	ConfigDigest string
	AttemptedAt  string // RFC 3339; "" = never tried
	OKAt         string // RFC 3339; "" = never succeeded
	Error        string // why the last try failed, in words
}

// GetExternalState returns (source, key)'s state; ok is false when there is none.
func (s *Store) GetExternalState(ctx context.Context, source, key string) (ExternalState, bool, error) {
	st := ExternalState{Source: source, Key: key}
	err := s.db.QueryRowContext(ctx, `
		SELECT etag, last_modified, config_digest, attempted_at, ok_at, error
		FROM external_state WHERE source = ? AND key = ?`, source, key).Scan(
		&st.ETag, &st.LastModified, &st.ConfigDigest, &st.AttemptedAt, &st.OKAt, &st.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return st, false, nil
	}
	if err != nil {
		return st, false, fmt.Errorf("store: external state %s %s: %w", source, key, err)
	}
	return st, true, nil
}

// PutExternalState writes (source, key)'s state.
func (s *Store) PutExternalState(ctx context.Context, st ExternalState) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO external_state (created_at, source, key, etag, last_modified, config_digest, attempted_at, ok_at, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (source, key) DO UPDATE SET
			etag = excluded.etag, last_modified = excluded.last_modified, config_digest = excluded.config_digest,
			attempted_at = excluded.attempted_at, ok_at = excluded.ok_at, error = excluded.error`,
		Now(), st.Source, st.Key, st.ETag, st.LastModified, st.ConfigDigest, st.AttemptedAt, st.OKAt, st.Error)
	if err != nil {
		return fmt.Errorf("store: external state %s %s: %w", st.Source, st.Key, err)
	}
	return nil
}

// ExternalStates returns every source's run state (key ""), by source.
func (s *Store) ExternalStates(ctx context.Context) (map[string]ExternalState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT source, etag, last_modified, config_digest, attempted_at, ok_at, error
		FROM external_state WHERE key = ''`)
	if err != nil {
		return nil, fmt.Errorf("store: external states: %w", err)
	}
	defer rows.Close()
	out := map[string]ExternalState{}
	for rows.Next() {
		var st ExternalState
		if err := rows.Scan(&st.Source, &st.ETag, &st.LastModified, &st.ConfigDigest, &st.AttemptedAt, &st.OKAt, &st.Error); err != nil {
			return nil, fmt.Errorf("store: external states: %w", err)
		}
		out[st.Source] = st
	}
	return out, rows.Err()
}

// ExternalPartsFailed counts source's per-request or per-metric states
// (key not "") whose last try failed: a source read only in part.
func (s *Store) ExternalPartsFailed(ctx context.Context, source string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM external_state WHERE source = ? AND key <> '' AND error <> ''`, source).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: external state %s: %w", source, err)
	}
	return n, nil
}

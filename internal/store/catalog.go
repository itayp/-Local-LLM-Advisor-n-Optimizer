package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/catalog/gguf"
)

func fileTypeName(ft int) string { return gguf.FileTypeName(uint32(ft)) }

// CatalogKey identifies a catalogue size across refreshes: catalog_models'
// UNIQUE (family_id, parameters).
type CatalogKey struct {
	FamilyID   string
	Parameters uint64
}

// CatalogModelRow is one stored catalogue size with the family fields the
// row carries (a size that has left families.yaml keeps the family as it
// was, so it can still be named).
type CatalogModelRow struct {
	Model       catalog.Model
	DisplayName string
	Maintainer  string
	License     catalog.License
	Purposes    []catalog.Purpose
	ReviewedAt  string
	SourceURL   string
	Notes       string
}

// SyncCatalogModels makes catalog_models agree with cat: every size gets a
// row (inserted the first time, its fields updated after), and every row
// whose size is no longer in cat is marked present = 0 — never deleted,
// since estimates and benchmarks point at its files. It returns the row id
// of every size in cat.
func (s *Store) SyncCatalogModels(ctx context.Context, cat *catalog.Catalogue) (map[CatalogKey]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: syncing the catalogue: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	now := Now()
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_models SET present = 0`); err != nil {
		return nil, fmt.Errorf("store: syncing the catalogue: %w", err)
	}
	ids := map[CatalogKey]int64{}
	for _, f := range cat.Families {
		license, err := json.Marshal(f.License)
		if err != nil {
			return nil, err
		}
		purposes, err := json.Marshal(f.Purposes)
		if err != nil {
			return nil, err
		}
		for _, sz := range f.Sizes {
			var id int64
			err := tx.QueryRowContext(ctx, `
				INSERT INTO catalog_models
					(created_at, family_id, display_name, maintainer, license_json, purposes_json,
					 parameters, context_length, ollama_tag, hf_repo, reviewed_at,
					 active_parameters, source_url, notes, present, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
				ON CONFLICT (family_id, parameters) DO UPDATE SET
					display_name = excluded.display_name,
					maintainer = excluded.maintainer,
					license_json = excluded.license_json,
					purposes_json = excluded.purposes_json,
					context_length = excluded.context_length,
					ollama_tag = excluded.ollama_tag,
					hf_repo = excluded.hf_repo,
					reviewed_at = excluded.reviewed_at,
					active_parameters = excluded.active_parameters,
					source_url = excluded.source_url,
					notes = excluded.notes,
					present = 1,
					updated_at = excluded.updated_at
				RETURNING id`,
				now, f.ID, f.DisplayName, f.Maintainer, string(license), string(purposes),
				int64(sz.Parameters), sz.ContextLength, sz.OllamaTag, sz.HFRepo, f.ReviewedAt,
				int64(sz.ActiveParameters), f.Source, f.Notes, now).Scan(&id)
			if err != nil {
				return nil, fmt.Errorf("store: catalogue size %s %s: %w", f.ID, sz.OllamaTag, err)
			}
			ids[CatalogKey{f.ID, sz.Parameters}] = id
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: syncing the catalogue: %w", err)
	}
	return ids, nil
}

// CatalogFileWrite is one file a refresh resolved, with the header metadata
// that goes to header_json.
type CatalogFileWrite struct {
	File       catalog.File
	HeaderJSON []byte
}

// CatalogModelRefresh is what one refresh of one size learned.
type CatalogModelRefresh struct {
	HFSHA             string
	ParametersCounted uint64 // 0 = the Hub gave none (stored as NULL)
	Files             []CatalogFileWrite
}

// RecordCatalogModelRefresh stores a successful refresh of one size: its
// files are upserted, the size's other files are marked present = 0, and
// the size's refresh fields are set, in one transaction.
func (s *Store) RecordCatalogModelRefresh(ctx context.Context, modelID int64, r CatalogModelRefresh) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: catalogue refresh of model %d: %w", modelID, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	now := Now()
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_files SET present = 0 WHERE catalog_model_id = ?`, modelID); err != nil {
		return fmt.Errorf("store: catalogue refresh of model %d: %w", modelID, err)
	}
	for _, w := range r.Files {
		f, h := w.File, w.File.Header
		hj := string(w.HeaderJSON)
		if hj == "" {
			hj = "{}"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO catalog_files
				(created_at, catalog_model_id, filename, quant, sha, bytes, bits_per_weight,
				 architecture, block_count, head_count, head_count_kv, key_length, embedding_length,
				 context_length, sliding_window, file_type, expert_count, has_vision, header_json, fetched_at,
				 role, parts, gguf_version, tensor_count, head_count_kv_stated, value_length,
				 full_attention_interval, expert_used_count, header_complete, present, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
			ON CONFLICT (catalog_model_id, filename) DO UPDATE SET
				quant = excluded.quant, sha = excluded.sha, bytes = excluded.bytes,
				bits_per_weight = excluded.bits_per_weight, architecture = excluded.architecture,
				block_count = excluded.block_count, head_count = excluded.head_count,
				head_count_kv = excluded.head_count_kv, key_length = excluded.key_length,
				embedding_length = excluded.embedding_length, context_length = excluded.context_length,
				sliding_window = excluded.sliding_window, file_type = excluded.file_type,
				expert_count = excluded.expert_count, has_vision = excluded.has_vision,
				header_json = excluded.header_json, fetched_at = excluded.fetched_at,
				role = excluded.role, parts = excluded.parts, gguf_version = excluded.gguf_version,
				tensor_count = excluded.tensor_count, head_count_kv_stated = excluded.head_count_kv_stated,
				value_length = excluded.value_length, full_attention_interval = excluded.full_attention_interval,
				expert_used_count = excluded.expert_used_count, header_complete = excluded.header_complete,
				present = 1, updated_at = excluded.updated_at`,
			now, modelID, f.Filename, f.Quant, f.SHA, int64(f.Bytes), f.BitsPerWeight,
			h.Architecture, h.BlockCount, h.HeadCount, h.HeadCountKV, h.KeyLength, h.EmbeddingLength,
			h.ContextLength, h.SlidingWindow, h.FileType, h.ExpertCount, h.HasVision, hj, f.FetchedAt.UTC().Format(time.RFC3339),
			string(f.Role), f.Parts, h.GGUFVersion, h.TensorCount, h.HeadCountKVStated, h.ValueLength,
			h.FullAttentionInterval, h.ExpertUsedCount, h.Complete, now); err != nil {
			return fmt.Errorf("store: catalogue file %s: %w", f.Filename, err)
		}
	}
	counted := sql.NullInt64{Int64: int64(r.ParametersCounted), Valid: r.ParametersCounted > 0}
	if _, err := tx.ExecContext(ctx, `
		UPDATE catalog_models SET hf_sha = ?, parameters_counted = ?, refreshed_at = ?, refresh_error = ''
		WHERE id = ?`, r.HFSHA, counted, now, modelID); err != nil {
		return fmt.Errorf("store: catalogue refresh of model %d: %w", modelID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: catalogue refresh of model %d: %w", modelID, err)
	}
	return nil
}

// RecordCatalogModelError stores why a size could not be refreshed. What
// an earlier refresh found (its files, refreshed_at) is left as it was: a
// failed refresh is not evidence that the files are gone.
func (s *Store) RecordCatalogModelError(ctx context.Context, modelID int64, msg string) error {
	if msg == "" {
		msg = "unknown error"
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE catalog_models SET refresh_error = ? WHERE id = ?`, msg, modelID); err != nil {
		return fmt.Errorf("store: recording the refresh error of model %d: %w", modelID, err)
	}
	return nil
}

const catalogModelColumns = `id, family_id, display_name, maintainer, license_json, purposes_json, parameters,
	active_parameters, context_length, ollama_tag, hf_repo, reviewed_at, source_url, notes, present,
	hf_sha, parameters_counted, refreshed_at, refresh_error`

func scanCatalogModel(sc interface{ Scan(...any) error }) (CatalogModelRow, error) {
	var r CatalogModelRow
	var license, purposes string
	var params, active int64
	var present int
	var counted sql.NullInt64
	var refreshed sql.NullString
	m := &r.Model
	if err := sc.Scan(&m.ID, &m.FamilyID, &r.DisplayName, &r.Maintainer, &license, &purposes, &params,
		&active, &m.Size.ContextLength, &m.Size.OllamaTag, &m.Size.HFRepo, &r.ReviewedAt, &r.SourceURL, &r.Notes,
		&present, &m.HFSHA, &counted, &refreshed, &m.RefreshError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, ErrNotFound
		}
		return r, err
	}
	m.Size.Parameters, m.Size.ActiveParameters = uint64(params), uint64(active)
	m.Present = present != 0
	m.ParametersCounted = uint64(counted.Int64)
	m.RefreshedAt = refreshed.String
	if err := json.Unmarshal([]byte(license), &r.License); err != nil {
		return r, fmt.Errorf("store: catalog_models %d license_json: %w", m.ID, err)
	}
	if err := json.Unmarshal([]byte(purposes), &r.Purposes); err != nil {
		return r, fmt.Errorf("store: catalog_models %d purposes_json: %w", m.ID, err)
	}
	m.Files = []catalog.File{}
	return r, nil
}

// CatalogModels returns the stored catalogue, family then size ordered, each
// size with its files. includeAbsent adds sizes and files that have left the
// catalogue or their repo.
func (s *Store) CatalogModels(ctx context.Context, includeAbsent bool) ([]CatalogModelRow, error) {
	q := `SELECT ` + catalogModelColumns + ` FROM catalog_models`
	if !includeAbsent {
		q += ` WHERE present = 1`
	}
	q += ` ORDER BY family_id, parameters`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: catalogue: %w", err)
	}
	var out []CatalogModelRow
	byID := map[int64]int{}
	for rows.Next() {
		r, err := scanCatalogModel(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		byID[r.Model.ID] = len(out)
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	files, err := s.catalogFiles(ctx, includeAbsent)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if i, ok := byID[f.ModelID]; ok {
			out[i].Model.Files = append(out[i].Model.Files, f)
		}
	}
	return out, nil
}

func (s *Store) catalogFiles(ctx context.Context, includeAbsent bool) ([]catalog.File, error) {
	q := `SELECT id, catalog_model_id, filename, role, quant, sha, parts, bytes, bits_per_weight, present,
		architecture, gguf_version, tensor_count, block_count, head_count, head_count_kv, head_count_kv_stated,
		key_length, value_length, embedding_length, context_length, sliding_window, full_attention_interval,
		file_type, expert_count, expert_used_count, has_vision, header_complete, fetched_at, header_json
		FROM catalog_files`
	if !includeAbsent {
		q += ` WHERE present = 1`
	}
	q += ` ORDER BY catalog_model_id, role, bytes`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: catalogue files: %w", err)
	}
	defer rows.Close()
	var out []catalog.File
	for rows.Next() {
		var f catalog.File
		var role, fetched, headerJSON string
		var bytes int64
		var present int
		h := &f.Header
		if err := rows.Scan(&f.ID, &f.ModelID, &f.Filename, &role, &f.Quant, &f.SHA, &f.Parts, &bytes,
			&f.BitsPerWeight, &present, &h.Architecture, &h.GGUFVersion, &h.TensorCount, &h.BlockCount,
			&h.HeadCount, &h.HeadCountKV, &h.HeadCountKVStated, &h.KeyLength, &h.ValueLength,
			&h.EmbeddingLength, &h.ContextLength, &h.SlidingWindow, &h.FullAttentionInterval, &h.FileType,
			&h.ExpertCount, &h.ExpertUsedCount, &h.HasVision, &h.Complete, &fetched, &headerJSON); err != nil {
			return nil, fmt.Errorf("store: catalogue files: %w", err)
		}
		f.Role, f.Bytes, f.Present = catalog.FileRole(role), uint64(bytes), present != 0
		if h.FileType >= 0 {
			h.FileTypeName = fileTypeName(h.FileType)
		}
		f.FetchedAt, _ = time.Parse(time.RFC3339, fetched)
		if f.Role == catalog.RoleModel {
			// The layout is derived on every read, never stored: a better
			// reading of the same header then needs no catalogue refresh.
			var raw struct {
				KV map[string]any `json:"kv"`
			}
			_ = json.Unmarshal([]byte(headerJSON), &raw) // an unreadable header_json leaves the typed columns, which NewLayout accepts
			f.Layout = catalog.NewLayout(f.Header, raw.KV)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CatalogFileHeaderJSON is a file's header_json: every metadata pair the
// parser kept (the Advanced view's raw table).
func (s *Store) CatalogFileHeaderJSON(ctx context.Context, fileID int64) ([]byte, error) {
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT header_json FROM catalog_files WHERE id = ?`, fileID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: header of catalogue file %d: %w", fileID, err)
	}
	return []byte(body), nil
}

// CachedHeader returns the cached parse of (repo, filename, sha).
func (s *Store) CachedHeader(ctx context.Context, repo, filename, sha string) ([]byte, bool, error) {
	if sha == "" {
		return nil, false, nil // no content hash, no cache key
	}
	var body string
	err := s.db.QueryRowContext(ctx,
		`SELECT header_json FROM hf_header_cache WHERE repo = ? AND filename = ? AND sha = ?`,
		repo, filename, sha).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: header cache: %w", err)
	}
	return []byte(body), true, nil
}

// PutCachedHeader stores a parsed header under (repo, filename, sha).
func (s *Store) PutCachedHeader(ctx context.Context, repo, filename, sha string, header []byte, bytesRead int64) error {
	if sha == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO hf_header_cache (created_at, repo, filename, sha, header_json, bytes_read)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (repo, filename, sha) DO UPDATE SET header_json = excluded.header_json, bytes_read = excluded.bytes_read`,
		Now(), repo, filename, sha, string(header), bytesRead)
	if err != nil {
		return fmt.Errorf("store: header cache: %w", err)
	}
	return nil
}

// CachedListing returns the last model-info answer for repo and its ETag.
func (s *Store) CachedListing(ctx context.Context, repo string) (etag string, body []byte, ok bool, err error) {
	var b string
	err = s.db.QueryRowContext(ctx, `SELECT etag, body FROM hf_listing_cache WHERE repo = ?`, repo).Scan(&etag, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, fmt.Errorf("store: listing cache: %w", err)
	}
	return etag, []byte(b), true, nil
}

// PutCachedListing stores repo's model-info answer and its ETag.
func (s *Store) PutCachedListing(ctx context.Context, repo, etag string, body []byte) error {
	now := Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO hf_listing_cache (created_at, repo, etag, body, fetched_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (repo) DO UPDATE SET etag = excluded.etag, body = excluded.body, fetched_at = excluded.fetched_at`,
		now, repo, etag, string(body), now)
	if err != nil {
		return fmt.Errorf("store: listing cache: %w", err)
	}
	return nil
}

// CatalogRefreshRow is one stored refresh.
type CatalogRefreshRow struct {
	ID         int64
	StartedAt  string
	FinishedAt string
	Trigger    string
	Sizes      int
	Resolved   int
	Report     []byte // the refresh's report as JSON
}

// AddCatalogRefresh records a finished refresh.
func (s *Store) AddCatalogRefresh(ctx context.Context, r CatalogRefreshRow) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO catalog_refreshes (created_at, finished_at, trigger, sizes, resolved, report_json)
		VALUES (?, ?, ?, ?, ?, ?)`, r.StartedAt, r.FinishedAt, r.Trigger, r.Sizes, r.Resolved, string(r.Report))
	if err != nil {
		return 0, fmt.Errorf("store: recording a catalogue refresh: %w", err)
	}
	return res.LastInsertId()
}

// LatestCatalogRefresh is the most recent refresh, or ErrNotFound.
func (s *Store) LatestCatalogRefresh(ctx context.Context) (CatalogRefreshRow, error) {
	var r CatalogRefreshRow
	var report string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, created_at, finished_at, trigger, sizes, resolved, report_json
		FROM catalog_refreshes ORDER BY id DESC LIMIT 1`).Scan(
		&r.ID, &r.StartedAt, &r.FinishedAt, &r.Trigger, &r.Sizes, &r.Resolved, &report)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, fmt.Errorf("store: latest catalogue refresh: %w", err)
	}
	r.Report = []byte(report)
	return r, nil
}

// InstalledMatch is how one installed_models row maps onto the catalogue.
type InstalledMatch struct {
	InstalledID int64
	ModelID     int64 // 0 = none (NULL)
	FileID      int64 // 0 = none (NULL)
	Kind        string
	Note        string
}

// SetInstalledModelMatches stores the catalogue mapping of installed models.
func (s *Store) SetInstalledModelMatches(ctx context.Context, matches []InstalledMatch) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: installed model matches: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	null := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: v != 0} }
	for _, m := range matches {
		if _, err := tx.ExecContext(ctx, `
			UPDATE installed_models SET catalog_model_id = ?, catalog_file_id = ?, catalog_match = ?, catalog_note = ?
			WHERE id = ?`, null(m.ModelID), null(m.FileID), m.Kind, m.Note, m.InstalledID); err != nil {
			return fmt.Errorf("store: installed model %d match: %w", m.InstalledID, err)
		}
	}
	return tx.Commit()
}

// UnknownInstalledModels are the present installed models the catalogue
// does not know — the curator's signal (build-plan step 4, item 4).
func (s *Store) UnknownInstalledModels(ctx context.Context) ([]InstalledModelRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+installedModelColumns+` FROM installed_models
		 WHERE present = 1 AND catalog_match = 'unknown' ORDER BY backend_name, name`)
	if err != nil {
		return nil, fmt.Errorf("store: unknown installed models: %w", err)
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

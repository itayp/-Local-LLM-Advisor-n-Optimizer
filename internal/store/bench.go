package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// BenchRunRow is one benchmark_runs row (build-plan step 6): a run with the
// whole context that makes it comparable — hardware, runtime and version,
// the path the runtime took, the model and its digest, the context, the
// cache and attention settings, the suite and daemon versions. The JSON
// columns hold the structs internal/bench owns (Config, results, the model
// facts, the estimate the run was compared with, notes); store keeps them
// as text and does not look inside.
//
// Zero means unknown for the numeric pointer fields (nil = NULL), and for
// the ids (0 = NULL): an installed model the catalogue does not know has no
// catalog_file_id, and a run is still a run.
type BenchRunRow struct {
	ID                  int64
	CreatedAt           string
	Status              string
	HardwareProfileID   int64
	HardwareFingerprint string
	BackendName         string
	BackendVersion      string
	RuntimePath         string // cuda | metal | rocm | vulkan | cpu | unknown
	ModelName           string
	ModelDigest         string
	InstalledModelID    int64 // 0 = NULL
	CatalogFileID       int64 // 0 = NULL
	Quantization        string
	WeightsBytes        uint64
	NumCtx              int
	EffectiveCtx        *int
	KVCacheType         string // f16 | q8_0 | q4_0 | unknown
	FlashAttention      bool
	FlashAttentionKnown bool
	SuiteVersion        string
	DaemonVersion       string
	ConfigKey           string
	StartedAt           string
	FinishedAt          string // "" while running

	ConfigJSON   string
	ResultsJSON  string
	ModelJSON    string
	EstimateJSON string
	NotesJSON    string

	GenTPSMedian    *float64
	PromptTPSMedian *float64
	TTFTMsMedian    *float64
	LoadMs          *float64

	PeakVRAMBytes   *uint64 // the rise over the reading before the load
	PeakRAMBytes    *uint64 // system memory in use at its peak
	PsSizeBytes     *uint64
	PsSizeVRAMBytes *uint64
	Resident        string // gpu | split | cpu | unknown
	MemorySource    string
	SamplerNote     string
	Unloaded        *bool
	MeasureAnyway   bool
	Error           string
}

const benchRunColumns = `id, created_at, status, hardware_profile_id, hardware_fingerprint, backend_name, backend_version,
	runtime_path, model_name, model_digest, installed_model_id, catalog_file_id, quantization, weights_bytes,
	num_ctx, effective_ctx, kv_cache_type, flash_attention, flash_attention_known, suite_version, daemon_version,
	config_key, started_at, finished_at, config_json, results_json, model_json, estimate_json, notes_json,
	gen_tps_median, prompt_tps_median, ttft_ms_median, load_ms, peak_vram_bytes, peak_ram_bytes,
	ps_size_bytes, ps_size_vram_bytes, resident, memory_source, sampler_note, unloaded, measure_anyway, error`

// InsertBenchRun stores a new run and returns its id. CreatedAt and
// StartedAt default to now.
func (s *Store) InsertBenchRun(ctx context.Context, r BenchRunRow) (int64, error) {
	if r.CreatedAt == "" {
		r.CreatedAt = Now()
	}
	if r.StartedAt == "" {
		r.StartedAt = r.CreatedAt
	}
	args := benchRunArgs(r)
	res, err := s.db.ExecContext(ctx, `INSERT INTO benchmark_runs (`+strings.TrimPrefix(benchRunColumns, "id, ")+`)
		VALUES (`+placeholders(len(args))+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("store: inserting benchmark run for %s: %w", r.ModelName, err)
	}
	return res.LastInsertId()
}

// UpdateBenchRun rewrites every column of run r.ID. A run's row is written
// when it starts and again as it progresses and ends; nothing else updates
// it.
func (s *Store) UpdateBenchRun(ctx context.Context, r BenchRunRow) error {
	if r.ID == 0 {
		return errors.New("store: UpdateBenchRun needs an id")
	}
	cols := strings.Split(strings.TrimPrefix(benchRunColumns, "id, "), ",")
	set := make([]string, len(cols))
	for i, c := range cols {
		set[i] = strings.TrimSpace(c) + " = ?"
	}
	args := append(benchRunArgs(r), r.ID)
	res, err := s.db.ExecContext(ctx, `UPDATE benchmark_runs SET `+strings.Join(set, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("store: updating benchmark run %d: %w", r.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func benchRunArgs(r BenchRunRow) []any {
	finished := sql.NullString{String: r.FinishedAt, Valid: r.FinishedAt != ""}
	return []any{
		r.CreatedAt, r.Status, r.HardwareProfileID, r.HardwareFingerprint, r.BackendName, r.BackendVersion,
		orDefault(r.RuntimePath, "unknown"), r.ModelName, r.ModelDigest, nullID(r.InstalledModelID), nullID(r.CatalogFileID),
		r.Quantization, int64(r.WeightsBytes),
		r.NumCtx, nullInt(r.EffectiveCtx), orDefault(r.KVCacheType, "unknown"), r.FlashAttention, r.FlashAttentionKnown,
		r.SuiteVersion, r.DaemonVersion,
		r.ConfigKey, r.StartedAt, finished, orDefault(r.ConfigJSON, "{}"), orDefault(r.ResultsJSON, "[]"),
		orDefault(r.ModelJSON, "{}"), r.EstimateJSON, orDefault(r.NotesJSON, "[]"),
		nullFloat(r.GenTPSMedian), nullFloat(r.PromptTPSMedian), nullFloat(r.TTFTMsMedian), nullFloat(r.LoadMs),
		nullUint(r.PeakVRAMBytes), nullUint(r.PeakRAMBytes),
		nullUint(r.PsSizeBytes), nullUint(r.PsSizeVRAMBytes), orDefault(r.Resident, "unknown"), r.MemorySource, r.SamplerNote,
		nullBoolPtr(r.Unloaded), r.MeasureAnyway, r.Error,
	}
}

func scanBenchRun(sc interface{ Scan(...any) error }) (BenchRunRow, error) {
	var r BenchRunRow
	var installed, catalogFile, effective sql.NullInt64
	var finished sql.NullString
	var weights int64
	var gen, prompt, ttft, load sql.NullFloat64
	var peakVRAM, peakRAM, psSize, psVRAM sql.NullInt64
	var unloaded sql.NullBool
	if err := sc.Scan(&r.ID, &r.CreatedAt, &r.Status, &r.HardwareProfileID, &r.HardwareFingerprint, &r.BackendName,
		&r.BackendVersion, &r.RuntimePath, &r.ModelName, &r.ModelDigest, &installed, &catalogFile, &r.Quantization,
		&weights, &r.NumCtx, &effective, &r.KVCacheType, &r.FlashAttention, &r.FlashAttentionKnown, &r.SuiteVersion,
		&r.DaemonVersion, &r.ConfigKey, &r.StartedAt, &finished, &r.ConfigJSON, &r.ResultsJSON, &r.ModelJSON,
		&r.EstimateJSON, &r.NotesJSON, &gen, &prompt, &ttft, &load, &peakVRAM, &peakRAM, &psSize, &psVRAM,
		&r.Resident, &r.MemorySource, &r.SamplerNote, &unloaded, &r.MeasureAnyway, &r.Error); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, ErrNotFound
		}
		return r, err
	}
	r.InstalledModelID, r.CatalogFileID = installed.Int64, catalogFile.Int64
	if effective.Valid {
		v := int(effective.Int64)
		r.EffectiveCtx = &v
	}
	r.FinishedAt = finished.String
	r.WeightsBytes = uint64(weights)
	r.GenTPSMedian, r.PromptTPSMedian, r.TTFTMsMedian, r.LoadMs = floatPtr(gen), floatPtr(prompt), floatPtr(ttft), floatPtr(load)
	r.PeakVRAMBytes, r.PeakRAMBytes, r.PsSizeBytes, r.PsSizeVRAMBytes = uintPtr(peakVRAM), uintPtr(peakRAM), uintPtr(psSize), uintPtr(psVRAM)
	if unloaded.Valid {
		v := unloaded.Bool
		r.Unloaded = &v
	}
	return r, nil
}

// BenchRun returns one run, or ErrNotFound.
func (s *Store) BenchRun(ctx context.Context, id int64) (BenchRunRow, error) {
	return scanBenchRun(s.db.QueryRowContext(ctx, `SELECT `+benchRunColumns+` FROM benchmark_runs WHERE id = ?`, id))
}

// BenchRunFilter narrows BenchRuns. Zero values do not filter.
type BenchRunFilter struct {
	ModelName           string
	HardwareFingerprint string
	BackendName         string
	ConfigKey           string
	Status              string
	BeforeID            int64 // only runs with a smaller id
	Limit               int   // default 100
}

// BenchRuns lists runs, newest first.
func (s *Store) BenchRuns(ctx context.Context, f BenchRunFilter) ([]BenchRunRow, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		where = append(where, cond)
		args = append(args, v)
	}
	if f.ModelName != "" {
		add("model_name = ?", f.ModelName)
	}
	if f.HardwareFingerprint != "" {
		add("hardware_fingerprint = ?", f.HardwareFingerprint)
	}
	if f.BackendName != "" {
		add("backend_name = ?", f.BackendName)
	}
	if f.ConfigKey != "" {
		add("config_key = ?", f.ConfigKey)
	}
	if f.Status != "" {
		add("status = ?", f.Status)
	}
	if f.BeforeID > 0 {
		add("id < ?", f.BeforeID)
	}
	q := `SELECT ` + benchRunColumns + ` FROM benchmark_runs`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q += fmt.Sprintf(` ORDER BY id DESC LIMIT %d`, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: benchmark runs: %w", err)
	}
	defer rows.Close()
	var out []BenchRunRow
	for rows.Next() {
		r, err := scanBenchRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InterruptBenchRuns marks every run still recorded as queued or running as
// failed with msg, and returns how many it changed. The daemon calls it on
// start: a run it was doing when it stopped did not finish, and a row that
// says "running" forever would be a lie.
func (s *Store) InterruptBenchRuns(ctx context.Context, msg string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE benchmark_runs SET status = 'failed', error = ?, finished_at = ?
		WHERE status IN ('queued', 'running')`, msg, Now())
	if err != nil {
		return 0, fmt.Errorf("store: closing interrupted benchmark runs: %w", err)
	}
	return res.RowsAffected()
}

// BenchSampleRow is one benchmark_samples row: one reading of one tool for
// one device, taken once a second during a run. Nil = the tool gives no
// such value.
type BenchSampleRow struct {
	RunID     int64
	SampledAt string
	Tool      string
	Device    string
	GPUUtil   *float64
	VRAMUsed  *uint64
	RAMUsed   *uint64
	TempC     *float64
	PowerW    *float64
}

// AddBenchSamples stores samples in one transaction.
func (s *Store) AddBenchSamples(ctx context.Context, samples []BenchSampleRow) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: benchmark samples: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	for _, x := range samples {
		if _, err := tx.ExecContext(ctx, `INSERT INTO benchmark_samples
			(run_id, sampled_at, tool, device, gpu_util_pct, vram_used_bytes, ram_used_bytes, temp_c, power_w)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			x.RunID, x.SampledAt, x.Tool, x.Device, nullFloat(x.GPUUtil), nullUint(x.VRAMUsed), nullUint(x.RAMUsed),
			nullFloat(x.TempC), nullFloat(x.PowerW)); err != nil {
			return fmt.Errorf("store: benchmark sample for run %d: %w", x.RunID, err)
		}
	}
	return tx.Commit()
}

// BenchSamples returns a run's samples in the order they were taken.
func (s *Store) BenchSamples(ctx context.Context, runID int64) ([]BenchSampleRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id, sampled_at, tool, device, gpu_util_pct, vram_used_bytes,
		ram_used_bytes, temp_c, power_w FROM benchmark_samples WHERE run_id = ? ORDER BY sampled_at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: benchmark samples for run %d: %w", runID, err)
	}
	defer rows.Close()
	var out []BenchSampleRow
	for rows.Next() {
		var x BenchSampleRow
		var util, temp, power sql.NullFloat64
		var vram, ram sql.NullInt64
		if err := rows.Scan(&x.RunID, &x.SampledAt, &x.Tool, &x.Device, &util, &vram, &ram, &temp, &power); err != nil {
			return nil, err
		}
		x.GPUUtil, x.TempC, x.PowerW = floatPtr(util), floatPtr(temp), floatPtr(power)
		x.VRAMUsed, x.RAMUsed = uintPtr(vram), uintPtr(ram)
		out = append(out, x)
	}
	return out, rows.Err()
}

// MeasuredEstimateRow is an estimates row whose source is 'measured': the
// estimate of one configuration on one hardware profile, replaced by what a
// benchmark of that configuration measured (product rule 4, second
// sentence; ARCHITECTURE.md D-13, D-43). The terms the estimator computed
// stay beside the measured values.
type MeasuredEstimateRow struct {
	HardwareProfileID int64
	CatalogFileID     int64
	NumCtx            int
	EffectiveCtx      int
	KVCacheType       string
	RuntimePath       string
	WeightsBytes      uint64
	KVBytes           uint64
	OverheadBytes     uint64
	TotalBytes        uint64 // measured when the run measured it, else the estimate
	GPUResidentBytes  uint64
	CPUOffloadBytes   uint64
	BudgetBytes       uint64
	Category          string
	Threshold         string
	GenTPS            float64 // the measurement: low = high
	PromptTPS         *float64
	MeasuredRunID     int64
	NotesJSON         string
}

// UpsertMeasuredEstimate inserts the row, or turns the existing estimate of
// the same configuration into this measurement. The unique key is the
// schema's: (hardware_profile_id, catalog_file_id, num_ctx, kv_cache_type,
// runtime_path).
func (s *Store) UpsertMeasuredEstimate(ctx context.Context, r MeasuredEstimateRow) error {
	now := Now()
	var prompt sql.NullFloat64
	if r.PromptTPS != nil {
		prompt = sql.NullFloat64{Float64: *r.PromptTPS, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO estimates
		(created_at, updated_at, hardware_profile_id, catalog_file_id, num_ctx, effective_ctx, kv_cache_type,
		 runtime_path, weights_bytes, kv_bytes, overhead_bytes, total_bytes, gpu_resident_bytes, cpu_offload_bytes,
		 budget_bytes, category, threshold, speed_known, gen_tps_low, gen_tps_high, prompt_tps_low, prompt_tps_high,
		 source, measured_run_id, notes_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, 'measured', ?, ?)
		ON CONFLICT (hardware_profile_id, catalog_file_id, num_ctx, kv_cache_type, runtime_path) DO UPDATE SET
			updated_at = excluded.updated_at, effective_ctx = excluded.effective_ctx,
			weights_bytes = excluded.weights_bytes, kv_bytes = excluded.kv_bytes, overhead_bytes = excluded.overhead_bytes,
			total_bytes = excluded.total_bytes, gpu_resident_bytes = excluded.gpu_resident_bytes,
			cpu_offload_bytes = excluded.cpu_offload_bytes, budget_bytes = excluded.budget_bytes,
			category = excluded.category, threshold = excluded.threshold, speed_known = 1,
			gen_tps_low = excluded.gen_tps_low, gen_tps_high = excluded.gen_tps_high,
			prompt_tps_low = excluded.prompt_tps_low, prompt_tps_high = excluded.prompt_tps_high,
			source = 'measured', measured_run_id = excluded.measured_run_id, notes_json = excluded.notes_json`,
		now, now, r.HardwareProfileID, r.CatalogFileID, r.NumCtx, r.EffectiveCtx, orDefault(r.KVCacheType, "f16"),
		r.RuntimePath, int64(r.WeightsBytes), int64(r.KVBytes), int64(r.OverheadBytes), int64(r.TotalBytes),
		int64(r.GPUResidentBytes), int64(r.CPUOffloadBytes), int64(r.BudgetBytes), r.Category, r.Threshold,
		r.GenTPS, r.GenTPS, prompt, prompt, r.MeasuredRunID, orDefault(r.NotesJSON, "[]"))
	if err != nil {
		return fmt.Errorf("store: recording the measured estimate of file %d at %d: %w", r.CatalogFileID, r.NumCtx, err)
	}
	return nil
}

// MeasuredEstimate is one measured configuration as the engine reads it
// back: which file, at which context, with which cache and on which path,
// and which run measured it.
type MeasuredEstimate struct {
	CatalogFileID int64
	NumCtx        int
	KVCacheType   string
	RuntimePath   string
	MeasuredRunID int64
	UpdatedAt     string
}

// MeasuredEstimates returns, for the hardware with this fingerprint, the
// latest measurement of every configuration — across every daemon start on
// that hardware, because a profile id is per start and a measurement is
// about the hardware.
func (s *Store) MeasuredEstimates(ctx context.Context, fingerprint string) ([]MeasuredEstimate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.catalog_file_id, e.num_ctx, e.kv_cache_type, e.runtime_path, e.measured_run_id, e.updated_at
		FROM estimates e JOIN hardware_profiles h ON h.id = e.hardware_profile_id
		WHERE e.source = 'measured' AND e.measured_run_id IS NOT NULL AND h.fingerprint = ?
		ORDER BY e.updated_at, e.id`, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("store: measured estimates: %w", err)
	}
	defer rows.Close()
	latest := map[[4]string]MeasuredEstimate{}
	var order [][4]string
	for rows.Next() {
		var m MeasuredEstimate
		if err := rows.Scan(&m.CatalogFileID, &m.NumCtx, &m.KVCacheType, &m.RuntimePath, &m.MeasuredRunID, &m.UpdatedAt); err != nil {
			return nil, err
		}
		k := [4]string{fmt.Sprint(m.CatalogFileID), fmt.Sprint(m.NumCtx), m.KVCacheType, m.RuntimePath}
		if _, seen := latest[k]; !seen {
			order = append(order, k)
		}
		latest[k] = m // later rows win: ordered by updated_at
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]MeasuredEstimate, 0, len(order))
	for _, k := range order {
		out = append(out, latest[k])
	}
	return out, nil
}

// ---- NULL helpers ------------------------------------------------------------

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func nullID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id != 0} }

func nullInt(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

func nullFloat(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

func nullUint(v *uint64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

func nullBoolPtr(v *bool) sql.NullBool {
	if v == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *v, Valid: true}
}

func floatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func uintPtr(v sql.NullInt64) *uint64 {
	if !v.Valid || v.Int64 < 0 {
		return nil
	}
	u := uint64(v.Int64)
	return &u
}

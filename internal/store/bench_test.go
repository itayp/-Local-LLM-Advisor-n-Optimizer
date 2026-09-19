package store

import (
	"context"
	"errors"
	"testing"
)

func f64(v float64) *float64 { return &v }
func u64(v uint64) *uint64   { return &v }

// A run's row round-trips every column, unknown values as NULL; samples
// belong to their run; the list filters and orders newest first.
func TestBenchRunsRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	hw, err := s.AddHardwareProfile(ctx, sampleProfile("NVIDIA GeForce RTX 5070 Ti", "10de:2c05", 16303<<20), "v1")
	if err != nil {
		t.Fatal(err)
	}
	row := BenchRunRow{Status: "running", HardwareProfileID: hw.ID, HardwareFingerprint: hw.Fingerprint, BackendName: "ollama",
		BackendVersion: "0.34.2", ModelName: "llama3.1:8b", ModelDigest: "sha256:46e0", Quantization: "Q4_K_M",
		WeightsBytes: 4_920_753_328, NumCtx: 8192, KVCacheType: "unknown", SuiteVersion: "1", DaemonVersion: "dev",
		ConfigKey: "k1", ConfigJSON: `{"model":"llama3.1:8b"}`}
	id, err := s.InsertBenchRun(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.BenchRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RuntimePath != "unknown" || got.KVCacheType != "unknown" || got.GenTPSMedian != nil || got.PeakVRAMBytes != nil ||
		got.Unloaded != nil || got.EffectiveCtx != nil || got.FinishedAt != "" || got.InstalledModelID != 0 || got.Resident != "unknown" {
		t.Fatalf("unknowns must read back as unknown: %+v", got)
	}

	eff, unloaded := 8192, true
	got.Status, got.RuntimePath, got.KVCacheType, got.FlashAttention, got.FlashAttentionKnown = "done", "cuda", "f16", true, true
	got.EffectiveCtx, got.FinishedAt, got.GenTPSMedian, got.PromptTPSMedian = &eff, Now(), f64(121.2), f64(3000)
	got.PeakVRAMBytes, got.PsSizeBytes, got.PsSizeVRAMBytes, got.Resident, got.Unloaded = u64(5_750_000_000), u64(5_463_000_000), u64(5_463_000_000), "gpu", &unloaded
	got.ResultsJSON = `[{"prompt":"500"}]`
	if err := s.UpdateBenchRun(ctx, got); err != nil {
		t.Fatal(err)
	}
	back, _ := s.BenchRun(ctx, id)
	if back.Status != "done" || *back.GenTPSMedian != 121.2 || *back.PeakVRAMBytes != 5_750_000_000 || !*back.Unloaded ||
		*back.EffectiveCtx != 8192 || !back.FlashAttentionKnown || back.ResultsJSON != `[{"prompt":"500"}]` || back.Resident != "gpu" {
		t.Fatalf("after update: %+v", back)
	}
	if err := s.UpdateBenchRun(ctx, BenchRunRow{ID: 9999, Status: "done"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("updating a missing run: %v", err)
	}

	if err := s.AddBenchSamples(ctx, []BenchSampleRow{
		{RunID: id, SampledAt: "2026-09-19T12:00:01Z", Tool: "nvidia-smi", Device: "0", GPUUtil: f64(96), VRAMUsed: u64(6 << 30)},
		{RunID: id, SampledAt: "2026-09-19T12:00:01Z", Tool: "os", RAMUsed: u64(12 << 30)},
	}); err != nil {
		t.Fatal(err)
	}
	samples, err := s.BenchSamples(ctx, id)
	if err != nil || len(samples) != 2 || samples[0].Device != "0" || *samples[0].GPUUtil != 96 || samples[1].GPUUtil != nil || *samples[1].RAMUsed != 12<<30 {
		t.Fatalf("samples %+v %v", samples, err)
	}

	id2, _ := s.InsertBenchRun(ctx, BenchRunRow{Status: "running", HardwareProfileID: hw.ID, BackendName: "ollama",
		ModelName: "qwen3:4b", NumCtx: 4096, ConfigKey: "k2"})
	runs, err := s.BenchRuns(ctx, BenchRunFilter{})
	if err != nil || len(runs) != 2 || runs[0].ID != id2 {
		t.Fatalf("newest first: %+v %v", runs, err)
	}
	if runs, _ := s.BenchRuns(ctx, BenchRunFilter{ConfigKey: "k1", Status: "done"}); len(runs) != 1 || runs[0].ID != id {
		t.Fatalf("by key: %+v", runs)
	}
	if runs, _ := s.BenchRuns(ctx, BenchRunFilter{ModelName: "llama3.1:8b", BeforeID: id}); len(runs) != 0 {
		t.Fatalf("before: %+v", runs)
	}
	// The daemon stopped while the second ran.
	if n, err := s.InterruptBenchRuns(ctx, "stopped"); err != nil || n != 1 {
		t.Fatalf("interrupted %d %v", n, err)
	}
	if r, _ := s.BenchRun(ctx, id2); r.Status != "failed" || r.Error != "stopped" || r.FinishedAt == "" {
		t.Fatalf("interrupted run %+v", r)
	}
}

// A measurement replaces the estimate of its configuration, and is found
// again from any later daemon start on the same hardware — never from
// another's.
func TestMeasuredEstimatesFollowTheHardware(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	p := sampleProfile("NVIDIA GeForce RTX 5070 Ti", "10de:2c05", 16303<<20)
	start1, _ := s.AddHardwareProfile(ctx, p, "v1")
	start2, _ := s.AddHardwareProfile(ctx, p, "v1")
	other, _ := s.AddHardwareProfile(ctx, sampleProfile("NVIDIA GeForce RTX 3080", "10de:2206", 10240<<20), "v1")
	fileID := seedCatalogFile(t, s)

	run := func(hwID int64) int64 {
		id, err := s.InsertBenchRun(ctx, BenchRunRow{Status: "done", HardwareProfileID: hwID, BackendName: "ollama", ModelName: "m", NumCtx: 8192})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	r1, r2 := run(start1.ID), run(start2.ID)
	put := func(hwID, runID int64, gen float64) {
		if err := s.UpsertMeasuredEstimate(ctx, MeasuredEstimateRow{HardwareProfileID: hwID, CatalogFileID: fileID, NumCtx: 8192,
			EffectiveCtx: 8192, KVCacheType: "f16", RuntimePath: "cuda", TotalBytes: 5 << 30, Category: "fits_with_headroom",
			GenTPS: gen, MeasuredRunID: runID}); err != nil {
			t.Fatal(err)
		}
	}
	put(start1.ID, r1, 118)
	put(start1.ID, r1, 119) // the same configuration again: updated in place
	put(start2.ID, r2, 121) // a later start: the latest wins

	got, err := s.MeasuredEstimates(ctx, start1.Fingerprint)
	if err != nil || len(got) != 1 || got[0].MeasuredRunID != r2 || got[0].NumCtx != 8192 || got[0].RuntimePath != "cuda" {
		t.Fatalf("measured %+v %v", got, err)
	}
	var n int
	var source string
	var low, high float64
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM estimates WHERE hardware_profile_id = ?`, start1.ID).Scan(&n)
	_ = s.db.QueryRowContext(ctx, `SELECT source, gen_tps_low, gen_tps_high FROM estimates WHERE hardware_profile_id = ?`, start1.ID).Scan(&source, &low, &high)
	if n != 1 || source != "measured" || low != 119 || high != 119 {
		t.Fatalf("row: %d %s %v–%v", n, source, low, high)
	}
	if got, _ := s.MeasuredEstimates(ctx, other.Fingerprint); len(got) != 0 {
		t.Fatalf("another machine's measurement: %+v", got)
	}
}

// seedCatalogFile stores a one-file catalogue model and returns the file id.
func seedCatalogFile(t *testing.T, s *Store) int64 {
	t.Helper()
	ctx := context.Background()
	res, err := s.db.ExecContext(ctx, `INSERT INTO catalog_models (created_at, family_id, display_name, parameters, context_length)
		VALUES (?, 'llama3.1', 'Llama 3.1', 8000000000, 131072)`, Now())
	if err != nil {
		t.Fatal(err)
	}
	modelID, _ := res.LastInsertId()
	res, err = s.db.ExecContext(ctx, `INSERT INTO catalog_files (created_at, catalog_model_id, filename, quant, bytes, fetched_at)
		VALUES (?, ?, 'm.gguf', 'Q4_K_M', 4920753328, ?)`, Now(), modelID, Now())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

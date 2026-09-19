package bench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"advisor/internal/estimate"
	"advisor/internal/figure"
	"advisor/internal/hardware"
	"advisor/internal/store"
)

// Key is the comparability key: a digest of every field that decides
// whether two runs measured the same thing (ARCHITECTURE.md D-46). The
// hardware, the runtime and its version, the path it took, the model's
// exact file, the context, the cache, flash attention, the parallel slots
// and the suite. Not the daemon's version: what the harness does to time a
// request is part of the suite's version, which is bumped when it changes.
func (c RunConfig) Key() string {
	model := c.ModelDigest
	if model == "" {
		model = c.Model + "|" + c.Quantization + "|" + strconv.FormatUint(c.WeightsBytes, 10)
	}
	fa := "unknown"
	if c.FlashAttentionKnown {
		fa = strconv.FormatBool(c.FlashAttention)
	}
	parts := []string{
		"v1", c.HardwareFingerprint, c.Backend, c.BackendVersion, string(pathOf(c.RuntimePath)), model,
		strconv.Itoa(c.NumCtx), c.KVCacheType, fa, strconv.Itoa(c.Parallel), c.SuiteVersion, c.SuiteDigest,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:20]
}

// writeBack turns the estimate of the run's configuration into the
// measurement (product rule 4's second sentence; ARCHITECTURE.md D-48): the
// estimates row for (this hardware profile, this catalogue file, this
// context, this cache type, this path) is written with the measured rates,
// source 'measured', and the run's id. Only a configuration the estimator
// can be asked about is written: a catalogue file, a cache type and a path
// that were read, not assumed.
func (h *Harness) writeBack(ctx context.Context, s *runState) {
	c := s.run.Config
	why := ""
	kv := estimate.KVCacheType(c.KVCacheType)
	switch {
	case s.run.GenTPS == nil:
		return
	case c.CatalogFileID == 0:
		why = "the curated list does not know this model, so there is no estimate of it to replace; the measurement is kept, and it narrows the estimates of other models on this computer"
	case !kv.Valid():
		return // the note about the unknown cache is already on the run
	case !c.RuntimePath.UsesGPU() && c.RuntimePath != hardware.PathCPU:
		why = "the path the runtime took is not known, so which estimate this measurement replaces is not either"
	}
	if why != "" {
		s.run.Notes = append(s.run.Notes, why)
		return
	}
	if h.store == nil {
		return
	}
	// The estimate of exactly this configuration: on the path the runtime
	// took, with the cache it allocated — which is not necessarily what was
	// expected before the load.
	t := s.p.target
	m := t.Machine
	m.ActualPath = c.RuntimePath
	pl := t.Estimator.Place(m)
	est := t.Estimator.FitPlaced(pl, m, s.p.facts.Model(), estimate.Request{NumCtx: c.NumCtx, KVCacheType: kv})

	total := est.Memory.Total.Value
	if s.run.PeakVRAM != nil && s.run.Resident == ResidentGPU {
		total = s.run.PeakVRAM.Value
	}
	notes, _ := json.Marshal(nonNil(s.run.Notes))
	row := store.MeasuredEstimateRow{
		HardwareProfileID: c.HardwareProfileID, CatalogFileID: c.CatalogFileID, NumCtx: c.NumCtx,
		EffectiveCtx: est.Memory.EffectiveCtx, KVCacheType: string(kv), RuntimePath: string(c.RuntimePath),
		WeightsBytes: est.Memory.Weights.Value, KVBytes: est.Memory.KVCache.Value, OverheadBytes: est.Memory.Overhead.Value,
		TotalBytes: total, GPUResidentBytes: est.Memory.GPUResident.Value, CPUOffloadBytes: est.Memory.CPUOffload.Value,
		BudgetBytes: est.BudgetBytes, Category: string(est.Category), Threshold: est.Threshold,
		GenTPS: s.run.GenTPS.Value, MeasuredRunID: s.run.ID, NotesJSON: string(notes),
	}
	if s.run.PromptTPS != nil {
		v := s.run.PromptTPS.Value
		row.PromptTPS = &v
	}
	if err := h.store.UpsertMeasuredEstimate(ctx, row); err != nil {
		h.log.Error("writing a measurement back", "run", s.run.ID, "err", err)
		return
	}
	s.run.Replaced = true
}

// comparison finds the previous finished run of the same configuration.
func (h *Harness) comparison(ctx context.Context, run Run) (*Comparison, error) {
	if run.GenTPS == nil || run.ID == 0 {
		return nil, nil
	}
	rows, err := h.store.BenchRuns(ctx, store.BenchRunFilter{ConfigKey: run.Config.Key(), Status: string(StatusDone), BeforeID: run.ID, Limit: 1})
	if err != nil || len(rows) == 0 || rows[0].GenTPSMedian == nil || *rows[0].GenTPSMedian <= 0 {
		return nil, err
	}
	prev := *rows[0].GenTPSMedian
	return &Comparison{RunID: rows[0].ID, GenTPS: figure.MeasuredRate(prev, "tok/s"),
		DiffPct: math.Round(1000*(run.GenTPS.Value-prev)/prev) / 10}, nil
}

// Get returns a run: the one in progress as it stands, or a stored one.
// withSamples adds its resource readings.
func (h *Harness) Get(ctx context.Context, id int64, withSamples bool) (Run, error) {
	h.mu.Lock()
	a := h.active
	h.mu.Unlock()
	if a != nil && a.id == id {
		a.mu.Lock()
		run := a.run
		a.mu.Unlock()
		return run, nil
	}
	if h.store == nil {
		return Run{}, fail(ErrNotFound, "no test with that id")
	}
	row, err := h.store.BenchRun(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return Run{}, fail(ErrNotFound, "no test with that id")
	}
	if err != nil {
		return Run{}, err
	}
	run, err := runFromRow(row)
	if err != nil {
		return Run{}, err
	}
	if run.Status == StatusDone {
		run.Comparison, _ = h.comparison(ctx, run)
	}
	if withSamples {
		samples, err := h.store.BenchSamples(ctx, id)
		if err != nil {
			return Run{}, err
		}
		for _, x := range samples {
			at, _ := time.Parse(time.RFC3339Nano, x.SampledAt)
			run.Samples = append(run.Samples, Sample{At: at, Tool: x.Tool, Device: x.Device, GPUUtil: x.GPUUtil,
				VRAMUsed: x.VRAMUsed, RAMUsed: x.RAMUsed, TempC: x.TempC, PowerW: x.PowerW})
		}
	}
	return run, nil
}

// HistoryFilter narrows History.
type HistoryFilter struct {
	Model string
	Limit int
}

// History lists runs, newest first, each compared with the run of the same
// configuration before it.
func (h *Harness) History(ctx context.Context, f HistoryFilter) (History, error) {
	out := History{Runs: []Run{}}
	if h.store == nil {
		return out, nil
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := h.store.BenchRuns(ctx, store.BenchRunFilter{ModelName: f.Model, Limit: limit})
	if err != nil {
		return out, err
	}
	active := h.Active()
	for _, row := range rows {
		if row.ID == active {
			run, err := h.Get(ctx, row.ID, false)
			if err == nil {
				out.Runs = append(out.Runs, run)
				continue
			}
		}
		run, err := runFromRow(row)
		if err != nil {
			h.log.Warn("reading a stored benchmark run", "run", row.ID, "err", err)
			continue
		}
		if run.Status == StatusDone {
			run.Comparison, _ = h.comparison(ctx, run)
		}
		out.Runs = append(out.Runs, run)
	}
	return out, nil
}

// storedConfig is config_json: the run's configuration and the run-level
// facts that have no column of their own.
type storedConfig struct {
	RunConfig
	Request  Request      `json:"request"`
	Skipped  []Skipped    `json:"skipped,omitempty"`
	Load     *figure.Rate `json:"load,omitempty"`
	Expected *figure.Rate `json:"expected_duration,omitempty"`
	Replaced bool         `json:"replaced"`
	Headline string       `json:"headline,omitempty"`
	Power    *figure.Rate `json:"power,omitempty"`
	GPUUtil  *figure.Rate `json:"gpu_util,omitempty"`
	PeakTemp *figure.Rate `json:"peak_temp,omitempty"`
}

// runFromRow rebuilds a Run from its row.
func runFromRow(row store.BenchRunRow) (Run, error) {
	var sc storedConfig
	if err := json.Unmarshal([]byte(row.ConfigJSON), &sc); err != nil {
		return Run{}, fmt.Errorf("bench: run %d config: %w", row.ID, err)
	}
	run := Run{
		ID: row.ID, Status: Status(row.Status), Phase: PhaseFinished, Request: sc.Request, Config: sc.RunConfig,
		Results: []PromptResult{}, Skipped: sc.Skipped, Load: sc.Load, ExpectedDuration: sc.Expected, Replaced: sc.Replaced,
		Headline: sc.Headline, Power: sc.Power, GPUUtil: sc.GPUUtil, PeakTemp: sc.PeakTemp,
		Resident: Resident(row.Resident), MemorySource: row.MemorySource, SamplerNote: row.SamplerNote,
		Unloaded: row.Unloaded, Error: row.Error,
	}
	if !run.Status.Finished() {
		run.Phase = PhaseMeasuring
	}
	run.Config.fromRow(row)
	run.StartedAt, _ = time.Parse(time.RFC3339, row.StartedAt)
	if row.FinishedAt != "" {
		t, _ := time.Parse(time.RFC3339, row.FinishedAt)
		run.FinishedAt = &t
	}
	if err := json.Unmarshal([]byte(row.ResultsJSON), &run.Results); err != nil {
		return Run{}, fmt.Errorf("bench: run %d results: %w", row.ID, err)
	}
	if row.EstimateJSON != "" {
		var est estimate.Estimate
		if err := json.Unmarshal([]byte(row.EstimateJSON), &est); err == nil {
			run.Estimate = &est
		}
	}
	_ = json.Unmarshal([]byte(row.NotesJSON), &run.Notes)
	if head, ok := headlineResult(run.Results); ok {
		run.GenTPS, run.PromptTPS, run.TTFT = head.GenTPS, head.PromptTPS, head.TTFT
		if run.Headline == "" {
			run.Headline = head.Prompt
		}
	}
	if row.PeakVRAMBytes != nil {
		v := figure.MeasuredBytes(*row.PeakVRAMBytes)
		run.PeakVRAM = &v
	}
	if row.PeakRAMBytes != nil {
		v := figure.MeasuredBytes(*row.PeakRAMBytes)
		run.PeakRAM = &v
	}
	if row.PsSizeBytes != nil {
		run.RuntimeSizeBytes = *row.PsSizeBytes
	}
	if row.PsSizeVRAMBytes != nil {
		run.RuntimeSizeVRAMBytes = *row.PsSizeVRAMBytes
	}
	return run, nil
}

// fromRow takes the columns a row carries of the configuration, which are
// authoritative over config_json's copy.
func (c *RunConfig) fromRow(row store.BenchRunRow) {
	c.HardwareProfileID, c.HardwareFingerprint = row.HardwareProfileID, row.HardwareFingerprint
	c.RuntimePath = hardware.RuntimePath(row.RuntimePath)
	c.KVCacheType = row.KVCacheType
	c.FlashAttention, c.FlashAttentionKnown = row.FlashAttention, row.FlashAttentionKnown
}

// ---- evidence for the estimator and the recommendation engine ----------------------

// MeasuredConfig is a configuration a benchmark measured on this hardware:
// the key the engine looks its estimates up by, and the measurement that
// replaces them.
type MeasuredConfig struct {
	CatalogFileID int64
	NumCtx        int
	KVCacheType   estimate.KVCacheType
	Path          hardware.RuntimePath
	RunID         int64
	Measurement   estimate.Measurement
}

// Evidence is what this hardware's benchmarks give the estimator: exact
// measurements of configurations, and observations to calibrate the rest.
type Evidence struct {
	Measured     []MeasuredConfig
	Observations []estimate.Observation
}

// Evidence reads, for the hardware with this fingerprint and this runtime,
// the latest measurement of every measured configuration, and every
// finished run as an observation (newest first). An empty fingerprint —
// detection not finished — has none.
func (h *Harness) Evidence(ctx context.Context, fingerprint, backendName string) (Evidence, error) {
	var ev Evidence
	if h.store == nil || fingerprint == "" {
		return ev, nil
	}
	measured, err := h.store.MeasuredEstimates(ctx, fingerprint)
	if err != nil {
		return ev, err
	}
	for _, me := range measured {
		row, err := h.store.BenchRun(ctx, me.MeasuredRunID)
		if err != nil || row.BackendName != backendName || row.GenTPSMedian == nil {
			continue
		}
		m := estimate.Measurement{GenerationTPS: *row.GenTPSMedian}
		if row.PromptTPSMedian != nil {
			m.PromptTPS = *row.PromptTPSMedian
		}
		if row.PeakVRAMBytes != nil && row.Resident == string(ResidentGPU) {
			m.PeakBytes = *row.PeakVRAMBytes
		}
		ev.Measured = append(ev.Measured, MeasuredConfig{CatalogFileID: me.CatalogFileID, NumCtx: me.NumCtx,
			KVCacheType: estimate.KVCacheType(me.KVCacheType), Path: hardware.RuntimePath(me.RuntimePath), RunID: me.MeasuredRunID, Measurement: m})
	}

	rows, err := h.store.BenchRuns(ctx, store.BenchRunFilter{HardwareFingerprint: fingerprint, BackendName: backendName,
		Status: string(StatusDone), Limit: 200})
	if err != nil {
		return ev, err
	}
	for _, row := range rows {
		path := hardware.RuntimePath(row.RuntimePath)
		if row.GenTPSMedian == nil || !(path.UsesGPU() || path == hardware.PathCPU) {
			continue
		}
		var facts ModelFacts
		if err := json.Unmarshal([]byte(row.ModelJSON), &facts); err != nil || facts.WeightsBytes == 0 {
			continue
		}
		var results []PromptResult
		if err := json.Unmarshal([]byte(row.ResultsJSON), &results); err != nil {
			continue
		}
		head, ok := headlineResult(results)
		if !ok {
			continue
		}
		o := estimate.Observation{
			Label: row.ModelName, Model: facts.Model(), Path: path,
			Resident:      (path.UsesGPU() && row.Resident == string(ResidentGPU)) || (path == hardware.PathCPU && row.Resident == string(ResidentCPU)),
			ContextTokens: head.PromptTokens + head.GenTokens/2, KVCacheType: estimate.KVCacheType(row.KVCacheType),
			GenerationTPS: head.GenTPS.Value,
		}
		if head.PromptTPS != nil {
			o.PromptTPS = head.PromptTPS.Value
		}
		ev.Observations = append(ev.Observations, o)
	}
	return ev, nil
}

// Package bench is the local benchmark harness. Step 1: types only. Step 6
// implements the suite, the runner, the resource sampler and the write-back
// that turns an estimate into a measurement.
//
// Everything a benchmark produces is a measurement — Source is Measured on
// every figure here — and every row stores the whole context (profile,
// backend + version, runtime path, model + quant + bytes, num_ctx, KV cache
// type, flash attention, suite version, daemon version). Nothing is
// comparable without all of it (PRD §21, first risk).
//
// The prompts are the suite's own text (data/bench/), never anything the
// user typed (product rule 7).
package bench

import (
	"time"

	"advisor/internal/estimate"
	"advisor/internal/figure"
	"advisor/internal/hardware"
)

// Status of a run.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusCancelled Status = "cancelled"
	StatusFailed    Status = "failed"
)

// Request starts a run (POST /api/bench).
type Request struct {
	ModelName   string               `json:"model_name"`
	NumCtx      int                  `json:"num_ctx" source:"n/a"` // configuration
	KVCacheType estimate.KVCacheType `json:"kv_cache_type"`
	// MeasureAnyway overrides the refusal to benchmark a configuration the
	// estimator says will spill to CPU (step 6, item 5).
	MeasureAnyway bool `json:"measure_anyway"`
	// PromptsOnly narrows the suite (the onboarding "Try it" runs the
	// 500-token prompt alone). Empty means the whole suite.
	PromptsOnly []string `json:"prompts_only,omitempty"`
}

// Config is the configuration a run was made with — the comparability key.
type Config struct {
	HardwareProfileID int64                `json:"hardware_profile_id" source:"n/a"`
	Backend           string               `json:"backend"`
	BackendVersion    string               `json:"backend_version"`
	RuntimePath       hardware.RuntimePath `json:"runtime_path"`
	ModelName         string               `json:"model_name"`
	Quantization      string               `json:"quantization"`
	WeightsBytes      uint64               `json:"weights_bytes" source:"n/a"` // the blob size, a fact
	NumCtx            int                  `json:"num_ctx" source:"n/a"`
	KVCacheType       estimate.KVCacheType `json:"kv_cache_type"`
	FlashAttention    bool                 `json:"flash_attention"`
	SuiteVersion      string               `json:"suite_version"`
	DaemonVersion     string               `json:"daemon_version"`
}

// PromptResult is the median and spread over N timed runs of one prompt.
type PromptResult struct {
	Prompt       string      `json:"prompt"`                     // the suite's prompt id, e.g. "500"
	PromptTokens int         `json:"prompt_tokens" source:"n/a"` // a count
	PromptTPS    figure.Rate `json:"prompt_tps"`                 // prompt_eval_count / prompt_eval_duration
	GenTPS       figure.Rate `json:"generation_tps"`             // eval_count / eval_duration
	TTFT         figure.Rate `json:"ttft"`                       // ms to the first streamed token, measured at the client
	Load         figure.Rate `json:"load"`                       // ms, load_duration
	SpreadPct    float64     `json:"spread_pct" source:"n/a"`    // (max−min)/median over the timed runs; a statistic about the measurement, shown under Advanced
	Runs         int         `json:"runs" source:"n/a"`          // a count
}

// Run is one benchmark_runs row and the payload of GET /api/bench/{id}.
type Run struct {
	ID         int64          `json:"id" source:"n/a"`
	Status     Status         `json:"status"`
	Config     Config         `json:"config"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Results    []PromptResult `json:"results"`
	Error      string         `json:"error,omitempty"`
	// Peak resource figures over the run, from the sampler. Absent (nil)
	// where no tool existed to read them — the row says so rather than
	// showing zero.
	PeakVRAM *figure.Bytes `json:"peak_vram,omitempty"`
	PeakRAM  *figure.Bytes `json:"peak_ram,omitempty"`
	// SamplerNote explains an absent sampler: "no VRAM reading is available
	// for AMD on Windows".
	SamplerNote string `json:"sampler_note,omitempty"`
}

// Sample is one 1 Hz resource reading (benchmark_samples).
type Sample struct {
	RunID    int64     `json:"run_id" source:"n/a"`
	At       time.Time `json:"at"`
	Tool     string    `json:"tool"`                                   // nvidia-smi, rocm-smi, amd-smi, sysfs, api/ps, os
	GPUUtil  *float64  `json:"gpu_util_pct,omitempty" source:"n/a"`    // raw sampler readings; the figures the user sees are the peaks on Run
	VRAMUsed *uint64   `json:"vram_used_bytes,omitempty" source:"n/a"` //
	RAMUsed  *uint64   `json:"ram_used_bytes,omitempty" source:"n/a"`  //
	TempC    *float64  `json:"temp_c,omitempty" source:"n/a"`          //
	PowerW   *float64  `json:"power_w,omitempty" source:"n/a"`         //
}

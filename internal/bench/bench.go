// Package bench is the local benchmark harness (build-plan step 6,
// ARCHITECTURE.md D-44..D-49): it runs a fixed suite of prompts through the
// runtime, times them with the runtime's own counters, samples the machine
// once a second while it does, stores every run with the whole context that
// makes it comparable, and writes the measurement back so the estimate of
// that configuration is replaced by it (product rule 4, second sentence).
//
// The files:
//
//	bench.go      the types the API serves
//	suite.go      data/bench/suite.yaml: the prompts, the options
//	config.go     every constant the harness uses, CHOSEN or MEASURED
//	plan.go       what a run would do: prompts that fit, the estimate, the
//	              refusal of a configuration that would spill, the duration
//	harness.go    runs one benchmark at a time, streams its progress, cancels
//	              (and unloads)
//	stats.go      medians and spreads
//	sampler.go    the 1 Hz resource sampler and its probes, per OS and vendor
//	evidence.go   stored runs back into measurements and calibration
//
// Everything a benchmark produces is a measurement: every figure here is
// Source = measured, except a plan's duration, which is an estimate and
// says so. The prompts are the suite's own text, never anything the user
// typed (product rule 7).
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
	// StatusQueued is part of the schema and never used by the MVP: runs are
	// one at a time, and a second request is refused, not queued.
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusCancelled Status = "cancelled"
	StatusFailed    Status = "failed"
)

// Finished reports whether the run has ended, one way or another.
func (s Status) Finished() bool {
	return s == StatusDone || s == StatusCancelled || s == StatusFailed
}

// Phase is what a running benchmark is doing now.
type Phase string

const (
	PhasePreparing Phase = "preparing" // checking what is loaded, unloading this model, reading the machine at rest
	PhaseLoading   Phase = "loading"   // the warm-up: the runtime loads the model
	PhaseMeasuring Phase = "measuring" // the timed requests
	PhaseUnloading Phase = "unloading" // freeing the model's memory
	PhaseFinished  Phase = "finished"
)

// Resident is where the runtime put the model, from its own list of loaded
// models after the load.
type Resident string

const (
	ResidentGPU     Resident = "gpu"   // every byte on the graphics device
	ResidentSplit   Resident = "split" // part on the graphics device, part on the processor
	ResidentCPU     Resident = "cpu"   // on the processor
	ResidentUnknown Resident = "unknown"
)

// Request starts a run (POST /api/bench) or asks what one would do
// (GET /api/bench/plan).
type Request struct {
	// Model is the installed model, by the runtime's name for it
	// ("llama3.1:8b").
	Model string `json:"model"`
	// NumCtx is the context to test. 0: what Ollama itself would use on this
	// machine (estimate.OllamaDefaultContext). Configuration.
	NumCtx int `json:"num_ctx,omitempty" source:"n/a"`
	// Prompts narrows the suite to these prompt ids (the onboarding's "Try
	// it" runs "500" alone). Empty: every prompt.
	Prompts []string `json:"prompts,omitempty"`
	// MeasureAnyway runs a configuration the estimator says would spill onto
	// the processor, which is otherwise refused (step 6, item 5).
	MeasureAnyway bool `json:"measure_anyway,omitempty"`
}

// RunConfig is everything a run was made with: the comparability key. Two
// runs are compared only when Key() is equal — a vulkan run and a rocm run on
// the same card are two configurations (PRD §21, first risk).
type RunConfig struct {
	HardwareProfileID   int64  `json:"hardware_profile_id" source:"n/a"` // a row id: this start's hardware profile
	HardwareFingerprint string `json:"hardware_fingerprint"`
	Backend             string `json:"backend"`
	BackendVersion      string `json:"backend_version"`
	// RuntimePath is the path the runtime was seen to take for this load
	// ("unknown" when its output could not be read); RuntimePathEvidence is
	// the line or the reason.
	RuntimePath         hardware.RuntimePath `json:"runtime_path"`
	RuntimePathEvidence string               `json:"runtime_path_evidence,omitempty"`

	Model         string `json:"model"`
	ModelDigest   string `json:"model_digest"`
	Quantization  string `json:"quantization"`
	WeightsBytes  uint64 `json:"weights_bytes" source:"n/a"`   // the model's size as the runtime lists it: a fact about the file
	CatalogFileID int64  `json:"catalog_file_id" source:"n/a"` // a row id; 0 when the catalogue does not know this model

	NumCtx int `json:"num_ctx" source:"n/a"` // configuration: the context asked for
	// EffectiveCtx is the context the runtime ran (it clamps to the model's
	// trained context); 0 when it did not say. Read from the runtime.
	EffectiveCtx int `json:"effective_ctx" source:"n/a"`
	// KVCacheType is the cache the runtime allocated, from its own output:
	// "f16", "q8_0", … or "unknown" when the output could not be read.
	KVCacheType         string `json:"kv_cache_type"`
	FlashAttention      bool   `json:"flash_attention"`
	FlashAttentionKnown bool   `json:"flash_attention_known"`
	// Parallel is how many requests the runtime's cache was sized for
	// (OLLAMA_NUM_PARALLEL); 0 when it did not say. Read from the runtime.
	Parallel int `json:"parallel" source:"n/a"`

	SuiteVersion string `json:"suite_version"`
	SuiteDigest  string `json:"suite_digest"`
	// CompletionTokens and Repeats are the suite's: configuration.
	CompletionTokens int    `json:"completion_tokens" source:"n/a"`
	Repeats          int    `json:"repeats" source:"n/a"`
	DaemonVersion    string `json:"daemon_version"`
}

// Timing is one timed request, as the runtime counted it — the raw material
// of a PromptResult's medians.
type Timing struct {
	PromptTokens int     `json:"prompt_tokens" source:"n/a"` // raw runtime counters behind the medians; the UI shows the medians
	CachedTokens int     `json:"cached_tokens" source:"n/a"` // likewise: prompt tokens the runtime reused instead of processing
	CachedKnown  bool    `json:"cached_known"`
	PromptMs     float64 `json:"prompt_ms" source:"n/a"` // likewise
	GenTokens    int     `json:"gen_tokens" source:"n/a"`
	GenMs        float64 `json:"gen_ms" source:"n/a"`
	TTFTMs       float64 `json:"ttft_ms" source:"n/a"` // at the client: request sent to first token received
	LoadMs       float64 `json:"load_ms" source:"n/a"`
	DoneReason   string  `json:"done_reason,omitempty"`
}

// PromptResult is the median and the spread over the timed requests of one
// prompt. Every rate is a measured point.
type PromptResult struct {
	Prompt       string `json:"prompt"`                     // the suite's prompt id, e.g. "500"
	PromptTokens int    `json:"prompt_tokens" source:"n/a"` // a count: this model's tokens for the prompt (median)
	GenTokens    int    `json:"gen_tokens" source:"n/a"`    // a count: tokens in the answer (median)

	PromptTPS *figure.Rate `json:"prompt_tps,omitempty"` // (prompt_eval_count − reused) / prompt_eval_duration; absent when the runtime gave no time
	GenTPS    figure.Rate  `json:"generation_tps"`       // eval_count / eval_duration
	TTFT      *figure.Rate `json:"ttft,omitempty"`       // ms to the first streamed token, measured at the client; absent when no token came

	// SpreadPct is (max − min) ÷ median of the generation rate over the
	// timed requests, in percent; PromptSpreadPct the same for the prompt
	// rate. Statistics about the measurement, shown under Advanced.
	SpreadPct       float64 `json:"spread_pct" source:"n/a"`
	PromptSpreadPct float64 `json:"prompt_spread_pct" source:"n/a"`
	Runs            int     `json:"runs" source:"n/a"` // a count

	Timings []Timing `json:"timings"`
	// Notes say what makes this result less certain, in words: runs that
	// disagreed, an answer that stopped early, a prompt the runtime partly
	// reused from its cache.
	Notes []string `json:"notes,omitempty"`
}

// Skipped is a prompt a run did not time, and why.
type Skipped struct {
	Prompt string `json:"prompt"`
	Why    string `json:"why"`
}

// Comparison is this run against the previous run of the same configuration
// (the same RunConfig.Key) — the check that the measurement repeats.
type Comparison struct {
	RunID int64 `json:"run_id" source:"n/a"` // a row id
	// GenTPS is that run's headline generation rate.
	GenTPS figure.Rate `json:"generation_tps"`
	// DiffPct is this run's headline generation rate against that one's, in
	// percent. A statistic about two measurements.
	DiffPct float64 `json:"diff_pct" source:"n/a"`
}

// Run is one benchmark_runs row and the payload of GET /api/bench/{id}.
type Run struct {
	ID      int64     `json:"id" source:"n/a"`
	Status  Status    `json:"status"`
	Phase   Phase     `json:"phase"`
	Request Request   `json:"request"`
	Config  RunConfig `json:"config"`

	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	Results []PromptResult `json:"results"`
	Skipped []Skipped      `json:"skipped,omitempty"`

	// Headline is the prompt whose medians stand for the run — the shortest
	// that ran, which matches what the estimator's figures are for (llama.cpp's
	// 512-token prompt, an almost empty cache while answering) — and GenTPS,
	// PromptTPS and TTFT are its medians.
	Headline  string       `json:"headline,omitempty"`
	GenTPS    *figure.Rate `json:"generation_tps,omitempty"`
	PromptTPS *figure.Rate `json:"prompt_tps,omitempty"`
	TTFT      *figure.Rate `json:"ttft,omitempty"`
	// Load is how long the runtime took to load the model for the warm-up
	// (Ollama's load_duration), in ms.
	Load *figure.Rate `json:"load,omitempty"`

	Resident Resident `json:"resident"`
	// RuntimeSizeBytes and RuntimeSizeVRAMBytes are the runtime's own account
	// of the loaded model (Ollama's /api/ps size and size_vram): its
	// estimate, not a measurement (ARCHITECTURE.md D-20, finding 4), kept as
	// context and as the evidence for where "fits" ends. 0 when not read.
	RuntimeSizeBytes     uint64 `json:"runtime_size_bytes,omitempty" source:"n/a"`
	RuntimeSizeVRAMBytes uint64 `json:"runtime_size_vram_bytes,omitempty" source:"n/a"`

	// PeakVRAM is how much the graphics memory counter rose over its reading
	// before the load, at its peak: what the model took. Absent where no
	// counter exists or it could not be trusted — MemorySource and
	// SamplerNote say which and why.
	PeakVRAM     *figure.Bytes `json:"peak_vram,omitempty"`
	MemorySource string        `json:"memory_source,omitempty"`
	// PeakRAM is the system memory in use at its peak during the run.
	PeakRAM *figure.Bytes `json:"peak_ram,omitempty"`
	// GPUUtil (%), PeakTemp (°C) and Power (W) are from the sampler while the
	// timed requests ran: mean utilisation, hottest reading, mean draw.
	// Absent where no tool reads them.
	GPUUtil  *figure.Rate `json:"gpu_util,omitempty"`
	PeakTemp *figure.Rate `json:"peak_temp,omitempty"`
	Power    *figure.Rate `json:"power,omitempty"`
	// SamplerNote says, in words, what could not be sampled on this machine
	// ("no reading of the graphics card's memory exists for AMD cards on
	// Windows") — the row says so rather than showing zero.
	SamplerNote string `json:"sampler_note,omitempty"`

	// Estimate is what the advisor estimated for this configuration before
	// the run: the numbers the measurement replaces.
	Estimate *estimate.Estimate `json:"estimate,omitempty"`
	// ExpectedDuration is how long the run was estimated to take, in seconds.
	ExpectedDuration *figure.Rate `json:"expected_duration,omitempty"`
	// Replaced is true when this run's measurement replaced the estimate of
	// its configuration (the catalogue knows the model, the run finished,
	// the cache type and the path were read).
	Replaced bool `json:"replaced"`
	// Unloaded is true when, after the run, the model was confirmed gone from
	// the runtime's list of loaded models; false when it was still there;
	// absent when that could not be checked.
	Unloaded *bool `json:"unloaded,omitempty"`

	Comparison *Comparison `json:"comparison,omitempty"`
	Notes      []string    `json:"notes,omitempty"`
	Error      string      `json:"error,omitempty"`

	// Samples are the 1 Hz readings, only on GET /api/bench/{id}.
	Samples []Sample `json:"samples,omitempty"`
}

// Sample is one resource reading (benchmark_samples): one tool, one device,
// one moment.
type Sample struct {
	At       time.Time `json:"at"`
	Tool     string    `json:"tool"`                                   // nvidia-smi, sysfs, vm_stat, ioreg, meminfo, os, api/ps
	Device   string    `json:"device,omitempty"`                       // GPU index, DRM card, model name for api/ps; "" for the whole system
	GPUUtil  *float64  `json:"gpu_util_pct,omitempty" source:"n/a"`    // raw sampler readings; the figures the user sees are the summaries on Run
	VRAMUsed *uint64   `json:"vram_used_bytes,omitempty" source:"n/a"` //
	RAMUsed  *uint64   `json:"ram_used_bytes,omitempty" source:"n/a"`  //
	TempC    *float64  `json:"temp_c,omitempty" source:"n/a"`          //
	PowerW   *float64  `json:"power_w,omitempty" source:"n/a"`         //
}

// Progress is one event of GET /api/bench/{id}'s stream.
type Progress struct {
	RunID  int64  `json:"run_id" source:"n/a"` // a row id
	Status Status `json:"status"`
	Phase  Phase  `json:"phase"`
	// Message says what is happening, in words.
	Message string `json:"message"`
	// Step of Steps requests have finished (warm-ups included). Counts.
	Step  int `json:"step" source:"n/a"`
	Steps int `json:"steps" source:"n/a"`
	// ElapsedSeconds is a clock reading since the run started.
	ElapsedSeconds float64 `json:"elapsed_seconds" source:"n/a"`
	// Remaining is the estimated time left, in seconds.
	Remaining *figure.Rate `json:"remaining,omitempty"`
	// Last is the latest resource reading of the graphics device (or the
	// system, where there is none).
	Last *Sample `json:"last,omitempty"`
	// Run is the run as it stands, results so far included.
	Run Run `json:"run"`
}

// Plan is what a run would do (GET /api/bench/plan, and the first thing
// POST /api/bench works out).
type Plan struct {
	Model string `json:"model"`
	// ModelSource says where the model's description came from: "catalogue"
	// (the curated list knows it) or "runtime" (what the runtime reports).
	ModelSource string `json:"model_source"`
	NumCtx      int    `json:"num_ctx" source:"n/a"` // configuration
	// NumCtxSource: "requested", or "ollama_default" — what Ollama itself
	// runs a model at on this machine when nobody sets a context.
	NumCtxSource string          `json:"num_ctx_source"`
	Prompts      []PlannedPrompt `json:"prompts"`
	Requests     int             `json:"requests" source:"n/a"` // a count, warm-ups included
	// Estimate is this model at this context on this machine, as the
	// advisor estimates it before measuring.
	Estimate estimate.Estimate `json:"estimate"`
	// Duration is the estimated length of the run, in seconds; absent when
	// there is no speed estimate, and DurationUnknown says so.
	Duration        *figure.Rate `json:"duration,omitempty"`
	DurationUnknown string       `json:"duration_unknown,omitempty"`
	// Refusal is set when the estimator says this configuration would spill
	// onto the processor or not fit at all: the run is refused unless
	// measure_anyway is set, and this is why, in words. RefusalCode is the
	// same for the UI's logic; SuggestedCtx a context that would fit.
	Refusal      string    `json:"refusal,omitempty"`
	RefusalCode  string    `json:"refusal_code,omitempty"`
	SuggestedCtx int       `json:"suggested_ctx,omitempty" source:"n/a"` // configuration the advisor suggests
	Suite        SuiteInfo `json:"suite"`
	Notes        []string  `json:"notes,omitempty"`
}

// PlannedPrompt is one prompt of a plan.
type PlannedPrompt struct {
	ID string `json:"id"`
	// Tokens is the prompt's length with the suite's reference tokenizer.
	Tokens int `json:"tokens" source:"n/a"` // a count
	Runs   int `json:"runs" source:"n/a"`   // timed requests planned; 0 when skipped
	// Skip says why this prompt will not run at this context.
	Skip string `json:"skip,omitempty"`
}

// SuiteInfo is the suite a run uses.
type SuiteInfo struct {
	Version          string  `json:"version"`
	Digest           string  `json:"digest"`
	CompletionTokens int     `json:"completion_tokens" source:"n/a"` // configuration
	Warmups          int     `json:"warmups" source:"n/a"`
	Repeats          int     `json:"repeats" source:"n/a"`
	Temperature      float64 `json:"temperature" source:"n/a"`
	Seed             int     `json:"seed" source:"n/a"`
}

// History is GET /api/bench/history.
type History struct {
	Runs []Run `json:"runs"`
}

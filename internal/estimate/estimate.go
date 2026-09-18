// Package estimate answers "will it fit, and how fast" before anything is
// downloaded. Step 1: types only. Step 5 implements Fit and the speed model.
//
// The arithmetic is step 0's, as validated on three machines and three
// runtime backends (scripts/probe0/, and the record in ARCHITECTURE.md):
//
//	predicted = weights_bytes + KV + overhead
//	KV        = 2 * block_count * head_count_kv * head_dim * ctx * 2 B   (f16 cache)
//	head_dim  = attention.key_length where stated, else embedding_length / head_count
//	ctx       = min(num_ctx, the model's trained context_length)
//	overhead  = a constant per runtime path: cuda 250 MiB, metal 0, vulkan 50, rocm 50, cpu 0
//
// Every constant goes in one exported config with the measurement that
// justifies it. Nothing here is a measurement: every number a user sees is
// a figure with Source = estimated until a benchmark replaces it.
package estimate

import (
	"advisor/internal/figure"
	"advisor/internal/hardware"
)

// Category is the five-way answer to "will it fit", in the order the UI
// shows them (PRD §6, step 3).
type Category string

const (
	FitsWithHeadroom   Category = "fits_with_headroom"
	Fits               Category = "fits"
	NeedsCPUOffload    Category = "needs_cpu_offload"
	ReducedContextOnly Category = "reduced_context_only"
	NotRecommended     Category = "not_recommended"
)

// KVCacheType is the KV cache precision the runtime is configured for.
// f16 is Ollama's default; q8_0 halves the cache and q4_0 quarters it.
type KVCacheType string

const (
	KVF16  KVCacheType = "f16"
	KVQ8_0 KVCacheType = "q8_0"
	KVQ4_0 KVCacheType = "q4_0"
)

// Request is what Fit is asked about.
type Request struct {
	CatalogFileID int64                `json:"catalog_file_id" source:"n/a"`
	NumCtx        int                  `json:"num_ctx" source:"n/a"` // configuration
	KVCacheType   KVCacheType          `json:"kv_cache_type"`
	RuntimePath   hardware.RuntimePath `json:"runtime_path"` // which overhead term applies
}

// Memory is the memory estimate, term by term, so the UI can explain it
// under the Advanced toggle.
type Memory struct {
	Weights     figure.Bytes `json:"weights"`
	KVCache     figure.Bytes `json:"kv_cache"`
	Overhead    figure.Bytes `json:"overhead"`
	Total       figure.Bytes `json:"total"`
	GPUResident figure.Bytes `json:"gpu_resident"`
	CPUOffload  figure.Bytes `json:"cpu_offload"`

	// EffectiveCtx is the context the estimate is for after clamping to the
	// model's trained context_length, as Ollama does. Configuration.
	EffectiveCtx int `json:"effective_ctx" source:"n/a"`
}

// Speed is the throughput estimate. It is a range, and the type says so.
type Speed struct {
	Generation figure.Rate `json:"generation"` // tok/s
	Prompt     figure.Rate `json:"prompt"`     // tok/s, estimated separately
	// Known is false when the GPU is not in data/hardware/gpus.yaml: then
	// there is no speed estimate, and the UI says so — never a number.
	Known bool `json:"known"`
}

// Estimate is the answer for one (catalogue file, context) on one profile.
// It is persisted in `estimates`; when a benchmark for the same
// configuration completes, the measured figures replace the estimated ones
// in place (product rule 4, second sentence) and Source flips.
type Estimate struct {
	Request  Request  `json:"request"`
	Memory   Memory   `json:"memory"`
	Speed    Speed    `json:"speed"`
	Category Category `json:"category"`
	// Threshold is the comparison that decided the category, in words the
	// Advanced view can show: "14.8 GB of 16.0 GB usable".
	Threshold string `json:"threshold"`
	// BudgetBytes is what the total was compared against: gpu_usable_bytes,
	// or RAM with the OS's own needs reserved on a CPU-only machine. Read
	// from the profile, so not a figure.
	BudgetBytes uint64 `json:"budget_bytes" source:"n/a"`
	// Notes carries the caveats step 0 found: sliding-window architectures
	// over-predict, MoE and vision models are their own problem.
	Notes []string `json:"notes,omitempty"`
}

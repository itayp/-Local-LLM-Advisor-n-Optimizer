// Package estimate answers "will it fit, and how fast" before anything is
// downloaded (build-plan step 5).
//
// The memory arithmetic is step 0's, as validated on three machines and
// three runtime backends (scripts/probe0/, ARCHITECTURE.md D-20):
//
//	predicted = weights_bytes + KV + overhead
//	KV        = 2 * block_count * head_count_kv * head_dim * ctx * 2 B   (f16 cache)
//	head_dim  = attention.key_length where stated, else embedding_length / head_count
//	ctx       = min(num_ctx, the model's trained context_length)
//	overhead  = a constant per runtime path: cuda 250 MiB, metal 0, vulkan 50, rocm 50, cpu 0
//
// generalised — without changing a single step 0 row — to the layer layouts
// the catalogue actually holds (catalog.Layout): layers that keep no cache,
// layers whose cache stops at a sliding window, keys and values of different
// lengths. step0_test.go replays every step 0 row through Fit and holds it
// to the step 0 gate.
//
// The files:
//
//	estimate.go    the types the API serves
//	config.go      every constant, each with the measurement behind it
//	placement.go   which memory a model would run in on this machine, and why
//	fit.go         Fit: the memory terms, the split, the category
//	speed.go       the speed estimate — a range, and labelled as one
//	devices.go     data/hardware/gpus.yaml: memory bandwidth per part
//
// Nothing here is a measurement: every number a user sees is a figure with
// Source = estimated until a benchmark replaces it (WithMeasurement).
package estimate

import (
	"advisor/internal/catalog"
	"advisor/internal/figure"
	"advisor/internal/hardware"
)

// Category is the five-way answer to "will it fit", in the order the UI
// shows them (PRD §6, step 3) — and a sixth for when the advisor cannot say.
type Category string

const (
	FitsWithHeadroom   Category = "fits_with_headroom"
	Fits               Category = "fits"
	NeedsCPUOffload    Category = "needs_cpu_offload"
	ReducedContextOnly Category = "reduced_context_only"
	NotRecommended     Category = "not_recommended"
	// CategoryUnknown: the memory the model would run in could not be read,
	// so there is nothing to compare against (ARCHITECTURE.md D-21). Never a
	// guess in either direction.
	CategoryUnknown Category = "unknown"
)

// FitsOnDevice reports whether the category means the whole model runs
// where the plan put it, at the context asked for.
func (c Category) FitsOnDevice() bool { return c == FitsWithHeadroom || c == Fits }

// KVCacheType is the KV cache precision the runtime is configured for.
// f16 is Ollama's default; q8_0 roughly halves the cache and q4_0 quarters it.
type KVCacheType string

const (
	KVF16  KVCacheType = "f16"
	KVQ8_0 KVCacheType = "q8_0"
	KVQ4_0 KVCacheType = "q4_0"
)

// Valid reports whether t is a cache type the estimator knows.
func (t KVCacheType) Valid() bool { return t == KVF16 || t == KVQ8_0 || t == KVQ4_0 }

// Machine is the hardware profile plus what the runtime was seen to do with
// it — the two halves of ARCHITECTURE.md D-5.
type Machine struct {
	Profile hardware.Profile
	// ActualPath is the runtime path the backend established for the primary
	// graphics device after a load (D-31): cuda, metal, rocm, vulkan or cpu.
	// Empty when no load has been observed yet; the plan is then made against
	// the expected path and labelled as such.
	ActualPath hardware.RuntimePath
	// RuntimeEnv is the runtime's steering environment as the backend
	// captured it (OLLAMA_VULKAN, …), for explaining a card that is not used.
	RuntimeEnv map[string]string
}

// Model is what Fit is asked about: one weights file of one catalogue size,
// and the vision encoder the runtime loads beside it when the size has one.
type Model struct {
	File      catalog.File
	Projector *catalog.File
	Size      catalog.Size
}

// Request is the configuration Fit is asked about.
type Request struct {
	CatalogFileID int64       `json:"catalog_file_id" source:"n/a"` // a row id; 0 for a file that is not in the catalogue
	NumCtx        int         `json:"num_ctx" source:"n/a"`         // configuration
	KVCacheType   KVCacheType `json:"kv_cache_type"`
	// RuntimePath is the path the estimate was made for, filled in by Fit.
	RuntimePath hardware.RuntimePath `json:"runtime_path"`
}

// Memory is the memory estimate, term by term, so the UI can show the
// arithmetic under the Advanced toggle.
type Memory struct {
	Weights  figure.Bytes `json:"weights"`  // the model file, plus its vision encoder when it has one
	KVCache  figure.Bytes `json:"kv_cache"` // grows with the context; includes a hybrid model's fixed state
	Overhead figure.Bytes `json:"overhead"` // the runtime path's own cost
	Total    figure.Bytes `json:"total"`
	// GPUResident is the part placed in graphics memory; CPUOffload the part
	// that lives in system memory — because the graphics memory is full, or
	// by the model's design (Gemma's per-layer tables), or because the whole
	// model runs on the processor.
	GPUResident figure.Bytes `json:"gpu_resident"`
	CPUOffload  figure.Bytes `json:"cpu_offload"`

	// EffectiveCtx is the context the estimate is for after clamping to the
	// model's trained context_length, as Ollama does. Configuration.
	EffectiveCtx int `json:"effective_ctx" source:"n/a"`
}

// Speed is the throughput estimate. It is a RANGE, and the type says so:
// both rates are figure.Rate with Low < High until a benchmark measures
// them, when they become points (WithMeasurement).
type Speed struct {
	// Known is false when there is no estimate: the graphics card (or the
	// processor's memory) is not in data/hardware/gpus.yaml. Then both rates
	// are absent and Unknown says why, in words — never a number.
	Known      bool         `json:"known"`
	Generation *figure.Rate `json:"generation,omitempty"` // tok/s while answering
	Prompt     *figure.Rate `json:"prompt,omitempty"`     // tok/s while reading the prompt, estimated separately
	Unknown    string       `json:"unknown,omitempty"`
	// Basis says, for the Advanced view, what the range was built from.
	Basis string `json:"basis,omitempty"`
}

// Basis records which inputs of an estimate were measured, estimated or
// unknown. recommend derives a recommendation's confidence from it
// (PRD §21's last risk).
type Basis struct {
	// MemoryModel is how far the memory arithmetic has been checked for this
	// model's shape.
	MemoryModel MemoryModel `json:"memory_model"`
	// PathSource is whether the runtime path was seen after a load or is the
	// rule-derived expectation.
	PathSource PathSource `json:"path_source"`
	// BudgetKnown is false when the memory to compare against could not be read.
	BudgetKnown bool `json:"budget_known"`
	// SpeedSource is measured, estimated or unknown.
	SpeedSource SpeedSource `json:"speed_source"`
}

// MemoryModel grades the memory estimate's footing.
type MemoryModel string

const (
	// MemoryValidated: a plain stack of attention layers, text only — the
	// shape step 0 measured within 15% on 27 of 28 rows.
	MemoryValidated MemoryModel = "validated"
	// MemoryModelled: the arithmetic follows the header and the runtime's
	// source (hybrid layers, sliding windows, experts, a vision encoder) but
	// the fleet has not measured this shape inside a gate yet.
	MemoryModelled MemoryModel = "modelled"
	// MemoryIncomplete: something the arithmetic needs could not be read from
	// the model's header, so part of the figure is missing or assumed.
	MemoryIncomplete MemoryModel = "incomplete"
	// MemoryMeasured: a benchmark's peak reading replaced the estimate.
	MemoryMeasured MemoryModel = "measured"
)

// PathSource says where the runtime path came from.
type PathSource string

const (
	PathEstablished PathSource = "established" // seen in the runtime after a load (D-31)
	PathExpected    PathSource = "expected"    // from data/hardware/runtime-support.yaml (D-24)
)

// SpeedSource says where the speed came from.
type SpeedSource string

const (
	SpeedMeasured  SpeedSource = "measured"
	SpeedEstimated SpeedSource = "estimated"
	SpeedUnknown   SpeedSource = "unknown"
)

// BudgetKind is which memory the model was compared against.
type BudgetKind string

const (
	BudgetGraphics BudgetKind = "graphics_memory" // a graphics card's own memory
	BudgetUnified  BudgetKind = "unified_memory"  // Apple Silicon: the share of memory macOS lets the GPU use
	BudgetSystem   BudgetKind = "system_memory"   // RAM with the operating system's needs reserved
)

// Estimate is the answer for one (model file, context) on one machine.
type Estimate struct {
	Request  Request  `json:"request"`
	Memory   Memory   `json:"memory"`
	Speed    Speed    `json:"speed"`
	Category Category `json:"category"`
	// Threshold is the comparison that decided the category, in words the
	// Advanced view can show: "8.9 GB of 11.8 GB (75%): at or under 80% fits
	// with headroom".
	Threshold string `json:"threshold"`
	// BudgetBytes is what the total was compared against: gpu_usable_bytes,
	// or RAM with the OS's own needs reserved. Read from the profile (less a
	// configured reserve), so not a figure. 0 with BudgetKnown false when it
	// could not be read.
	BudgetBytes uint64     `json:"budget_bytes" source:"n/a"`
	BudgetKnown bool       `json:"budget_known"`
	BudgetKind  BudgetKind `json:"budget_kind"`
	// SuggestedCtx is, when the context asked for does not fit but a shorter
	// one does, the longest context from the ladder that fits. Configuration
	// the advisor suggests; 0 otherwise.
	SuggestedCtx int `json:"suggested_ctx,omitempty" source:"n/a"`
	// Basis is the provenance of the inputs.
	Basis Basis `json:"basis"`
	// Notes carries the caveats, for the Advanced view: what the layout
	// assumed, what was left out, what step 0 found about this shape.
	Notes []string `json:"notes,omitempty"`
}

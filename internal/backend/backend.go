// Package backend is the seam between the advisor and the runtimes that
// actually run models. Ollama is the only implementation in the MVP;
// llama.cpp and LM Studio are a second file each later (PRD §18), not a
// rewrite — which is only true if this interface leaks no Ollama
// assumption.
//
// Step 3 completed the interface (Models, Show, Running, Pull, Generate,
// Unload, Install, Start) after reading Ollama's HTTP API (docs/api.md),
// LM Studio's local REST API (v1: /api/v1/models, /models/load,
// /models/unload, /models/download) and its `lms` CLI (ls, get, load, ps,
// unload), and llama-server's API (/props, /v1/models, /slots, and its
// newer --models-dir router mode: /models, /models/load, /models/unload,
// load-on-demand by name). Every method here has a home in all three:
//
//   - Models/Show/Running read an inventory every backend can report in some
//     form (a downloaded-models list; a loaded-models list with sizes).
//   - Pull takes a ModelSource, not a string, because "an Ollama library tag"
//     and "a GGUF file on Hugging Face" are different things to fetch — the
//     seam most likely to leak. llama.cpp has no pull of its own; its
//     implementation (a later file) fetches the GGUF itself and places it in
//     the runtime's models directory.
//   - Generate is deliberately thin (no chat history, tools or images — that
//     is a chat app's job, D-4): the benchmark harness (step 6) is its only
//     caller, and every backend has *some* completion endpoint.
//   - Install and Start are asymmetric by design: what "installed" means
//     differs per backend (an app bundle, a service, a binary someone built
//     themselves), so each implementation decides for itself; the interface
//     only promises that after Install, Detect can (eventually) report
//     installed, and after Start, running.
package backend

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"advisor/internal/hardware"
)

// State is the coarse state of a runtime on this machine. The four values
// are distinct on purpose: "installed but not running" and "not installed"
// are two different buttons in the UI (step 3, item 4).
type State string

const (
	StateNotInstalled        State = "not_installed"
	StateInstalledNotRunning State = "installed_not_running"
	StateRunning             State = "running"
	StateUnsupported         State = "unsupported" // this OS/arch has no build of the runtime
)

// Status is what Detect learned about a runtime.
type Status struct {
	State   State  `json:"state"`
	Version string `json:"version,omitempty"` // when running
	Host    string `json:"host,omitempty"`    // the API endpoint, e.g. http://127.0.0.1:11434
	Detail  string `json:"detail,omitempty"`  // plain words for the UI when something is off

	// RuntimePaths records, per GPU index in the hardware profile, which path
	// the runtime actually took (cuda, metal, rocm, vulkan, cpu) — a fact the
	// app establishes after a load (step 3, item 5), never an assumption.
	// Empty until a model has been loaded, or when a load happened but the
	// evidence (the server's own log) could not be read to say which named
	// backend it was — D-21: unknown is left out, never guessed.
	RuntimePaths map[int]hardware.RuntimePath `json:"runtime_paths,omitempty"`

	// Env is the environment variables that steer which GPU path a runtime
	// takes, captured exactly as this process's environment has them —
	// never set, never written to (OLLAMA_VULKAN, GGML_VK_VISIBLE_DEVICES,
	// HSA_OVERRIDE_GFX_VERSION for Ollama). Only variables that are
	// actually set are included. A future backend decides its own relevant
	// set the same way Ollama's implementation does.
	Env map[string]string `json:"env,omitempty"`
}

// SourceKind is where a model to Pull comes from — the seam D-3 calls out
// as most likely to leak an Ollama assumption.
type SourceKind string

const (
	// SourceOllamaTag is a name Ollama's own library resolves
	// ("llama3.1:8b", "qwen3:4b-q4_K_M").
	SourceOllamaTag SourceKind = "ollama_tag"
	// SourceHuggingFace is one GGUF file inside a Hugging Face repo —
	// what catalog.File (step 4) carries for a llama.cpp/LM Studio model.
	SourceHuggingFace SourceKind = "huggingface_gguf"
)

// ModelSource names a model to fetch. Exactly the fields matching Kind are
// set. A backend that cannot fetch from this Kind returns
// ErrUnsupportedSource from Pull — never a best-effort guess at a
// translation between the two (an Ollama tag is not a Hugging Face repo,
// and pretending otherwise is exactly the leak D-3 warns about).
type ModelSource struct {
	Kind SourceKind

	// OllamaTag is set when Kind == SourceOllamaTag.
	OllamaTag string

	// HFRepo and HFFile are set when Kind == SourceHuggingFace: the repo
	// ("TheBloke/Llama-3.1-8B-GGUF") and the file inside it
	// ("llama-3.1-8b.Q4_K_M.gguf").
	HFRepo string
	HFFile string
}

// String is a plain-words name for logging and progress text, not an
// identifier any backend parses back.
func (s ModelSource) String() string {
	switch s.Kind {
	case SourceOllamaTag:
		return s.OllamaTag
	case SourceHuggingFace:
		return s.HFRepo + "/" + s.HFFile
	default:
		return string(s.Kind)
	}
}

// Validate reports whether s is well-formed for its Kind. It says nothing
// about whether a particular backend can fetch it — that is
// ErrUnsupportedSource's job.
func (s ModelSource) Validate() error {
	switch s.Kind {
	case SourceOllamaTag:
		if s.OllamaTag == "" {
			return errors.New("backend: ModelSource: ollama_tag source needs OllamaTag")
		}
	case SourceHuggingFace:
		if s.HFRepo == "" || s.HFFile == "" {
			return errors.New("backend: ModelSource: huggingface_gguf source needs HFRepo and HFFile")
		}
	default:
		return fmt.Errorf("backend: ModelSource: unknown kind %q", s.Kind)
	}
	return nil
}

// ErrUnsupportedSource is returned by Pull when asked to fetch a
// ModelSource.Kind the backend does not know how to reach.
var ErrUnsupportedSource = errors.New("backend: this runtime cannot fetch a model from that source")

// Installed is one model Models() found on this machine, as the runtime's
// own inventory reports it. It is a fact the runtime reports, not the
// advisor's own estimate or measurement (product rule 4 is about the
// advisor's memory predictions; where these fields become API numbers they
// carry source:"n/a", the same as hardware.Profile's read-from-the-OS
// numbers).
type Installed struct {
	Name          string `json:"name"` // "llama3.1:8b" — what Show/Generate/Pull/Unload take
	Digest        string `json:"digest,omitempty"`
	SizeBytes     uint64 `json:"size_bytes" source:"n/a"` // the blob, from the runtime
	Family        string `json:"family,omitempty"`
	ParameterSize string `json:"parameter_size,omitempty"`
	Quantization  string `json:"quantization,omitempty"`
	ModifiedAt    string `json:"modified_at,omitempty"` // RFC 3339; "" if the runtime does not say
}

// ModelInfo is what Show(name) returns: everything steps 4 (the catalogue,
// GGUF parsing) and 5 (the estimator) need about one model.
//
// Details carries the model's raw metadata exactly as the GGUF format
// itself names it: "<architecture>.block_count",
// "<architecture>.attention.head_count_kv",
// "<architecture>.attention.key_length", "<architecture>.context_length",
// and so on — a value may arrive as a JSON number, a numeric string, or (for
// a few architectures) a per-layer array. This is not an Ollama-shaped leak
// into the interface: Ollama's /api/show re-exposes the GGUF file's own
// key-value metadata close to verbatim, and a backend that reads a GGUF
// file directly (llama.cpp, LM Studio, or step 4's own parser for a
// not-yet-downloaded Hugging Face file) populates the same map from the
// file's header, using the same keys, because they are the file format's
// keys, not the runtime's. ARCHITECTURE.md D-20's findings — prefer
// attention.key_length over embedding_length/head_count when both are
// present, clamp context to context_length, read attention.sliding_window —
// are all read from this map by the estimator (step 5), the same way
// regardless of which backend produced it. A backend that cannot read GGUF
// metadata at all leaves Details empty; the estimator then has nothing to
// work with for that model, which is honest (product rule 4 / D-21) rather
// than a broken interface.
type ModelInfo struct {
	Name          string   `json:"name"`
	Architecture  string   `json:"architecture,omitempty"` // Details["general.architecture"], hoisted for convenience
	Family        string   `json:"family,omitempty"`
	Families      []string `json:"families,omitempty"`
	ParameterSize string   `json:"parameter_size,omitempty"`
	Quantization  string   `json:"quantization,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"` // "completion", "vision", "embedding", ...
	// ContextLength is Details["<architecture>.context_length"], hoisted for
	// convenience; a fact read from the model file via the runtime, not an
	// advisor estimate.
	ContextLength int `json:"context_length,omitempty" source:"n/a"`

	Details map[string]any `json:"details,omitempty"` // raw, architecture-prefixed GGUF metadata

	Modelfile  string `json:"modelfile,omitempty"` // Ollama's Modelfile text; "" where the concept does not exist
	Template   string `json:"template,omitempty"`
	Parameters string `json:"parameters,omitempty"` // the Modelfile's PARAMETER lines, as the runtime returns them
}

// Loaded is one entry from Running(): a model this backend currently has in
// memory.
type Loaded struct {
	Name          string `json:"name"`
	SizeBytes     uint64 `json:"size_bytes" source:"n/a"`      // total resident (Ollama: size)
	SizeVRAMBytes uint64 `json:"size_vram_bytes" source:"n/a"` // the portion on a GPU (Ollama: size_vram); 0 means CPU-only
	ContextLength int    `json:"context_length" source:"n/a"`  // the context this instance is running with (already clamped)
	ExpiresAt     string `json:"expires_at,omitempty"`         // RFC 3339; "" when the runtime does not say or it never expires
}

// PullProgress is one update from a Pull in progress. Completed and Total
// are bytes; Total is 0 when the runtime has not reported a size yet (an
// early "pulling manifest" message).
type PullProgress struct {
	Status    string
	Completed int64
	Total     int64
}

// GenerateRequest is a minimal completion request — enough for the
// benchmark harness (step 6), its only caller. It deliberately does not
// grow chat history, tools or images: a chat app's job (D-4), not the
// advisor's (D-8: the advisor calls no LLM to do its own job).
type GenerateRequest struct {
	Model  string
	Prompt string
	System string

	// Options is passed through to the runtime mostly unexamined (num_ctx,
	// num_predict, temperature, seed, ...); the benchmark harness owns what
	// it means and how it is scored.
	Options map[string]any

	// KeepAlive controls how long the runtime keeps the model resident after
	// this request ("5m", "0" to unload immediately, "-1" forever). ""
	// means the runtime's own default.
	KeepAlive string

	// Raw sends Prompt to the model exactly as given: no chat template, no
	// system prompt, nothing the runtime adds. The benchmark harness times
	// its own text and nothing else (step 6). Every runtime has such a mode
	// (Ollama's raw, llama-server's /completion, LM Studio's
	// /v1/completions).
	Raw bool

	// NoTruncate asks the runtime to refuse a prompt longer than the context
	// instead of silently dropping part of it — a benchmark of a shortened
	// prompt times the wrong thing — and not to shift the context when the
	// answer reaches its end.
	NoTruncate bool
}

// GenerateEvent is one update from Generate: a token chunk, or the final
// event (Done == true) carrying the runtime's own timing, which the
// benchmark harness reads for tokens/sec.
type GenerateEvent struct {
	Response string
	// Thinking is text a reasoning model produced before its answer, when
	// the runtime separates the two. For timing it is output like any other.
	Thinking   string
	Done       bool
	DoneReason string

	TotalDuration      time.Duration
	LoadDuration       time.Duration
	PromptEvalCount    int
	PromptEvalDuration time.Duration
	EvalCount          int
	EvalDuration       time.Duration

	// PromptEvalCached is how many of PromptEvalCount's tokens the runtime
	// reused from its cache instead of processing — PromptEvalDuration
	// covers only the rest. PromptEvalCachedKnown is false when the runtime
	// does not say (Ollama before its llama-server engine).
	PromptEvalCached      int
	PromptEvalCachedKnown bool
}

// LoadReport is what a runtime said, in its own output, about how it loaded
// a model: the path it took, the cache and attention settings it chose,
// where the layers went. Read, never assumed (D-21): a field the output did
// not state stays empty, and Evidence lists the lines that were read.
type LoadReport struct {
	// Read is false when the runtime's output could not be read at all
	// (no log where one was looked for); Why then says so.
	Read bool
	Why  string

	RuntimePath hardware.RuntimePath // "" when the output named no device
	// Devices are the devices that hold part of the model's weights, by the
	// runtime's own names ("CUDA0", "MTL0", "Vulkan0", "CPU_Mapped").
	Devices []string

	FlashAttention      bool
	FlashAttentionKnown bool
	KVCacheType         string // "f16", "q8_0", …; "" when not stated

	// LayersOnGPU of Layers were placed on the graphics device; both 0 when
	// not stated.
	LayersOnGPU int
	Layers      int

	// ContextSize and Parallel are what the runtime was started with (the
	// cache holds ContextSize tokens, shared between Parallel requests);
	// 0 when not stated.
	ContextSize int
	Parallel    int

	Evidence []string
}

// LoadObserver is implemented by a backend that can report how it loaded a
// model. ObserveLoad marks the runtime's output as it is now; the function
// it returns reads what the runtime said after the mark. The benchmark
// harness calls it around its first request, which is the load.
type LoadObserver interface {
	ObserveLoad() func(ctx context.Context) LoadReport
}

// InstallProgress is one update from Install: a plain sentence fit for a
// progress line, and bytes when the current step is a download.
type InstallProgress struct {
	Status    string
	Completed int64
	Total     int64
}

// InstallSizer is implemented by a backend that can say, before Install
// ever runs, about how large its download is. Product rule 5: the button
// has to say what it will cost before it is clicked, and Install itself is
// not the place to find out — by the time it reports a Total, the download
// has already started. Optional (a type assertion, the same shape as
// LoadObserver above) because not every backend has a single downloadable
// installer to ask about.
type InstallSizer interface {
	// InstallSize reports the download's size in bytes. known is false
	// when the size could not be read (D-21: unknown is unknown, never a
	// default) — the caller says so in words rather than showing "about 0 MB".
	InstallSize(ctx context.Context) (bytes int64, known bool, err error)
}

// Backend is a runtime the advisor can drive.
type Backend interface {
	// Name is the registry key and the value stored in backends.name:
	// "ollama", later "llamacpp", "lmstudio".
	Name() string

	// Detect reports whether the runtime is installed, running, or cannot
	// run here. It must be cheap and must never start anything.
	Detect(ctx context.Context) (Status, error)

	// Models lists what is installed on this machine, whether or not it is
	// currently loaded. Callers check Detect's Status.State first; Models
	// does not start the runtime, and returns an error if it is not
	// running (an HTTP-driven backend has no other way to ask).
	Models(ctx context.Context) ([]Installed, error)

	// Show returns the metadata steps 4 and 5 need for one installed
	// model.
	Show(ctx context.Context, name string) (ModelInfo, error)

	// Running lists what this backend currently has loaded in memory.
	Running(ctx context.Context) ([]Loaded, error)

	// Pull fetches a model, reporting progress as it goes (progress may be
	// nil). It returns ErrUnsupportedSource when src.Kind is not one this
	// backend can fetch from.
	Pull(ctx context.Context, src ModelSource, progress func(PullProgress)) error

	// Generate runs one completion. onEvent is called for every streamed
	// chunk and once more, with Done == true, for the final event carrying
	// timing; it may be called synchronously from within Generate. Used by
	// the benchmark harness (step 6) only.
	Generate(ctx context.Context, req GenerateRequest, onEvent func(GenerateEvent) error) error

	// Unload asks the runtime to free a model's memory now.
	Unload(ctx context.Context, name string) error

	// Delete removes an installed model from the runtime's own storage,
	// freeing the disk space it took (the Models screen's "Remove" button,
	// build-plan step 8). The runtime is the one place that model's files
	// live (D-16); the advisor never touches a model file directly.
	Delete(ctx context.Context, name string) error

	// Install downloads and sets up the runtime itself. progress may be
	// nil. Product rule 5: this only ever runs from a UI button that says
	// what it will do before it is clicked; Install itself changes nothing
	// until called.
	Install(ctx context.Context, progress func(InstallProgress)) error

	// Start launches the runtime so a subsequent Detect can report
	// StateRunning. Product rule 5 again: a button, never automatic.
	Start(ctx context.Context) error
}

// Registry holds the backends the daemon knows about, by name. There is one
// package-level registry (Register / Lookup / All); a Registry value exists
// so tests can build their own.
type Registry struct {
	mu       sync.RWMutex
	backends map[string]Backend
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{backends: map[string]Backend{}}
}

// Register adds b. Registering the same name twice is a programming error
// and panics, like http.Handle does.
func (r *Registry) Register(b Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := b.Name()
	if name == "" {
		panic("backend: Register with an empty name")
	}
	if _, dup := r.backends[name]; dup {
		panic(fmt.Sprintf("backend: Register called twice for %q", name))
	}
	r.backends[name] = b
}

// Lookup returns the backend registered under name.
func (r *Registry) Lookup(name string) (Backend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.backends[name]
	return b, ok
}

// All returns every registered backend, sorted by name.
func (r *Registry) All() []Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Backend, 0, len(r.backends))
	for _, b := range r.backends {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

var global = NewRegistry()

// Register adds a backend to the package-level registry. Implementations
// call it from an init() in their own package (internal/backend/ollama),
// and cmd/advisor imports that package for its side effect.
func Register(b Backend) { global.Register(b) }

// Lookup finds a backend in the package-level registry.
func Lookup(name string) (Backend, bool) { return global.Lookup(name) }

// All lists the package-level registry.
func All() []Backend { return global.All() }

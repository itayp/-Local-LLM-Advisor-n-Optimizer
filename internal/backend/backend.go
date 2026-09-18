// Package backend is the seam between the advisor and the runtimes that
// actually run models. Ollama is the only implementation in the MVP;
// llama.cpp and LM Studio are a second file each later (PRD §18), not a
// rewrite — which is only true if this interface leaks no Ollama assumption.
//
// Step 1 ships the part of the contract the rest of the skeleton needs — a
// name, a detected Status, a registry. Step 3 defines the full interface
// (Models, Show, Running, Pull, Generate, Unload, Install, Start) after
// checking every method against the LM Studio local API and llama-server;
// it adds methods here rather than defining a second interface elsewhere.
package backend

import (
	"context"
	"fmt"
	"sort"
	"sync"

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
	// Empty until a model has been loaded.
	RuntimePaths map[int]hardware.RuntimePath `json:"runtime_paths,omitempty"`
}

// Backend is a runtime the advisor can drive. Step 3 grows this interface;
// see the package comment for the intended method set.
type Backend interface {
	// Name is the registry key and the value stored in backends.name:
	// "ollama", later "llamacpp", "lmstudio".
	Name() string
	// Detect reports whether the runtime is installed, running, or cannot
	// run here. It must be cheap and must never start anything.
	Detect(ctx context.Context) (Status, error)
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
// call it from an init() in their own package (internal/backend/ollama in
// step 3), and cmd/advisor imports that package for its side effect.
func Register(b Backend) { global.Register(b) }

// Lookup finds a backend in the package-level registry.
func Lookup(name string) (Backend, bool) { return global.Lookup(name) }

// All lists the package-level registry.
func All() []Backend { return global.All() }

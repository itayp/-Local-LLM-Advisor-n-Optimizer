// Package ollama drives Ollama over its local HTTP API
// (http://127.0.0.1:11434 by default; OLLAMA_HOST overrides). It is the
// only backend.Backend implementation in the MVP (ARCHITECTURE.md D-3).
//
// Layout:
//
//	ollama.go        the Backend type and the interface methods that talk
//	                  HTTP: Name, Detect, Models, Show, Running, Pull,
//	                  Generate, Unload
//	client.go         the raw HTTP client, Ollama's wire types, and the
//	                  model_info helpers step 0 validated (D-20)
//	env.go            the OS seam (env vars, $PATH, stat) Detect/Install/
//	                  Start use, so their logic is testable without a real
//	                  install
//	install.go        shared download-with-progress plumbing; per-OS
//	                  Install/Start/binary-location in install_<os>.go
//	runtimepath.go    step 3 item 5: what path a load actually took, from
//	                  /api/ps and the server's own device-discovery log
package ollama

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"advisor/internal/backend"
)

func init() {
	backend.Register(New())
}

// Name is the registry key ("ollama") and the value stored in backends.name.
const Name = "ollama"

// detectTimeout bounds Detect's HTTP probe. Detect "must be cheap and must
// never start anything" (backend.Backend's doc comment); a stalled or very
// slow local daemon should read as unreachable quickly, not hang the UI.
const detectTimeout = 2 * time.Second

// Backend drives Ollama over its HTTP API.
type Backend struct {
	env env

	mu sync.Mutex
	// supervisedLog is the log file this process wrote when it started
	// Ollama itself (Start, on any OS, launches "ollama serve" and
	// redirects its output here). When set, runtimePaths prefers it over
	// guessing at Ollama's own default log location, because it is known
	// to be this Ollama process's log, not possibly a stale one from a
	// previous run.
	supervisedLog string
}

// New builds the Ollama backend. It reads OLLAMA_HOST (and other
// environment) at call time, not at package init, so tests can control it
// through env rather than the process environment.
func New() *Backend {
	return &Backend{env: realEnv{}}
}

func (b *Backend) Name() string { return Name }

// host is the base URL of Ollama's API: OLLAMA_HOST if set (a scheme is
// added if it has none), else the documented default.
func (b *Backend) host() string {
	h := strings.TrimSpace(b.env.getenv("OLLAMA_HOST"))
	if h == "" {
		return "http://127.0.0.1:11434"
	}
	if !strings.HasPrefix(h, "http://") && !strings.HasPrefix(h, "https://") {
		h = "http://" + h
	}
	return strings.TrimRight(h, "/")
}

func (b *Backend) client(timeout time.Duration) *httpClient {
	return newHTTPClient(b.host(), timeout)
}

// Detect reports whether Ollama is installed, running, or cannot run here.
// It tries the HTTP API first (cheap, and the only way to learn the
// running version); when that fails it falls back to looking for the
// binary on disk, which never starts anything.
func (b *Backend) Detect(ctx context.Context) (backend.Status, error) {
	ctx, cancel := context.WithTimeout(ctx, detectTimeout)
	defer cancel()

	env := b.captureEnv()

	c := b.client(detectTimeout)
	if v, err := c.version(ctx); err == nil {
		status := backend.Status{State: backend.StateRunning, Version: v, Host: b.host(), Env: env}
		// Best-effort: what is actually loaded right now, and on what path.
		// Never fails Detect — a load already happened or it didn't; this
		// only decides whether RuntimePaths says something about it.
		if p, err := c.ps(ctx); err == nil && len(p.Models) > 0 {
			paths, detail := runtimePathsFromPS(p.Models[0].SizeVRAM, b.readRecentLog())
			if len(paths) > 0 {
				status.RuntimePaths = paths
			}
			if detail != "" {
				status.Detail = detail
			}
		}
		return status, nil
	}

	path, ok := b.findBinary()
	if !ok {
		return backend.Status{State: backend.StateNotInstalled, Env: env}, nil
	}
	return backend.Status{
		State:  backend.StateInstalledNotRunning,
		Host:   b.host(),
		Detail: "found at " + path,
		Env:    env,
	}, nil
}

// Models lists what /api/tags reports: every model Ollama has downloaded,
// whether or not it is currently loaded.
func (b *Backend) Models(ctx context.Context) ([]backend.Installed, error) {
	c := b.client(15 * time.Second)
	t, err := c.tags(ctx)
	if err != nil {
		return nil, fmt.Errorf("ollama: listing installed models: %w", err)
	}
	out := make([]backend.Installed, 0, len(t.Models))
	for _, m := range t.Models {
		name := m.Model
		if name == "" {
			name = m.Name
		}
		out = append(out, backend.Installed{
			Name:          name,
			Digest:        m.Digest,
			SizeBytes:     m.Size,
			Family:        m.Details.Family,
			ParameterSize: m.Details.ParameterSize,
			Quantization:  m.Details.QuantizationLevel,
			ModifiedAt:    m.ModifiedAt,
		})
	}
	return out, nil
}

// Show returns the metadata steps 4 and 5 need for one installed model.
func (b *Backend) Show(ctx context.Context, name string) (backend.ModelInfo, error) {
	if strings.TrimSpace(name) == "" {
		return backend.ModelInfo{}, errors.New("ollama: Show: name is required")
	}
	c := b.client(15 * time.Second)
	s, err := c.show(ctx, name)
	if err != nil {
		return backend.ModelInfo{}, fmt.Errorf("ollama: reading %s: %w", name, err)
	}
	arch := archOf(s.ModelInfo)
	info := backend.ModelInfo{
		Name:          name,
		Architecture:  arch,
		Family:        s.Details.Family,
		Families:      s.Details.Families,
		ParameterSize: s.Details.ParameterSize,
		Quantization:  s.Details.QuantizationLevel,
		Capabilities:  s.Capabilities,
		Details:       s.ModelInfo,
		Modelfile:     s.Modelfile,
		Template:      s.Template,
		Parameters:    s.Parameters,
	}
	if n, ok := miNum(s.ModelInfo, arch, "context_length"); ok {
		info.ContextLength = int(n)
	}
	return info, nil
}

// Running lists /api/ps: what Ollama currently has loaded in memory.
func (b *Backend) Running(ctx context.Context) ([]backend.Loaded, error) {
	c := b.client(15 * time.Second)
	p, err := c.ps(ctx)
	if err != nil {
		return nil, fmt.Errorf("ollama: listing running models: %w", err)
	}
	out := make([]backend.Loaded, 0, len(p.Models))
	for _, m := range p.Models {
		name := m.Model
		if name == "" {
			name = m.Name
		}
		out = append(out, backend.Loaded{
			Name:          name,
			SizeBytes:     m.Size,
			SizeVRAMBytes: m.SizeVRAM,
			ContextLength: m.ContextLength,
			ExpiresAt:     m.ExpiresAt,
		})
	}
	return out, nil
}

// Pull fetches an Ollama library tag. Ollama has no notion of a raw Hugging
// Face GGUF file; a source of that kind is ErrUnsupportedSource, not a
// best-effort translation (the interface's own warning about this seam).
func (b *Backend) Pull(ctx context.Context, src backend.ModelSource, progress func(backend.PullProgress)) error {
	if src.Kind != backend.SourceOllamaTag {
		return fmt.Errorf("%w: ollama pulls ollama_tag sources, got %q", backend.ErrUnsupportedSource, src.Kind)
	}
	if err := src.Validate(); err != nil {
		return err
	}
	c := b.client(0) // no per-request timeout: a multi-GB pull legitimately takes a while; ctx still bounds it
	return c.pull(ctx, src.OllamaTag, func(status string, completed, total int64) {
		if progress != nil {
			progress(backend.PullProgress{Status: status, Completed: completed, Total: total})
		}
	})
}

// Generate runs one completion. Used by the benchmark harness (step 6)
// only (backend.Backend's doc comment) — the advisor calls no LLM to do its
// own job (D-8).
func (b *Backend) Generate(ctx context.Context, req backend.GenerateRequest, onEvent func(backend.GenerateEvent) error) error {
	if strings.TrimSpace(req.Model) == "" {
		return errors.New("ollama: Generate: Model is required")
	}
	c := b.client(0)
	return c.generate(ctx, req.Model, req.Prompt, req.System, req.KeepAlive, req.Options, func(l genLine) error {
		if onEvent == nil {
			return nil
		}
		return onEvent(backend.GenerateEvent{
			Response:           l.Response,
			Done:               l.Done,
			DoneReason:         l.DoneReason,
			TotalDuration:      time.Duration(l.TotalDuration),
			LoadDuration:       time.Duration(l.LoadDuration),
			PromptEvalCount:    l.PromptEvalCount,
			PromptEvalDuration: time.Duration(l.PromptEvalDuration),
			EvalCount:          l.EvalCount,
			EvalDuration:       time.Duration(l.EvalDuration),
		})
	})
}

// Unload asks Ollama to free a model's memory now: a generate call with
// keep_alive: 0, exactly as Ollama's docs describe (there is no dedicated
// unload endpoint).
func (b *Backend) Unload(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("ollama: Unload: name is required")
	}
	c := b.client(15 * time.Second)
	if err := c.unload(ctx, name); err != nil {
		return fmt.Errorf("ollama: unloading %s: %w", name, err)
	}
	return nil
}

// setSupervisedLog records where Start wrote this Ollama process's output,
// for runtimePaths to read in preference to guessing at a default log
// location. Exported to the package only (install_*.go calls it).
func (b *Backend) setSupervisedLog(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.supervisedLog = path
}

func (b *Backend) getSupervisedLog() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.supervisedLog
}

// parseKeepAlive is used by callers building GenerateRequest.KeepAlive from
// a duration, kept here so both step 3's manual status line and step 6's
// benchmark harness format it the same way Ollama accepts.
func parseKeepAlive(d time.Duration) string {
	if d < 0 {
		return "-1"
	}
	return strconv.Itoa(int(d.Seconds())) + "s"
}

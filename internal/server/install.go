package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"advisor/internal/backend"
)

// Ollama install/start (build-plan step 7's Ollama screen, product rule 5
// — the button says what it will do and what it costs before it runs):
//
//	GET  /api/backends/{name}/install-size   the download's size, before Install ever runs
//	POST /api/backends/{name}/install        start it; 409 while one is already running for this name
//	GET  /api/backends/{name}/install        the latest status; the UI polls this (no SSE — a short,
//	                                          one-viewer action, unlike a benchmark run)
//	POST /api/backends/{name}/start          launch the runtime; the UI then polls GET /api/backends
//	                                          until Detect reports it running
//
// Neither is written to the store: D-13's history tables are evidence the
// app is built on (a benchmark result, a hardware profile), and an install
// is not that — it is a short, one-time, foreground action. If the daemon
// itself restarts mid-install the user just clicks the button again;
// Detect() and Models() tell the truth about what actually happened
// either way.

const (
	installTimeout = 10 * time.Minute // a few hundred MB on a slow line
	startTimeout   = 15 * time.Second // launching a process, not downloading one
)

// InstallSizeResponse is GET /api/backends/{name}/install-size.
type InstallSizeResponse struct {
	// Bytes is a fact read with a HEAD request against the download host
	// (internal/backend.InstallSizer) — not the advisor's own estimate.
	Bytes int64 `json:"bytes" source:"n/a"`
	Known bool  `json:"known"`
}

// InstallStatus is POST and GET /api/backends/{name}/install.
type InstallStatus struct {
	Backend string `json:"backend"`
	Status  string `json:"status"` // "idle" | "running" | "done" | "failed"
	Message string `json:"message,omitempty"`
	// Completed/Total are read live from the download's own byte counter
	// (backend.InstallProgress) — a fact, not the advisor's estimate.
	Completed int64  `json:"completed_bytes" source:"n/a"`
	Total     int64  `json:"total_bytes,omitempty" source:"n/a"`
	Error     string `json:"error,omitempty"`
}

// BackendStartResponse is POST /api/backends/{name}/start.
type BackendStartResponse struct {
	Backend string `json:"backend"`
	Status  string `json:"status"` // "starting"
}

// installTracker holds one in-flight (or last-finished) install per
// backend name, in memory only (see the package doc above).
type installTracker struct {
	mu     sync.Mutex
	byName map[string]InstallStatus
}

func (t *installTracker) status(name string) InstallStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.byName[name]; ok {
		return s
	}
	return InstallStatus{Backend: name, Status: "idle"}
}

// start runs fn in the background unless an install for name is already
// running, in which case it returns the running status and false.
func (t *installTracker) start(name string, fn func(ctx context.Context, update func(InstallStatus))) (InstallStatus, bool) {
	t.mu.Lock()
	if t.byName == nil {
		t.byName = map[string]InstallStatus{}
	}
	if cur, ok := t.byName[name]; ok && cur.Status == "running" {
		t.mu.Unlock()
		return cur, false
	}
	snapshot := InstallStatus{Backend: name, Status: "running"}
	t.byName[name] = snapshot
	t.mu.Unlock()

	update := func(v InstallStatus) {
		v.Backend = name
		t.mu.Lock()
		t.byName[name] = v
		t.mu.Unlock()
	}
	go fn(context.Background(), update)
	return snapshot, true
}

// backendByName finds a registered backend by its Name(), or writes a 404
// and reports false.
func (s *Server) backendByName(w http.ResponseWriter, r *http.Request) (backend.Backend, bool) {
	name := r.PathValue("name")
	for _, b := range s.backendList() {
		if b.Name() == name {
			return b, true
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "no such runtime: "+name)
	return nil, false
}

func (s *Server) handleBackendInstallSize(w http.ResponseWriter, r *http.Request) {
	b, ok := s.backendByName(w, r)
	if !ok {
		return
	}
	sizer, ok := b.(backend.InstallSizer)
	if !ok {
		writeJSON(w, http.StatusOK, InstallSizeResponse{Known: false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	n, known, err := sizer.InstallSize(ctx)
	if err != nil {
		// Not knowing the size is not a failure the user needs to see —
		// the button says "size unknown" and still works (D-21).
		s.log.Warn("install size", "backend", b.Name(), "err", err)
		writeJSON(w, http.StatusOK, InstallSizeResponse{Known: false})
		return
	}
	writeJSON(w, http.StatusOK, InstallSizeResponse{Bytes: n, Known: known})
}

func (s *Server) handleBackendInstallStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.installs.status(r.PathValue("name")))
}

func (s *Server) handleBackendInstallStart(w http.ResponseWriter, r *http.Request) {
	b, ok := s.backendByName(w, r)
	if !ok {
		return
	}
	status, started := s.installs.start(b.Name(), func(ctx context.Context, update func(InstallStatus)) {
		ctx, cancel := context.WithTimeout(ctx, installTimeout)
		defer cancel()
		err := b.Install(ctx, func(p backend.InstallProgress) {
			update(InstallStatus{Status: "running", Message: p.Status, Completed: p.Completed, Total: p.Total})
		})
		if err != nil {
			update(InstallStatus{Status: "failed", Error: err.Error()})
			return
		}
		update(InstallStatus{Status: "done", Message: "installed"})
	})
	if !started {
		writeError(w, http.StatusConflict, "install_running", "an install for "+b.Name()+" is already running")
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) handleBackendStart(w http.ResponseWriter, r *http.Request) {
	b, ok := s.backendByName(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), startTimeout)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "start_failed", b.Name()+" could not be started: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, BackendStartResponse{Backend: b.Name(), Status: "starting"})
}

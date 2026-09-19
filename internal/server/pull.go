package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"advisor/internal/backend"
)

// A model pull (build-plan step 7's "Getting it" screen):
//
//	POST /api/models/pull          start pulling an Ollama tag; 409 while one is already running
//	GET  /api/models/pull          the latest status; the UI polls this (no SSE — the same call as install.go)
//	POST /api/models/pull/cancel   stop it
//
// One at a time, system-wide — the same shape bench.Harness enforces for
// a benchmark run, and for the same reason: Ollama itself only usefully
// does one pull at a time. Not persisted (install.go's reasoning applies
// here too).

// pullCancelWait bounds how long POST /api/models/pull/cancel waits for
// the pull to actually stop before answering with the status as it
// stands — mirrors bench.go's cancelWait, much shorter because a pull has
// no model to unload afterwards.
const pullCancelWait = 10 * time.Second

// PullRequest is POST /api/models/pull.
type PullRequest struct {
	// OllamaTag is the only source kind the MVP's one backend can fetch
	// (backend.SourceOllamaTag) — recommend.Recommendation.PullName is
	// exactly this string, so the "Download" button can send it straight
	// through.
	OllamaTag string `json:"ollama_tag"`
}

// PullStatus is POST and GET /api/models/pull.
type PullStatus struct {
	Model   string `json:"model,omitempty"`
	Status  string `json:"status"` // "idle" | "running" | "done" | "failed" | "cancelled"
	Message string `json:"message,omitempty"`
	// Completed/Total are read live from Ollama's own pull progress
	// (backend.PullProgress) — a fact, not the advisor's estimate.
	Completed int64  `json:"completed_bytes" source:"n/a"`
	Total     int64  `json:"total_bytes,omitempty" source:"n/a"`
	Error     string `json:"error,omitempty"`
}

// pullTracker holds the one in-flight (or last-finished) pull, in memory
// only.
type pullTracker struct {
	mu     sync.Mutex
	status PullStatus
	cancel context.CancelFunc
	done   chan struct{}
}

func (t *pullTracker) current() PullStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status.Status == "" {
		return PullStatus{Status: "idle"}
	}
	return t.status
}

// start runs fn in the background unless a pull is already running, in
// which case it returns the running status and false.
func (t *pullTracker) start(model string, fn func(ctx context.Context, update func(PullStatus))) (PullStatus, bool) {
	t.mu.Lock()
	if t.status.Status == "running" {
		s := t.status
		t.mu.Unlock()
		return s, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.cancel = cancel
	t.done = done
	t.status = PullStatus{Model: model, Status: "running"}
	snapshot := t.status
	t.mu.Unlock()

	update := func(v PullStatus) {
		if v.Model == "" {
			v.Model = model
		}
		t.mu.Lock()
		t.status = v
		t.mu.Unlock()
	}
	go func() {
		defer close(done)
		fn(ctx, update)
	}()
	return snapshot, true
}

// cancelRunning cancels the running pull's context and waits (briefly)
// for it to actually stop before returning the status as it stands.
// ok is false when nothing was running to cancel.
func (t *pullTracker) cancelRunning(ctx context.Context) (PullStatus, bool) {
	t.mu.Lock()
	if t.status.Status != "running" || t.cancel == nil {
		s := t.status
		t.mu.Unlock()
		return s, false
	}
	cancel, done := t.cancel, t.done
	t.mu.Unlock()

	cancel()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return t.current(), true
}

func (s *Server) handlePullStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.pulls.current())
}

func (s *Server) pullBackend(w http.ResponseWriter) (backend.Backend, bool) {
	all := s.backendList()
	if len(all) == 0 {
		writeError(w, http.StatusServiceUnavailable, "backend_not_running", "no runtime is available to download with")
		return nil, false
	}
	return all[0], true // one runtime in the MVP, the same choice bench.go makes
}

func (s *Server) handlePullStart(w http.ResponseWriter, r *http.Request) {
	var req PullRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", `the request must be JSON: {"ollama_tag": "llama3.1:8b"}`)
		return
	}
	tag := strings.TrimSpace(req.OllamaTag)
	if tag == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "ollama_tag must not be empty")
		return
	}
	b, ok := s.pullBackend(w)
	if !ok {
		return
	}
	status, started := s.pulls.start(tag, func(ctx context.Context, update func(PullStatus)) {
		err := b.Pull(ctx, backend.ModelSource{Kind: backend.SourceOllamaTag, OllamaTag: tag}, func(p backend.PullProgress) {
			update(PullStatus{Status: "running", Message: p.Status, Completed: p.Completed, Total: p.Total})
		})
		switch {
		case err == nil:
			update(PullStatus{Status: "done", Message: "downloaded"})
		case errors.Is(err, context.Canceled):
			update(PullStatus{Status: "cancelled"})
		default:
			update(PullStatus{Status: "failed", Error: err.Error()})
		}
	})
	if !started {
		writeError(w, http.StatusConflict, "pull_running", "a download is already running")
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) handlePullCancel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), pullCancelWait)
	defer cancel()
	status, ok := s.pulls.cancelRunning(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no download is running")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

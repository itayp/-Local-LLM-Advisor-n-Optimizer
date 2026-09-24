package server

import (
	"net/http"
	"sync"
	"time"

	"advisor/internal/catalog/refresh"
	"advisor/internal/store"
)

// The model list's state, for every screen that depends on it:
//
//	GET /api/catalog/status   has the list been fetched, is a fetch running and how far it is, and the public scores' one sentence
//
// A screen that needs the list asks this first and, when it has never been
// fetched, offers the one button that fetches it (POST /api/catalog/refresh)
// — so no screen is a dead end on a fresh install, whatever else the
// machine already has (Itay, testing on Windows, 2026-09-24: Ollama and a
// model were there, the list was not, and nothing offered to fetch it).
// While a fetch runs, the screens poll this for the progress bar; the POST
// itself answers only when the refresh is over.

// CatalogStatus is GET /api/catalog/status.
type CatalogStatus struct {
	// Fetched is true once at least one size of the list has been resolved:
	// the list can recommend. False on a fresh install.
	Fetched     bool                `json:"fetched"`
	LastRefresh *CatalogRefreshInfo `json:"last_refresh,omitempty"`
	// Running is true while a refresh is in progress; Progress says how far.
	Running  bool             `json:"running"`
	Progress *CatalogProgress `json:"progress,omitempty"`
	// PublicFetched is true once any public benchmark source has been read
	// successfully; PublicUpdated is the sentence the detail view shows
	// ("Public scores last updated …", and any source whose last check failed).
	PublicFetched bool   `json:"public_fetched"`
	PublicUpdated string `json:"public_updated"`
	// PublicRunning is true while the public scores download in the
	// background; PublicProgress says which source and what of it.
	PublicRunning  bool             `json:"public_running"`
	PublicProgress *CatalogProgress `json:"public_progress,omitempty"`
}

// CatalogProgress is where a running refresh is.
type CatalogProgress struct {
	Phase     string `json:"phase"`              // "models" (the list's descriptions) | "public" (the public scores)
	Message   string `json:"message"`            // what is being read now, in words
	Done      int    `json:"done" source:"n/a"`  // parts of this phase finished; a count
	Total     int    `json:"total" source:"n/a"` // parts in this phase; a count
	StartedAt string `json:"started_at"`         // RFC 3339, when the refresh began
}

// refreshProgress is the running refresh's progress, in memory only.
type refreshProgress struct {
	mu      sync.Mutex
	running bool
	p       CatalogProgress
}

func (rp *refreshProgress) begin() {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.running = true
	rp.p = CatalogProgress{Phase: refresh.PhaseModels, Message: "Starting", StartedAt: time.Now().UTC().Format(time.RFC3339)}
}

func (rp *refreshProgress) update(p refresh.Progress) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	msg := "Reading the description of " + p.Current
	switch {
	case p.Current == "":
		msg = "Starting"
	case p.Phase == refresh.PhasePublic:
		msg = "Reading public scores from " + p.Current
	}
	rp.p.Phase, rp.p.Message, rp.p.Done, rp.p.Total = p.Phase, msg, p.Done, p.Total
}

func (rp *refreshProgress) end() {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.running = false
}

func (rp *refreshProgress) snapshot() (CatalogProgress, bool) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.p, rp.running
}

// handleCatalogStatus is GET /api/catalog/status.
func (s *Server) handleCatalogStatus(w http.ResponseWriter, r *http.Request) {
	out := CatalogStatus{PublicUpdated: "Public scores have not been fetched yet."}
	if p, running := s.cat.progress.snapshot(); running {
		out.Running, out.Progress = true, &p
	}
	if p, running := s.cat.public.snapshot(); running {
		out.PublicRunning, out.PublicProgress = true, &p
	}
	if s.store == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	rows, err := s.store.CatalogModels(r.Context(), false)
	if err != nil {
		s.log.Error("reading the catalogue", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "the model list could not be read")
		return
	}
	out.Fetched = catalogFetched(rows)
	if latest, err := s.store.LatestCatalogRefresh(r.Context()); err == nil {
		out.LastRefresh = &CatalogRefreshInfo{
			StartedAt: latest.StartedAt, FinishedAt: latest.FinishedAt, Trigger: latest.Trigger,
			Sizes: latest.Sizes, Resolved: latest.Resolved,
		}
	}
	if view, err := s.publicView(r.Context(), rows); err == nil && view != nil {
		out.PublicUpdated = view.Updated()
	}
	if states, err := s.store.ExternalStates(r.Context()); err == nil {
		for _, st := range states {
			if st.OKAt != "" {
				out.PublicFetched = true
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// catalogFetched reports whether any present size has resolved files.
func catalogFetched(rows []store.CatalogModelRow) bool {
	for _, row := range rows {
		if row.Model.Present && row.Model.RefreshedAt != "" && len(row.Model.Files) > 0 {
			return true
		}
	}
	return false
}

package server

import (
	"context"
	"net/http"

	"advisor/internal/backend"
	"advisor/internal/catalog/refresh"
	"advisor/internal/hardware"
	"advisor/internal/store"
)

// BackendInfo is one runtime's most recent check, as GET /api/backends
// serves it: backend.Status flattened with what the history store adds
// (InstalledVersion — what this app itself installed, which Detect alone
// cannot know) and a CheckedAt so the UI can say how fresh this is. State
// is the field that answers step 3 item 4's requirement directly:
// "installed but not running" (backend.StateInstalledNotRunning) and "not
// installed" (backend.StateNotInstalled) are different values, not a
// boolean plus a guess.
type BackendInfo struct {
	Name             string                       `json:"name"`
	State            backend.State                `json:"state"`
	Version          string                       `json:"version,omitempty"`
	Host             string                       `json:"host,omitempty"`
	InstalledVersion string                       `json:"installed_version,omitempty"`
	RuntimePaths     map[int]hardware.RuntimePath `json:"runtime_paths,omitempty"`
	Env              map[string]string            `json:"env,omitempty"`
	Detail           string                       `json:"detail,omitempty"`
	CheckedAt        string                       `json:"checked_at"`
}

// BackendsResponse is GET /api/backends.
type BackendsResponse struct {
	Backends []BackendInfo `json:"backends"`
}

// InstalledModelInfo is one row of GET /api/models/installed: what
// Models() last reported for one backend, from installed_models.
type InstalledModelInfo struct {
	BackendName   string `json:"backend_name"`
	Name          string `json:"name"`
	Digest        string `json:"digest,omitempty"`
	SizeBytes     uint64 `json:"size_bytes" source:"n/a"` // the blob, from the runtime
	Quantization  string `json:"quantization,omitempty"`
	Family        string `json:"family,omitempty"`
	ParameterSize string `json:"parameter_size,omitempty"`
	ModifiedAt    string `json:"modified_at,omitempty"`
	LastSeenAt    string `json:"last_seen_at"`

	// How the model maps onto the curated catalogue (step 4). CatalogMatch
	// is "file" (a known size and quant), "model" (a known size, a quant the
	// catalogue does not track), "unknown" (not in the catalogue — a signal
	// for the curator, not an error), or "" before the first mapping.
	CatalogMatch   string `json:"catalog_match"`
	CatalogModelID int64  `json:"catalog_model_id,omitempty" source:"n/a"` // catalog_models id
	CatalogFileID  int64  `json:"catalog_file_id,omitempty" source:"n/a"`  // catalog_files id
	CatalogNote    string `json:"catalog_note,omitempty"`
}

// InstalledModelsResponse is GET /api/models/installed.
type InstalledModelsResponse struct {
	Models []InstalledModelInfo `json:"models"`
}

// RecordBackends detects every registered backend (internal/backend's
// package-level registry — "ollama" today) and, for one that is running,
// refreshes its inventory into installed_models. main calls this once in
// the background on startup, the same shape as RecordHardware; GET
// /api/backends calls it again on every request, which is what makes it
// "on demand" per step 3 item 4 — Detect is documented to be cheap and to
// never start anything, so re-checking on every page load is the point,
// not a cost to avoid.
func (s *Server) RecordBackends(ctx context.Context) BackendsResponse {
	all := s.backendList()
	out := make([]BackendInfo, 0, len(all))
	for _, b := range all {
		out = append(out, s.recordOneBackend(ctx, b))
	}
	return BackendsResponse{Backends: out}
}

func (s *Server) recordOneBackend(ctx context.Context, b backend.Backend) BackendInfo {
	name := b.Name()
	status, err := b.Detect(ctx)
	if err != nil {
		// A real failure, not "not installed" (that is a Status.State
		// value, not an error) — D-21: report what is actually known
		// (the last stored check, if any) rather than guessing a state.
		s.log.Warn("detecting backend", "backend", name, "err", err)
		if s.store != nil {
			if prev, perr := s.store.LatestBackend(ctx, name); perr == nil {
				return backendInfoFromRow(prev, "could not be re-checked just now: "+err.Error())
			}
		}
		return BackendInfo{Name: name, Detail: "could not be checked: " + err.Error(), CheckedAt: store.Now()}
	}

	installedVersion := ""
	if s.store != nil {
		if prev, perr := s.store.LatestBackend(ctx, name); perr == nil {
			installedVersion = prev.InstalledVersion
		}
	}

	// Refresh the inventory whenever the runtime is reachable enough to ask
	// — the whole point of item 4 is that installed_models stays current
	// without the user doing anything.
	if status.State == backend.StateRunning {
		if models, merr := b.Models(ctx); merr != nil {
			s.log.Warn("listing installed models", "backend", name, "err", merr)
		} else if s.store != nil {
			if uerr := s.store.UpsertInstalledModels(ctx, name, models); uerr != nil {
				s.log.Error("storing installed models", "backend", name, "err", uerr)
			} else if _, merr := refresh.MapInstalled(ctx, s.store); merr != nil {
				// The inventory is stored; only its catalogue mapping is stale.
				s.log.Error("mapping installed models to the catalogue", "err", merr)
			}
		}
	}

	if s.store == nil {
		return BackendInfo{
			Name: name, State: status.State, Version: status.Version, Host: status.Host,
			InstalledVersion: installedVersion, RuntimePaths: status.RuntimePaths, Env: status.Env,
			Detail: status.Detail, CheckedAt: store.Now(),
		}
	}
	// The check is attributed to the hardware it ran on, once that is known,
	// so that a runtime path seen on this machine is never applied to a
	// different graphics card later (migration 0004).
	fingerprint := ""
	select {
	case <-s.hw.ready:
		if s.hw.err == nil {
			fingerprint = s.hw.resp.Fingerprint
		}
	default:
	}
	row, rerr := s.store.RecordBackendOn(ctx, fingerprint, name, status, installedVersion)
	if rerr != nil {
		// The check itself is still good; only its history row is missing
		// (the same trade-off RecordHardware makes).
		s.log.Error("storing backend check", "backend", name, "err", rerr)
		return BackendInfo{
			Name: name, State: status.State, Version: status.Version, Host: status.Host,
			InstalledVersion: installedVersion, RuntimePaths: status.RuntimePaths, Env: status.Env,
			Detail: status.Detail, CheckedAt: store.Now(),
		}
	}
	return backendInfoFromRow(row, row.Detail)
}

func backendInfoFromRow(row store.BackendRow, detail string) BackendInfo {
	return BackendInfo{
		Name: row.Name, State: row.State, Version: row.Version, Host: row.Host,
		InstalledVersion: row.InstalledVersion, RuntimePaths: row.RuntimePaths, Env: row.Env,
		Detail: detail, CheckedAt: row.CreatedAt,
	}
}

func (s *Server) handleBackends(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.RecordBackends(r.Context()))
}

// handleModelsInstalled serves the whole present inventory, or one
// backend's with ?backend=name.
func (s *Server) handleModelsInstalled(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, InstalledModelsResponse{Models: []InstalledModelInfo{}})
		return
	}
	var rows []store.InstalledModelRow
	var err error
	if name := r.URL.Query().Get("backend"); name != "" {
		rows, err = s.store.InstalledModels(r.Context(), name)
	} else {
		rows, err = s.store.AllInstalledModels(r.Context())
	}
	if err != nil {
		s.log.Error("reading installed models", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "installed models could not be read")
		return
	}
	out := InstalledModelsResponse{Models: make([]InstalledModelInfo, 0, len(rows))}
	for _, row := range rows {
		out.Models = append(out.Models, installedModelInfoFromRow(row))
	}
	writeJSON(w, http.StatusOK, out)
}

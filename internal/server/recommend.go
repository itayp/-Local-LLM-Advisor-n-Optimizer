package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/estimate"
	"advisor/internal/hardware"
	"advisor/internal/recommend"
	"advisor/internal/store"
)

// ModelFitResponse is GET /api/models/{id}/fit: how every tracked variant of
// one catalogue size would do on this machine at one context.
type ModelFitResponse struct {
	FamilyID    string        `json:"family_id"`
	DisplayName string        `json:"display_name"`
	Model       catalog.Model `json:"model"` // the size, without its file list (each fit carries its file)
	// NumCtx is the context the fits are for; NumCtxSource says where it came
	// from: "requested" (?ctx=), or "ollama_default" — what Ollama runs a
	// model at on this machine when nobody set one. Configuration.
	NumCtx       int                  `json:"num_ctx" source:"n/a"`
	NumCtxSource string               `json:"num_ctx_source"`
	KVCacheType  estimate.KVCacheType `json:"kv_cache_type"`
	// RuntimePath and PathSource say what the numbers were built for; GPUNotUsed
	// is set when a graphics card exists and they are without it.
	RuntimePath string              `json:"runtime_path"`
	PathSource  estimate.PathSource `json:"path_source"`
	GPUNotUsed  *estimate.UnusedGPU `json:"gpu_not_used,omitempty"`
	Fits        []FileFit           `json:"fits"` // smallest file first
}

// FileFit is one weights file's estimate.
type FileFit struct {
	File catalog.File `json:"file"`
	// Default marks the file the size's Ollama tag pulls — the one a
	// recommendation would name.
	Default  bool              `json:"default"`
	Estimate estimate.Estimate `json:"estimate"`
}

// machine assembles what the estimator plans against: this start's hardware
// profile, and the runtime path the backend was last SEEN taking on this
// hardware (ARCHITECTURE.md D-5, D-31). It waits for detection like GET
// /api/hardware does.
func (s *Server) machine(w http.ResponseWriter, r *http.Request) (estimate.Machine, bool) {
	select {
	case <-s.hw.ready:
	case <-r.Context().Done():
		return estimate.Machine{}, false
	case <-time.After(hardwareWait):
		writeError(w, http.StatusServiceUnavailable, "detecting", "still reading this computer; try again in a moment")
		return estimate.Machine{}, false
	}
	if s.hw.err != nil {
		writeError(w, http.StatusServiceUnavailable, "detection_failed", "this computer could not be read: "+s.hw.err.Error())
		return estimate.Machine{}, false
	}
	m := estimate.Machine{Profile: s.hw.resp.Profile}
	if s.store != nil {
		m.ActualPath, m.RuntimeEnv = s.observedPath(r.Context(), s.hw.resp.Fingerprint)
	}
	return m, true
}

// observedPath is the path the runtime took for the primary graphics device
// the last time a check caught it with a model loaded, on this hardware. The
// runtimes are checked afresh first (Detect is cheap and starts nothing —
// D-30), so a model the user loaded a minute ago in their chat app is what
// the recommendation is built on, without a visit to another screen.
func (s *Server) observedPath(ctx context.Context, fingerprint string) (hardware.RuntimePath, map[string]string) {
	s.RecordBackends(ctx)
	var path hardware.RuntimePath
	var env map[string]string
	for _, b := range s.backendList() {
		if row, err := s.store.LatestBackend(ctx, b.Name()); err == nil {
			env = row.Env
		}
		row, err := s.store.LastObservedRuntimePaths(ctx, b.Name(), fingerprint)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("reading the last observed runtime path", "backend", b.Name(), "err", err)
			}
			continue
		}
		if p, ok := row.RuntimePaths[0]; ok {
			path = p
		}
		break // one runtime in the MVP; the first that has been seen decides
	}
	return path, env
}

// catalogueEntries is the stored catalogue in the shape the engine reads.
func (s *Server) catalogueEntries(ctx context.Context) ([]recommend.Entry, error) {
	entries, _, err := s.catalogue(ctx)
	return entries, err
}

// catalogue is the stored catalogue in the shape the engine reads, and the
// rows it came from.
func (s *Server) catalogue(ctx context.Context) ([]recommend.Entry, []store.CatalogModelRow, error) {
	rows, err := s.store.CatalogModels(ctx, false)
	if err != nil {
		return nil, nil, err
	}
	out := make([]recommend.Entry, 0, len(rows))
	for _, row := range rows {
		out = append(out, recommend.Entry{FamilyID: row.Model.FamilyID, DisplayName: row.DisplayName, Purposes: row.Purposes, Model: row.Model})
	}
	return out, rows, nil
}

// handleRecommend is GET /api/recommend?purposes=coding,chat — at most three
// recommendations for this machine (build-plan step 5). Optional: current=
// the installed model to compare with, min_context=, gpu_only=1,
// allow_split=1.
func (s *Server) handleRecommend(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var purposes []catalog.Purpose
	for _, raw := range q["purposes"] {
		for _, part := range strings.Split(raw, ",") {
			if part = strings.TrimSpace(part); part == "" {
				continue
			}
			p := catalog.Purpose(part)
			if !p.Valid() {
				writeError(w, http.StatusBadRequest, "bad_purpose", "unknown purpose \""+part+"\"; the purposes are "+joinPurposes())
				return
			}
			purposes = append(purposes, p)
		}
	}
	prefs := recommend.Preferences{
		CurrentModel: strings.TrimSpace(q.Get("current")),
		GPUOnly:      truthy(q.Get("gpu_only")),
		AllowSplit:   truthy(q.Get("allow_split")),
	}
	if v := q.Get("min_context"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxContext {
			writeError(w, http.StatusBadRequest, "bad_context", "min_context must be a number of tokens")
			return
		}
		prefs.MinContext = n
	}
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "no_store", "recommendations need the model list, which is not available")
		return
	}
	m, ok := s.machine(w, r)
	if !ok {
		return
	}
	entries, rows, err := s.catalogue(r.Context())
	if err != nil {
		s.log.Error("reading the catalogue", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "the model list could not be read")
		return
	}
	// Public quality signals (build-plan step 9b). Unreadable public data
	// never blocks a recommendation: it is logged and left out.
	view, err := s.publicView(r.Context(), rows)
	if err != nil {
		s.log.Warn("reading public data; recommending without it", "err", err)
	}
	installedRows, err := s.store.AllInstalledModels(r.Context())
	if err != nil {
		s.log.Error("reading installed models", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "installed models could not be read")
		return
	}
	installed := make([]recommend.InstalledModel, 0, len(installedRows))
	for _, row := range installedRows {
		installed = append(installed, recommend.InstalledModel{
			Name: row.Name, SizeBytes: row.SizeBytes, ParameterSize: row.ParameterSize,
			CatalogModelID: row.CatalogModelID, CatalogFileID: row.CatalogFileID,
		})
	}
	engine, err := recommend.New(entries)
	if err != nil {
		s.log.Error("building the recommendation engine", "err", err)
		writeError(w, http.StatusInternalServerError, "estimator", "the advisor's own data could not be read: "+err.Error())
		return
	}
	// What this computer's own benchmarks measured: exact measurements
	// replace their configurations' estimates, the rest calibrate the speed
	// ranges (build-plan step 6, item 6).
	engine.Measurements = s.evidence(r.Context(), m, engine.Estimator)
	if view != nil {
		// Public data reaches the purpose-fit term only (recommend.Config.
		// ExternalWeight), and each card one "Public data" line of its own.
		engine.Public = view.Scoring()
	}
	res := engine.Recommend(m, purposes, installed, prefs)
	if view != nil {
		for i := range res.Recommendations {
			res.Recommendations[i].Public = view.Line(res.Recommendations[i].Model.ID, res.Purposes)
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// maxContext bounds ?ctx= and ?min_context=: above any trained context in
// the catalogue, below anything that would overflow the arithmetic.
const maxContext = 1 << 22

// fitError is why a fit could not be answered, for writeError.
type fitError struct {
	status      int
	code, words string
}

// handleModelFit is GET /api/models/{id}/fit?ctx=8192&kv=f16 — {id} is a
// catalogue size (the "id" of a size in GET /api/catalog). Without ctx the
// fits are for Ollama's own default context on this machine.
func (s *Server) handleModelFit(w http.ResponseWriter, r *http.Request) {
	resp, _, ferr := s.modelFit(w, r)
	if ferr != nil {
		if ferr.status != 0 {
			writeError(w, ferr.status, ferr.code, ferr.words)
		}
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// modelFit answers /fit for the size in the path, and returns the stored
// catalogue rows it read. A fitError with status 0 means the response has
// already been written (hardware still detecting) or the request is gone.
func (s *Server) modelFit(w http.ResponseWriter, r *http.Request) (ModelFitResponse, []store.CatalogModelRow, *fitError) {
	var resp ModelFitResponse
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return resp, nil, &fitError{http.StatusBadRequest, "bad_id", "the model id must be a positive number"}
	}
	q := r.URL.Query()
	kv := estimate.KVF16
	if v := q.Get("kv"); v != "" {
		if kv = estimate.KVCacheType(v); !kv.Valid() {
			return resp, nil, &fitError{http.StatusBadRequest, "bad_kv", "kv must be f16, q8_0 or q4_0"}
		}
	}
	ctx, ctxSource := 0, "ollama_default"
	if v := q.Get("ctx"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 256 || n > maxContext {
			return resp, nil, &fitError{http.StatusBadRequest, "bad_context", "ctx must be a number of tokens, 256 or more"}
		}
		ctx, ctxSource = n, "requested"
	}
	if s.store == nil {
		return resp, nil, &fitError{http.StatusServiceUnavailable, "no_store", "the model list is not available"}
	}
	m, ok := s.machine(w, r)
	if !ok {
		return resp, nil, &fitError{}
	}
	entries, rows, err := s.catalogue(r.Context())
	if err != nil {
		s.log.Error("reading the catalogue", "err", err)
		return resp, nil, &fitError{http.StatusInternalServerError, "store", "the model list could not be read"}
	}
	var entry *recommend.Entry
	for i := range entries {
		if entries[i].Model.ID == id {
			entry = &entries[i]
		}
	}
	if entry == nil {
		return resp, nil, &fitError{http.StatusNotFound, "not_found", "no model with that id in the list"}
	}
	est, err := estimate.New()
	if err != nil {
		s.log.Error("building the estimator", "err", err)
		return resp, nil, &fitError{http.StatusInternalServerError, "estimator", "the advisor's own data could not be read: " + err.Error()}
	}
	measured := s.evidence(r.Context(), m, est)
	pl := est.Place(m)
	if ctx == 0 {
		ctx = estimate.OllamaDefaultContext(pl)
	}

	model := entry.Model
	var projector *catalog.File
	for i := range model.Files {
		if f := model.Files[i]; f.Present && f.Role == catalog.RoleProjector {
			projector = &f
			break
		}
	}
	defaultQuants := recommend.DefaultConfig().DefaultQuants
	if model.Size.OllamaQuant != "" {
		defaultQuants = append([]string{model.Size.OllamaQuant}, defaultQuants...)
	}
	resp = ModelFitResponse{
		FamilyID: entry.FamilyID, DisplayName: entry.DisplayName, NumCtx: ctx, NumCtxSource: ctxSource, KVCacheType: kv,
		RuntimePath: string(pl.Path), PathSource: pl.PathSource, GPUNotUsed: pl.Unused, Fits: []FileFit{},
	}
	defaultFound := false
	for _, f := range model.Files {
		if !f.Present || f.Role != catalog.RoleModel {
			continue
		}
		fit := FileFit{File: f, Estimate: est.FitPlaced(pl, m, estimate.Model{File: f, Projector: projector, Size: model.Size}, estimate.Request{NumCtx: ctx, KVCacheType: kv})}
		if meas, ok := measured[recommend.MeasurementKey{CatalogFileID: f.ID, NumCtx: ctx}]; ok && kv == estimate.KVF16 {
			fit.Estimate = fit.Estimate.WithMeasurement(meas)
		}
		resp.Fits = append(resp.Fits, fit)
	}
	for _, want := range defaultQuants {
		for i := range resp.Fits {
			if !defaultFound && strings.EqualFold(resp.Fits[i].File.Quant, want) {
				resp.Fits[i].Default, defaultFound = true, true
			}
		}
	}
	model.Files = []catalog.File{}
	resp.Model = model
	return resp, rows, nil
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func joinPurposes() string {
	parts := make([]string, 0, len(catalog.Purposes))
	for _, p := range catalog.Purposes {
		parts = append(parts, string(p))
	}
	return strings.Join(parts, ", ")
}

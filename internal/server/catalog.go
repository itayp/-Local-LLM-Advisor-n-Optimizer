package server

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"advisor/internal/catalog"
	"advisor/internal/catalog/hf"
	"advisor/internal/catalog/refresh"
	"advisor/internal/store"
	"advisor/internal/version"
)

// CatalogFamily is one curated family with its sizes as the store holds
// them — each size with what the last refresh resolved (its quant files and
// their header fields), or its refresh error.
type CatalogFamily struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"display_name"`
	Maintainer  string            `json:"maintainer"`
	License     catalog.License   `json:"license"`
	Purposes    []catalog.Purpose `json:"purposes"`
	ReviewedAt  string            `json:"reviewed_at"`
	Source      string            `json:"source"`
	Notes       string            `json:"notes,omitempty"`
	Sizes       []catalog.Model   `json:"sizes"`
}

// CatalogRefreshInfo is when the catalogue was last refreshed, by whom, and
// how much of it resolved.
type CatalogRefreshInfo struct {
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Trigger    string `json:"trigger"`
	Sizes      int    `json:"sizes" source:"n/a"`    // a count
	Resolved   int    `json:"resolved" source:"n/a"` // a count
}

// CatalogResponse is GET /api/catalog.
type CatalogResponse struct {
	Quants      []string            `json:"quants"`
	Families    []CatalogFamily     `json:"families"`
	LastRefresh *CatalogRefreshInfo `json:"last_refresh,omitempty"` // absent: never refreshed
}

// UnknownInstalledModel is an installed model the catalogue does not know:
// the curator's signal (build-plan step 4, item 4), not an error.
type UnknownInstalledModel struct {
	BackendName   string `json:"backend_name"`
	Name          string `json:"name"`
	Family        string `json:"family,omitempty"`
	ParameterSize string `json:"parameter_size,omitempty"`
	Quantization  string `json:"quantization,omitempty"`
	Note          string `json:"note"`
}

// UnknownInstalledResponse is GET /api/catalog/unknown.
type UnknownInstalledResponse struct {
	Models []UnknownInstalledModel `json:"models"`
}

// catalogState is what the catalogue endpoints share: where the curated
// list comes from, the Hugging Face client, and the one-refresh-at-a-time
// lock.
type catalogState struct {
	load      func() (*catalog.Catalogue, error) // catalog.Default; tests replace it
	newClient func() *hf.Client                  // hf.New; tests point it at a fake hub
	running   sync.Mutex
}

func (c *catalogState) init() {
	c.load = catalog.Default
	c.newClient = func() *hf.Client { return hf.New(version.UserAgent()) }
}

// ErrRefreshRunning is returned by RefreshCatalog while another refresh is
// in progress.
var ErrRefreshRunning = errors.New("server: a catalogue refresh is already running")

// RefreshCatalog runs a catalogue refresh (build-plan step 4) and returns
// its report. One runs at a time; a second call meanwhile gets
// ErrRefreshRunning rather than a second set of requests to Hugging Face.
func (s *Server) RefreshCatalog(ctx context.Context, trigger string) (refresh.Report, error) {
	if s.store == nil {
		return refresh.Report{}, errors.New("server: no database, nothing to refresh into")
	}
	if !s.cat.running.TryLock() {
		return refresh.Report{}, ErrRefreshRunning
	}
	defer s.cat.running.Unlock()
	cat, err := s.cat.load()
	if err != nil {
		return refresh.Report{}, err
	}
	client := s.cat.newClient()
	client.Log = s.log
	return refresh.Run(ctx, refresh.Options{
		Catalogue: cat, Store: s.store, HF: client, Log: s.log, Trigger: trigger,
	})
}

// handleCatalogRefresh is POST /api/catalog/refresh. The refresh is detached
// from the request: closing the tab does not abandon it half-way (each size
// is stored as it resolves), and the next GET /api/catalog shows the result.
func (s *Server) handleCatalogRefresh(w http.ResponseWriter, r *http.Request) {
	type result struct {
		rep refresh.Report
		err error
	}
	done := make(chan result, 1)
	go func() {
		rep, err := s.RefreshCatalog(context.WithoutCancel(r.Context()), "api")
		done <- result{rep, err}
	}()
	select {
	case <-r.Context().Done():
		return
	case res := <-done:
		switch {
		case errors.Is(res.err, ErrRefreshRunning):
			writeError(w, http.StatusConflict, "refresh_running", "a catalogue refresh is already running; its result will be in GET /api/catalog")
		case res.err != nil:
			s.log.Error("catalogue refresh", "err", res.err)
			writeError(w, http.StatusInternalServerError, "refresh_failed", "the catalogue refresh stopped: "+res.err.Error())
		default:
			writeJSON(w, http.StatusOK, res.rep)
		}
	}
}

// handleCatalog is GET /api/catalog: the curated families in the order
// families.yaml lists them, each size with its resolved files.
func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	cat, err := s.cat.load()
	if err != nil {
		s.log.Error("loading the catalogue", "err", err)
		writeError(w, http.StatusInternalServerError, "catalogue", "the curated catalogue could not be loaded: "+err.Error())
		return
	}
	out := CatalogResponse{Quants: cat.Quants, Families: make([]CatalogFamily, 0, len(cat.Families))}
	stored := map[store.CatalogKey]catalog.Model{}
	if s.store != nil {
		rows, err := s.store.CatalogModels(r.Context(), false)
		if err != nil {
			s.log.Error("reading the catalogue", "err", err)
			writeError(w, http.StatusInternalServerError, "store", "the catalogue could not be read")
			return
		}
		for _, row := range rows {
			stored[store.CatalogKey{FamilyID: row.Model.FamilyID, Parameters: row.Model.Size.Parameters}] = row.Model
		}
		if latest, err := s.store.LatestCatalogRefresh(r.Context()); err == nil {
			out.LastRefresh = &CatalogRefreshInfo{
				StartedAt: latest.StartedAt, FinishedAt: latest.FinishedAt, Trigger: latest.Trigger,
				Sizes: latest.Sizes, Resolved: latest.Resolved,
			}
		}
	}
	for _, f := range cat.Families {
		fam := CatalogFamily{
			ID: f.ID, DisplayName: f.DisplayName, Maintainer: f.Maintainer, License: f.License,
			Purposes: f.Purposes, ReviewedAt: f.ReviewedAt, Source: f.Source, Notes: f.Notes,
			Sizes: make([]catalog.Model, 0, len(f.Sizes)),
		}
		for _, sz := range f.Sizes {
			m, ok := stored[store.CatalogKey{FamilyID: f.ID, Parameters: sz.Parameters}]
			if !ok {
				// Not synced yet (a new build before its first start or
				// refresh): the YAML's own fields, nothing resolved.
				m = catalog.Model{FamilyID: f.ID, Size: sz, Present: true, Files: []catalog.File{}}
			}
			m.Size = sz // the YAML is the schema of record
			fam.Sizes = append(fam.Sizes, m)
		}
		out.Families = append(out.Families, fam)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCatalogUnknown is GET /api/catalog/unknown: installed models the
// catalogue does not know, with a sentence each for the curator.
func (s *Server) handleCatalogUnknown(w http.ResponseWriter, r *http.Request) {
	out := UnknownInstalledResponse{Models: []UnknownInstalledModel{}}
	if s.store == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	rows, err := s.store.UnknownInstalledModels(r.Context())
	if err != nil {
		s.log.Error("reading unknown installed models", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "installed models could not be read")
		return
	}
	for _, row := range rows {
		out.Models = append(out.Models, UnknownInstalledModel{
			BackendName: row.BackendName, Name: row.Name, Family: row.Family,
			ParameterSize: row.ParameterSize, Quantization: row.Quantization, Note: row.CatalogNote,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// SyncCatalogue writes families.yaml's sizes into catalog_models (no
// network), so the catalogue and the installed-model mapping are current
// from the first start of a new build, before any refresh.
func (s *Server) SyncCatalogue(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	cat, err := s.cat.load()
	if err != nil {
		return err
	}
	if _, err := s.store.SyncCatalogModels(ctx, cat); err != nil {
		return err
	}
	_, err = refresh.MapInstalled(ctx, s.store)
	return err
}

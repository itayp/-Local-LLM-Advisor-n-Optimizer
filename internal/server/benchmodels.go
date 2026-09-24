package server

import (
	"net/http"
	"sort"

	"advisor/internal/bench"
	"advisor/internal/estimate"
	"advisor/internal/recommend"
)

// BenchModel is one model the Benchmarks screen can offer to test.
type BenchModel struct {
	// Name is what a test is started with: an installed model's name as the
	// runtime lists it, or — not installed yet — the Ollama tag a download
	// fetches (then the same name once it is installed).
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`          // "Qwen3.5 9B"; "" for an installed model the list does not know
	ModelID     int64  `json:"model_id,omitempty" source:"n/a"` // the catalogue size's id; 0 when the list does not know it
	Installed   bool   `json:"installed"`
	// DownloadBytes is what it costs to get (not installed): the weights file
	// the tag pulls plus the vision encoder, from the list's own listing — a
	// fact, shown as-is.
	DownloadBytes uint64 `json:"download_bytes,omitempty" source:"n/a"`
	// Fit is how the estimator expects it to fit at Ollama's default context
	// (not installed only).
	Fit estimate.Category `json:"fit,omitempty"`
}

// BenchModelsResponse is GET /api/bench/models: what can be tested here.
type BenchModelsResponse struct {
	Installed []BenchModel `json:"installed"` // curated first, then smallest first
	// Available is what the list has that is not installed and would run
	// here, smallest download first — testing one downloads it first.
	Available []BenchModel `json:"available"`
	// CatalogueFetched is false until the model list has been fetched; then
	// Available is empty for want of a list, not for want of room.
	CatalogueFetched bool `json:"catalogue_fetched"`
	// TooBig counts sizes in the list left out of Available because they
	// would not fit this computer; a count.
	TooBig int `json:"too_big" source:"n/a"`
}

// handleBenchModels is GET /api/bench/models. The installed inventory is
// read afresh first (it is cheap and starts nothing), so a model downloaded
// a moment ago — here or in a chat app — is offered straight away.
func (s *Server) handleBenchModels(w http.ResponseWriter, r *http.Request) {
	out := BenchModelsResponse{Installed: []BenchModel{}, Available: []BenchModel{}}
	if s.store == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	s.RecordBackends(r.Context())
	rows, err := s.store.AllInstalledModels(r.Context())
	if err != nil {
		s.log.Error("reading installed models", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "installed models could not be read")
		return
	}
	entries, catRows, err := s.catalogue(r.Context())
	if err != nil {
		s.log.Error("reading the catalogue", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "the model list could not be read")
		return
	}
	out.CatalogueFetched = catalogFetched(catRows)
	byID := map[int64]recommend.Entry{}
	for _, e := range entries {
		byID[e.Model.ID] = e
	}
	installedIDs := map[int64]bool{}
	type inst struct {
		m       BenchModel
		curated bool
		size    uint64
	}
	var ins []inst
	for _, row := range rows {
		if !row.Present {
			continue
		}
		bm := BenchModel{Name: row.Name, Installed: true, ModelID: row.CatalogModelID}
		if e, ok := byID[row.CatalogModelID]; ok && row.CatalogModelID != 0 {
			bm.DisplayName = recommend.DisplayName(e)
			installedIDs[row.CatalogModelID] = true
		}
		ins = append(ins, inst{bm, row.CatalogMatch == "file", row.SizeBytes})
	}
	sort.SliceStable(ins, func(i, j int) bool {
		if ins[i].curated != ins[j].curated {
			return ins[i].curated
		}
		return ins[i].size < ins[j].size
	})
	for _, x := range ins {
		out.Installed = append(out.Installed, x.m)
	}

	if out.CatalogueFetched {
		m, ok := s.machine(w, r)
		if !ok {
			return
		}
		engine, err := recommend.New(entries)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "estimator", "the advisor's own data could not be read: "+err.Error())
			return
		}
		est := engine.Estimator
		pl := est.Place(m)
		ctx := estimate.OllamaDefaultContext(pl)
		for _, e := range entries {
			if installedIDs[e.Model.ID] {
				continue
			}
			file, projector, ok := engine.DefaultFile(e)
			if !ok {
				continue
			}
			fit := est.FitPlaced(pl, m, estimate.Model{File: file, Projector: projector, Size: e.Model.Size}, estimate.Request{NumCtx: ctx, KVCacheType: estimate.KVF16})
			if !testable(fit.Category) {
				out.TooBig++
				continue
			}
			bytes := file.Bytes
			if projector != nil {
				bytes += projector.Bytes
			}
			out.Available = append(out.Available, BenchModel{
				Name: e.Model.Size.OllamaTag, DisplayName: recommend.DisplayName(e), ModelID: e.Model.ID,
				DownloadBytes: bytes, Fit: fit.Category,
			})
		}
		sort.SliceStable(out.Available, func(i, j int) bool { return out.Available[i].DownloadBytes < out.Available[j].DownloadBytes })
	}
	writeJSON(w, http.StatusOK, out)
}

// testable is whether a size is worth offering to download and test: it
// runs here, even if only partly on the graphics or at a shorter context
// (the plan then says so, and asks before a slow test).
func testable(c estimate.Category) bool {
	switch c {
	case estimate.FitsWithHeadroom, estimate.Fits, estimate.ReducedContextOnly, estimate.NeedsCPUOffload:
		return true
	}
	return false
}

// handleBenchProgress is GET /api/bench/{id}/progress: the run's progress
// as one JSON answer — the same thing the stream sends, for a browser whose
// stream does not arrive (something between them buffering it).
func (s *Server) handleBenchProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := benchID(w, r)
	if !ok {
		return
	}
	if s.bench == nil {
		writeError(w, http.StatusNotFound, "not_found", "no test with that id")
		return
	}
	current, _, unsubscribe, live := s.bench.Subscribe(id)
	unsubscribe()
	if !live {
		run, err := s.bench.Get(r.Context(), id, false)
		if err != nil {
			s.writeBenchError(w, err)
			return
		}
		current = bench.Progress{RunID: run.ID, Status: run.Status, Phase: run.Phase, Message: finishedMessage(run), Run: run}
	}
	writeJSON(w, http.StatusOK, current)
}

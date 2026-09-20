package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"advisor/internal/store"
)

// removeTimeout bounds how long a delete may take: it is a local file
// removal on the runtime's side, not a download — generous only because a
// very large model's blob can still take a moment on a slow disk.
const removeTimeout = 30 * time.Second

// ModelRemoveRequest is the body of POST /api/backends/{name}/models/remove.
type ModelRemoveRequest struct {
	Name string `json:"name"`
}

// handleModelRemove is the Models screen's "Remove" button (build-plan
// step 8): the button's own confirm already told the person the GB it
// frees (InstalledModelInfo.SizeBytes, a fact the UI already has — no
// round trip needed to ask again), so this endpoint only does the one
// thing left: delete it, then answer with the inventory as it now stands,
// the same shape GET /api/models/installed serves.
func (s *Server) handleModelRemove(w http.ResponseWriter, r *http.Request) {
	b, ok := s.backendByName(w, r)
	if !ok {
		return
	}
	var req ModelRemoveRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", `the request must be JSON: {"name": "llama3.1:8b"}`)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name must not be empty")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), removeTimeout)
	defer cancel()
	if err := b.Delete(ctx, name); err != nil {
		s.log.Warn("removing a model", "backend", b.Name(), "model", name, "err", err)
		writeError(w, http.StatusInternalServerError, "remove_failed", name+" could not be removed: "+err.Error())
		return
	}
	// Resync the inventory so the answer — and the next GET
	// /api/models/installed — reflects the removal immediately, the same
	// refresh RecordBackends does after every check. Without a store there
	// is nothing to resync into; GET /api/models/installed already answers
	// an empty list in that case (backend.go), so this does too.
	if s.store == nil {
		writeJSON(w, http.StatusOK, InstalledModelsResponse{Models: []InstalledModelInfo{}})
		return
	}
	if models, merr := b.Models(ctx); merr != nil {
		s.log.Warn("re-reading installed models after a removal", "backend", b.Name(), "err", merr)
	} else if uerr := s.store.UpsertInstalledModels(ctx, b.Name(), models); uerr != nil {
		s.log.Error("recording installed models after a removal", "err", uerr)
	}
	resp, err := s.installedModelsResponse(ctx)
	if err != nil {
		s.log.Error("reading installed models after a removal", "err", err)
		writeError(w, http.StatusInternalServerError, "store", name+" was removed, but the installed models list could not be re-read")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// installedModelsResponse is GET /api/models/installed's own payload,
// shared with handleModelRemove so a removal answers with the inventory
// as it now stands in one round trip.
func (s *Server) installedModelsResponse(ctx context.Context) (InstalledModelsResponse, error) {
	rows, err := s.store.AllInstalledModels(ctx)
	if err != nil {
		return InstalledModelsResponse{}, err
	}
	out := InstalledModelsResponse{Models: make([]InstalledModelInfo, 0, len(rows))}
	for _, row := range rows {
		out.Models = append(out.Models, installedModelInfoFromRow(row))
	}
	return out, nil
}

// installedModelInfoFromRow is store.InstalledModelRow flattened into the
// API shape — factored out of handleModelsInstalled (backend.go) so this
// file builds the same response after a removal instead of duplicating it.
func installedModelInfoFromRow(row store.InstalledModelRow) InstalledModelInfo {
	return InstalledModelInfo{
		BackendName: row.BackendName, Name: row.Name, Digest: row.Digest, SizeBytes: row.SizeBytes,
		Quantization: row.Quantization, Family: row.Family, ParameterSize: row.ParameterSize,
		ModifiedAt: row.ModifiedAt, LastSeenAt: row.LastSeenAt,
		CatalogMatch: row.CatalogMatch, CatalogModelID: row.CatalogModelID,
		CatalogFileID: row.CatalogFileID, CatalogNote: row.CatalogNote,
	}
}

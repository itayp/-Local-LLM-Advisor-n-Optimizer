package server

import (
	"net/http"

	"advisor/internal/catalog"
	"advisor/internal/recommend"
)

// ModelDetailResponse is GET /api/models/{id}/detail: one catalogue size, with
// what others have published about the model and what this machine would
// do with it, as two sibling objects that never share a struct, a column or
// a sentence (research/EXTERNAL_SOURCES.md, the display rule P-1 to P-8):
//
//   - public: other people's results about the model — a figure.Public each,
//     with who published it, what it tested and the source's own date;
//   - local: estimated or measured here — exactly GET /api/models/{id}/fit.
//
// {id} is a size's id in GET /api/catalog. The query (ctx, kv) is /fit's.
type ModelDetailResponse struct {
	ID         int64             `json:"id" source:"n/a"` // the catalogue size's id
	FamilyID   string            `json:"family_id"`
	Name       string            `json:"name"` // "Qwen3.5 9B"
	Maintainer string            `json:"maintainer"`
	Purposes   []catalog.Purpose `json:"purposes"`              // most credible first
	ReleasedAt string            `json:"released_at,omitempty"` // the original repo's creation date; display only
	PullName   string            `json:"pull_name"`
	Public     PublicBlock       `json:"public"`
	Local      ModelFitResponse  `json:"local"`
}

// PublicBlock is a size's "Public data" block.
type PublicBlock struct {
	// Entries are the size's public values, each with its position in
	// words; empty when no approved source has scored this size (P-7: the
	// UI says so; it never borrows a sibling size's score).
	Entries []catalog.PublicEntry `json:"entries"`
	// Updated is when public scores were last fetched, and which source's
	// latest check failed, in words.
	Updated string `json:"updated"`
}

// handleModelDetail is GET /api/models/{id}/detail.
func (s *Server) handleModelDetail(w http.ResponseWriter, r *http.Request) {
	local, rows, ferr := s.modelFit(w, r)
	if ferr != nil {
		if ferr.status != 0 {
			writeError(w, ferr.status, ferr.code, ferr.words)
		}
		return
	}
	resp := ModelDetailResponse{Local: local, Public: PublicBlock{Entries: []catalog.PublicEntry{}}}
	for _, row := range rows {
		if row.Model.ID != local.Model.ID {
			continue
		}
		resp.ID, resp.FamilyID, resp.Maintainer, resp.Purposes = row.Model.ID, row.Model.FamilyID, row.Maintainer, row.Purposes
		resp.Name = recommend.DisplayName(recommend.Entry{FamilyID: row.Model.FamilyID, DisplayName: row.DisplayName, Model: row.Model})
		resp.ReleasedAt, resp.PullName = row.Model.ReleasedAt, row.Model.Size.OllamaTag
	}
	view, err := s.publicView(r.Context(), rows)
	if err != nil {
		// The machine's half stands on its own; the public half says why it is empty.
		s.log.Warn("reading public data", "err", err)
		resp.Public.Updated = "Public scores could not be read: " + err.Error()
	} else {
		if e := view.Entries(local.Model.ID); e != nil {
			resp.Public.Entries = e
		}
		resp.Public.Updated = view.Updated()
	}
	writeJSON(w, http.StatusOK, resp)
}

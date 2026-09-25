package server

import (
	"net/http"

	"advisor/internal/catalog"
	"advisor/internal/recommend"
)

// SpeedNeedsResponse is GET /api/speed-needs: the curated thresholds a
// purpose's speed is graded against (ARCHITECTURE.md D-58,
// data/recommend/speed-needs.yaml), for the tokens_per_sec glossary
// explainer (backlog item (b)) — what a given speed is good *for*, not just
// what tok/s *is*. This is never how a card explains itself: a
// recommendation's own reason is already a templated sentence
// (recommend.Reason, internal/recommend/reasons.go); this endpoint is the
// general table behind the glossary, the same for every machine and every
// model.
//
// Every number here is a curated configuration value — not a measurement of
// this machine (figure.Rate) and not a publisher's figure about a model
// (figure.Public) — so each is tagged source:"n/a" rather than wrapped in a
// figure type.
type SpeedNeedsResponse struct {
	// WordsPerToken turns a tokens-a-second number into the words a second
	// the rest of the UI shows a person (CLAUDE.md: tok/s never appears in
	// plain copy).
	WordsPerToken float64            `json:"words_per_token" source:"n/a"` // curated threshold from speed-needs.yaml
	Purposes      []SpeedNeedPurpose `json:"purposes"`
}

// SpeedNeedPurpose is one row of the table: how fast an answer needs to
// stream to feel good while someone reads along, and how long a wait still
// feels good before the first word.
type SpeedNeedPurpose struct {
	Purpose catalog.Purpose `json:"purpose"`
	// Mode is "read_along" (most purposes: a person reads the answer as it
	// streams) or "per_step" (agentic: nobody reads along between steps, so
	// there is no stream bar — only Wait, for one whole step).
	Mode recommend.Mode `json:"mode"`
	// Stream is the answer speed, in tokens a second, that earns each grade.
	// The three numbers are the same for every read_along purpose (they come
	// from how fast a person takes in words, not from the purpose itself);
	// nil for a per_step purpose, which has no stream bar.
	Stream *SpeedLevels `json:"stream,omitempty"`
	// Wait is the seconds before the first visible word — for a typical
	// prompt of this purpose, or for a per_step purpose's one step — that
	// still earns each grade.
	Wait SpeedLevels `json:"wait_s"`
}

// SpeedLevels is the same three grades speed-needs.yaml always states:
// excellent, good and usable. Below usable is too_slow, which has no
// number of its own — it is simply slower than usable's.
type SpeedLevels struct {
	Excellent float64 `json:"excellent" source:"n/a"` // curated threshold from speed-needs.yaml
	Good      float64 `json:"good" source:"n/a"`      // curated threshold from speed-needs.yaml
	Usable    float64 `json:"usable" source:"n/a"`    // curated threshold from speed-needs.yaml
}

// handleSpeedNeeds is GET /api/speed-needs: the whole curated table, read
// fresh from the embedded, validated data every time (cheap — it is parsed
// once and cached by recommend.DefaultSpeedNeeds, the same pattern
// GET /api/chatapps uses for its own cheap, storeless data).
func (s *Server) handleSpeedNeeds(w http.ResponseWriter, r *http.Request) {
	sn, err := recommend.DefaultSpeedNeeds()
	if err != nil {
		s.log.Error("reading speed-needs.yaml", "err", err)
		writeError(w, http.StatusInternalServerError, "speed_needs", "the advisor's speed table could not be read: "+err.Error())
		return
	}
	resp := SpeedNeedsResponse{WordsPerToken: sn.WordsPerToken()}
	for _, p := range catalog.Purposes {
		need, ok := sn.Purpose(p)
		if !ok {
			continue // validated at load time to have every purpose; defensive only
		}
		row := SpeedNeedPurpose{
			Purpose: p,
			Mode:    need.Mode,
			Wait: SpeedLevels{
				Excellent: need.WaitS.Excellent.Value,
				Good:      need.WaitS.Good.Value,
				Usable:    need.WaitS.Usable.Value,
			},
		}
		if need.Mode == recommend.ModeReadAlong {
			exc, _ := sn.StreamRate(recommend.GradeExcellent)
			good, _ := sn.StreamRate(recommend.GradeGood)
			usable, _ := sn.StreamRate(recommend.GradeUsable)
			row.Stream = &SpeedLevels{Excellent: exc, Good: good, Usable: usable}
		}
		resp.Purposes = append(resp.Purposes, row)
	}
	writeJSON(w, http.StatusOK, resp)
}

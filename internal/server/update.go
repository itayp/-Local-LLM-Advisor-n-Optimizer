package server

import (
	"context"
	"net/http"
	"time"

	"advisor/internal/update"
	"advisor/internal/version"
)

// Update (build-plan step 11): the Settings screen's "Check for updates"
// button — GET /api/update/check. Manual only, on purpose: this is the one
// endpoint in the whole daemon that leaves the machine to ask a question
// ("what's the newest release?"), and product rule 7's spirit ("no usage
// data leaves the machine") is kept by never running it on a timer and by
// the request itself carrying nothing about the user or this computer —
// see internal/update's own doc comment and ARCHITECTURE.md D-62.

// updateHTTPTimeout bounds one check; a person is watching a button, not
// waiting on a background job.
const updateHTTPTimeout = 8 * time.Second

// handleUpdateCheck answers with update.Info as-is: no numeric fields, so
// nothing here needs a figure.* wrapper (product rule 4 only concerns
// numbers with provenance).
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	check := s.checkUpdate
	if check == nil {
		check = defaultCheckUpdate
	}
	writeJSON(w, http.StatusOK, check(r.Context(), version.Version))
}

// defaultCheckUpdate is the real network call; tests replace s.checkUpdate
// with a fake so they never touch the network (the same seam style as
// s.open and s.backendList).
func defaultCheckUpdate(ctx context.Context, current string) update.Info {
	client := &http.Client{Timeout: updateHTTPTimeout}
	return update.Check(ctx, client, current)
}

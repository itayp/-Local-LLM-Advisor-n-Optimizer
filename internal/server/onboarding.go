package server

import (
	"net/http"

	"advisor/internal/store"
)

// The onboarding API (build-plan step 7):
//
//	GET  /api/onboarding           whether the first-run flow has been completed
//	POST /api/onboarding/complete  mark it completed
//
// This is what "make it what the browser opens to until it has been
// completed once" is built on: main.go always opens the browser at "/";
// the UI itself decides, from this endpoint, whether to show the
// first-run flow or the working screens (product rule 5 is the user
// clicking "Use it" at the end of the flow, not a guess).

// onboardingCompleteKey is the settings table row this lives in — one
// key, no history (a setting, not evidence the app is built on; D-13).
const onboardingCompleteKey = "onboarding.completed"

// OnboardingStatus is GET /api/onboarding and POST /api/onboarding/complete.
type OnboardingStatus struct {
	Completed bool `json:"completed"`
	// CompletedAt is when POST /api/onboarding/complete was last called;
	// "" until then. A fact read back from storage, not an estimate or a
	// measurement — no figure.
	CompletedAt string `json:"completed_at,omitempty"`
}

func (s *Server) onboardingStatus(r *http.Request) OnboardingStatus {
	if s.store == nil {
		// Nothing durable to ask: without a store the daemon cannot
		// remember completion across a restart, so it never claims to —
		// the UI shows the flow every time rather than guessing "done".
		return OnboardingStatus{}
	}
	at, ok, err := s.store.Setting(r.Context(), onboardingCompleteKey)
	if err != nil {
		s.log.Warn("reading onboarding status", "err", err)
		return OnboardingStatus{}
	}
	return OnboardingStatus{Completed: ok, CompletedAt: at}
}

func (s *Server) handleOnboardingStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.onboardingStatus(r))
}

// handleOnboardingComplete is POST /api/onboarding/complete: there is
// nothing to configure, so the body (if any) is ignored.
func (s *Server) handleOnboardingComplete(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "no_store",
			"the advisor has no database to remember this in, so it cannot mark setup as finished")
		return
	}
	if err := s.store.SetSetting(r.Context(), onboardingCompleteKey, store.Now()); err != nil {
		s.log.Error("recording onboarding completion", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "setup could not be marked as finished")
		return
	}
	writeJSON(w, http.StatusOK, s.onboardingStatus(r))
}

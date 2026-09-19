package server

import (
	"net/http"

	"advisor/internal/chatapps"
)

// ChatAppsResponse is GET /api/chatapps: the chat apps build-plan step 7's
// "Use it" screen can point a person at (ARCHITECTURE.md D-4 — the advisor
// detects and hands off, never installs or drives one). Cheap enough (a
// handful of stat calls) to run fresh on every request, the same call
// GET /api/backends makes about re-detecting every time.
type ChatAppsResponse struct {
	Apps []chatapps.App `json:"apps"`
}

func (s *Server) handleChatApps(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ChatAppsResponse{Apps: chatapps.Detect()})
}

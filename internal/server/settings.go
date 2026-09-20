package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"advisor/internal/hardware"
)

// Settings (build-plan step 8):
//
//	GET  /api/settings                   the durable, machine-wide settings
//	PUT  /api/settings                   change them
//	POST /api/settings/open-data-dir     open the daemon's data folder in the OS file manager
//	POST /api/settings/open-models-dir   open the folder the runtime keeps its models in
//
// Advanced (product rule 2's toggle) is the only setting the MVP has. The
// UI keeps a localStorage copy for a snappy first paint
// (ui/src/state/settings.tsx) and reconciles it with this endpoint on
// load; this table is what survives a cleared browser profile or a second
// window. Notifications (step 10) and anything else land here the same
// way later: one more row read into the same response.
//
// D-16: the daemon writes to one folder (store.DefaultDataDir) and reads
// models from wherever the runtime keeps them; the Settings screen shows
// both paths and a button that opens each. Opening a folder is something
// only the daemon can do (a page in the browser cannot reach the OS's file
// manager), which is why these are POSTs, not plain links.

// settingsAdvancedKey is the settings table row Advanced lives in.
const settingsAdvancedKey = "settings.advanced"

// SettingsResponse is GET and PUT /api/settings.
type SettingsResponse struct {
	Advanced bool `json:"advanced"`
	// DataDir is read-only here: the daemon decides it at startup
	// (store.DefaultDataDir, ADVISOR_DATA_DIR in development); a fact, not
	// a figure.
	DataDir string `json:"data_dir"`
}

// SettingsUpdate is the body of PUT /api/settings.
type SettingsUpdate struct {
	Advanced bool `json:"advanced"`
}

func (s *Server) settingsResponse(r *http.Request) SettingsResponse {
	resp := SettingsResponse{}
	if s.store == nil {
		return resp
	}
	resp.DataDir = filepath.Dir(s.store.Path())
	if v, ok, err := s.store.Setting(r.Context(), settingsAdvancedKey); err != nil {
		s.log.Warn("reading settings", "err", err)
	} else if ok {
		resp.Advanced = v == "1"
	}
	return resp
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsResponse(r))
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var req SettingsUpdate
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", `the request must be JSON: {"advanced": true}`)
		return
	}
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "no_store", "the advisor has no database to remember this in")
		return
	}
	value := "0"
	if req.Advanced {
		value = "1"
	}
	if err := s.store.SetSetting(r.Context(), settingsAdvancedKey, value); err != nil {
		s.log.Error("writing settings", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "the setting could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, s.settingsResponse(r))
}

// handleOpenDataDir opens the daemon's own data folder — the database now;
// logs and a user-space Ollama install (step 3) live beside it.
func (s *Server) handleOpenDataDir(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "no_store", "there is no data folder yet")
		return
	}
	s.openFolderOrError(w, filepath.Dir(s.store.Path()))
}

// handleOpenModelsDir opens the folder this start's hardware detection
// found the runtime keeping its models in (hardware.Storage.ModelsDir) —
// it waits for detection exactly as GET /api/hardware does.
func (s *Server) handleOpenModelsDir(w http.ResponseWriter, r *http.Request) {
	select {
	case <-s.hw.ready:
	case <-r.Context().Done():
		return
	case <-time.After(hardwareWait):
		writeError(w, http.StatusServiceUnavailable, "detecting", "still reading this computer; try again in a moment")
		return
	}
	if s.hw.err != nil {
		writeError(w, http.StatusServiceUnavailable, "detection_failed", "the models folder is not known yet")
		return
	}
	dir := s.hw.resp.Profile.Storage.ModelsDir
	if dir == "" || dir == hardware.Unknown {
		// D-21: unknown is unknown, never a guess at where to open.
		writeError(w, http.StatusServiceUnavailable, "unknown", "the models folder could not be found on this computer")
		return
	}
	s.openFolderOrError(w, dir)
}

func (s *Server) openFolderOrError(w http.ResponseWriter, dir string) {
	open := s.open
	if open == nil {
		open = openInFileManager
	}
	if err := open(dir); err != nil {
		s.log.Warn("opening a folder", "dir", dir, "err", err)
		writeError(w, http.StatusInternalServerError, "open_failed", "that folder could not be opened")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// openInFileManager asks the OS to open dir in its file manager — the same
// per-OS shape as cmd/advisor's openBrowser, one step removed: the daemon
// is what runs it, because a page in the browser cannot open a folder on
// its own.
func openInFileManager(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		cmd = exec.Command("explorer", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	return cmd.Start()
}

package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/hardware"
	"advisor/internal/watch"
)

// Settings (build-plan step 8):
//
//	GET  /api/settings                   the durable, machine-wide settings
//	PUT  /api/settings                   change them
//	POST /api/settings/open-data-dir     open the daemon's data folder in the OS file manager
//	POST /api/settings/open-models-dir   open the folder the runtime keeps its models in
//
// Advanced (product rule 2's toggle) was the MVP's only setting; the
// purposes picked on Recommend and the new-model watch's own settings
// (step 10) landed here the same way this comment always said the next
// one would: one more row read into the same response. The UI keeps a
// localStorage copy of all of it for a snappy first paint
// (ui/src/state/settings.tsx) and reconciles it with this endpoint on
// load; this table is what survives a cleared browser profile or a second
// window.
//
// D-16: the daemon writes to one folder (store.DefaultDataDir) and reads
// models from wherever the runtime keeps them; the Settings screen shows
// both paths and a button that opens each. Opening a folder is something
// only the daemon can do (a page in the browser cannot reach the OS's file
// manager), which is why these are POSTs, not plain links.

// settingsAdvancedKey is the settings table row Advanced lives in.
const settingsAdvancedKey = "settings.advanced"

// settingsPurposesKey is "the purposes you picked" (PRD §12's notification
// rule, build-plan step 10): the Recommend screen's own selection, made
// durable so the watch scheduler (watch.go) has something to check new
// models against even when nobody has the UI open — comma-separated
// catalog.Purpose values; "" (the unset default) means none chosen yet.
const settingsPurposesKey = "settings.purposes"

// settingsWatchEnabledKey and settingsWatchModeKey are the new-model
// watch's own two settings (build-plan step 10, item 3): the master switch
// and the desktop-notification mode. Interval (watch.Settings) is not
// stored here — nothing in the UI sets it, and 0 (unset) already means
// "use watch.Config.DefaultInterval".
const (
	settingsWatchEnabledKey = "watch.enabled"
	settingsWatchModeKey    = "watch.mode"
)

// SettingsResponse is GET and PUT /api/settings.
type SettingsResponse struct {
	Advanced bool `json:"advanced"`
	// DataDir is read-only here: the daemon decides it at startup
	// (store.DefaultDataDir, ADVISOR_DATA_DIR in development); a fact, not
	// a figure.
	DataDir string `json:"data_dir"`
	// Purposes is the durable copy of the Recommend screen's own
	// selection (settingsPurposesKey); empty until the user has picked at
	// least once.
	Purposes []catalog.Purpose `json:"purposes"`
	// Watch is the new-model watch's settings (build-plan step 10, item 3).
	Watch watch.Settings `json:"watch"`
}

// SettingsUpdate is the body of PUT /api/settings. Purposes and Watch are
// optional — nil (the field left out of the request body) leaves the
// stored value as it is, so a PUT that only flips Advanced (every caller
// already sends that one in full) does not also reset the other, and vice
// versa.
type SettingsUpdate struct {
	Advanced bool               `json:"advanced"`
	Purposes *[]catalog.Purpose `json:"purposes,omitempty"`
	Watch    *watch.Settings    `json:"watch,omitempty"`
}

func (s *Server) settingsResponse(r *http.Request) SettingsResponse {
	resp := SettingsResponse{Watch: watch.DefaultSettings()}
	if s.store == nil {
		return resp
	}
	resp.DataDir = filepath.Dir(s.store.Path())
	if v, ok, err := s.store.Setting(r.Context(), settingsAdvancedKey); err != nil {
		s.log.Warn("reading settings", "err", err)
	} else if ok {
		resp.Advanced = v == "1"
	}
	resp.Purposes = s.purposesSetting(r.Context())
	resp.Watch = s.watchSettings(r.Context())
	return resp
}

// purposesSetting is the durable "purposes you picked"
// (settingsPurposesKey): comma-separated catalog.Purpose values, an unknown
// or no-longer-valid one dropped rather than failing the whole read (a
// purpose retired from a later catalogue update must not break a settings
// row written before it was).
func (s *Server) purposesSetting(ctx context.Context) []catalog.Purpose {
	if s.store == nil {
		return nil
	}
	v, ok, err := s.store.Setting(ctx, settingsPurposesKey)
	if err != nil {
		s.log.Warn("reading the saved purposes", "err", err)
		return nil
	}
	if !ok || strings.TrimSpace(v) == "" {
		return nil
	}
	var out []catalog.Purpose
	for _, part := range strings.Split(v, ",") {
		if p := catalog.Purpose(strings.TrimSpace(part)); p.Valid() {
			out = append(out, p)
		}
	}
	return out
}

// watchSettings is the new-model watch's durable settings, defaulting a
// fresh install (nothing in the settings table yet) to
// watch.DefaultSettings() — on, and notifying — rather than the zero value
// (off, no mode) an unset key would otherwise read back as.
func (s *Server) watchSettings(ctx context.Context) watch.Settings {
	out := watch.DefaultSettings()
	if s.store == nil {
		return out
	}
	if v, ok, err := s.store.Setting(ctx, settingsWatchEnabledKey); err != nil {
		s.log.Warn("reading watch settings", "err", err)
	} else if ok {
		out.Enabled = v == "1"
	}
	if v, ok, err := s.store.Setting(ctx, settingsWatchModeKey); err != nil {
		s.log.Warn("reading watch settings", "err", err)
	} else if ok && watch.NotifyMode(v).Valid() {
		out.Mode = watch.NotifyMode(v)
	}
	return out
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
	if req.Purposes != nil {
		for _, p := range *req.Purposes {
			if !p.Valid() {
				writeError(w, http.StatusBadRequest, "bad_purpose", "unknown purpose \""+string(p)+"\"; the purposes are "+joinPurposes())
				return
			}
		}
	}
	if req.Watch != nil && !req.Watch.Mode.Valid() {
		writeError(w, http.StatusBadRequest, "bad_mode", "watch.mode must be on, quiet or never")
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
	if req.Purposes != nil {
		parts := make([]string, 0, len(*req.Purposes))
		for _, p := range *req.Purposes {
			parts = append(parts, string(p))
		}
		if err := s.store.SetSetting(r.Context(), settingsPurposesKey, strings.Join(parts, ",")); err != nil {
			s.log.Error("writing settings", "err", err)
			writeError(w, http.StatusInternalServerError, "store", "the setting could not be saved")
			return
		}
	}
	if req.Watch != nil {
		enabled := "0"
		if req.Watch.Enabled {
			enabled = "1"
		}
		if err := s.store.SetSetting(r.Context(), settingsWatchEnabledKey, enabled); err != nil {
			s.log.Error("writing settings", "err", err)
			writeError(w, http.StatusInternalServerError, "store", "the setting could not be saved")
			return
		}
		if err := s.store.SetSetting(r.Context(), settingsWatchModeKey, string(req.Watch.Mode)); err != nil {
			s.log.Error("writing settings", "err", err)
			writeError(w, http.StatusInternalServerError, "store", "the setting could not be saved")
			return
		}
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

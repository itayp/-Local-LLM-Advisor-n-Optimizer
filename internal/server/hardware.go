package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"advisor/internal/hardware"
	"advisor/internal/store"
	"advisor/internal/version"
)

// HardwareResponse is GET /api/hardware: the machine as detected at this
// daemon start, and whether it differs from the previous start's.
type HardwareResponse struct {
	// ProfileID is the hardware_profiles row; benchmarks and estimates
	// reference it, which is what keeps them attributable to this hardware
	// after it changes. 0 when the profile could not be stored.
	ProfileID   int64  `json:"profile_id" source:"n/a"` // a row id
	DetectedAt  string `json:"detected_at"`             // RFC 3339 UTC
	Fingerprint string `json:"fingerprint"`
	// Changed is true when the hardware differs from the previous daemon
	// start (a GPU swapped, memory added). PreviousProfileID is that start's
	// row, so the UI can say what changed.
	Changed           bool             `json:"changed"`
	PreviousProfileID int64            `json:"previous_profile_id,omitempty" source:"n/a"` // a row id
	Profile           hardware.Profile `json:"profile"`
}

// HardwareHistory is GET /api/hardware/history: every distinct hardware
// configuration this machine has had, most recent first.
type HardwareHistory struct {
	Configurations []HardwareConfiguration `json:"configurations"`
}

// HardwareConfiguration is one fingerprint's worth of daemon starts.
type HardwareConfiguration struct {
	Fingerprint     string        `json:"fingerprint"`
	FirstSeen       string        `json:"first_seen"`
	LastSeen        string        `json:"last_seen"`
	Starts          int           `json:"starts" source:"n/a"`            // a count of daemon starts
	LatestProfileID int64         `json:"latest_profile_id" source:"n/a"` // a row id
	Current         bool          `json:"current"`                        // the configuration detected at this start
	Tier            hardware.Tier `json:"tier"`
	Summary         string        `json:"summary"`
	GPUs            []string      `json:"gpus"` // names, primary first
}

// hardwareState is this start's profile, published once detection is done.
type hardwareState struct {
	once  sync.Once
	ready chan struct{}
	resp  HardwareResponse
	err   error
}

// hardwareWait bounds how long GET /api/hardware waits for a detection
// that is still running (PowerShell's own timeout is 60 s). A variable so
// tests can shorten it.
var hardwareWait = 90 * time.Second

// RecordHardware detects the machine, stores the profile — a new row on
// every daemon start, never an update, so history is kept — and publishes
// it to GET /api/hardware. main runs it once, in the background, after the
// listener is up: the browser never waits for system_profiler or PowerShell
// to open the page, only the hardware request does.
func (s *Server) RecordHardware(ctx context.Context, detect func(context.Context) (hardware.Profile, error)) (HardwareResponse, error) {
	var resp HardwareResponse
	p, err := detect(ctx)
	if err != nil {
		s.publishHardware(resp, err)
		return resp, err
	}
	resp = HardwareResponse{
		DetectedAt:  store.Now(),
		Fingerprint: hardware.Fingerprint(p),
		Profile:     p,
	}
	if s.store != nil {
		row, err := s.store.AddHardwareProfile(ctx, p, version.Version)
		if err != nil {
			// The profile is still right; only its history entry is missing.
			s.log.Error("storing the hardware profile", "err", err)
		} else {
			resp.ProfileID, resp.DetectedAt = row.ID, row.CreatedAt
			if prev, err := s.store.HardwareProfileBefore(ctx, row.ID); err == nil {
				resp.PreviousProfileID = prev.ID
				resp.Changed = prev.Fingerprint != row.Fingerprint
			} else if !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("reading the previous hardware profile", "err", err)
			}
		}
	}
	s.publishHardware(resp, nil)
	return resp, nil
}

func (s *Server) publishHardware(resp HardwareResponse, err error) {
	s.hw.once.Do(func() {
		s.hw.resp, s.hw.err = resp, err
		close(s.hw.ready)
	})
}

func (s *Server) handleHardware(w http.ResponseWriter, r *http.Request) {
	select {
	case <-s.hw.ready:
	case <-r.Context().Done():
		return
	case <-time.After(hardwareWait):
		writeError(w, http.StatusServiceUnavailable, "detecting", "still reading this computer; try again in a moment")
		return
	}
	if s.hw.err != nil {
		writeError(w, http.StatusServiceUnavailable, "detection_failed", "this computer could not be read: "+s.hw.err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.hw.resp)
}

func (s *Server) handleHardwareHistory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "no_store", "hardware history is not available")
		return
	}
	confs, err := s.store.HardwareConfigurations(r.Context())
	if err != nil {
		s.log.Error("reading hardware history", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "hardware history could not be read")
		return
	}
	current := ""
	select {
	case <-s.hw.ready:
		current = s.hw.resp.Fingerprint
	default:
	}
	out := HardwareHistory{Configurations: []HardwareConfiguration{}}
	for _, c := range confs {
		gpus := make([]string, 0, len(c.Latest.GPUs))
		for _, g := range c.Latest.GPUs {
			gpus = append(gpus, g.Name)
		}
		out.Configurations = append(out.Configurations, HardwareConfiguration{
			Fingerprint:     c.Fingerprint,
			FirstSeen:       c.FirstSeen,
			LastSeen:        c.LastSeen,
			Starts:          c.Starts,
			LatestProfileID: c.LatestProfileID,
			Current:         c.Fingerprint == current,
			Tier:            c.Latest.Tier,
			Summary:         c.Latest.Summary,
			GPUs:            gpus,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleHardwareProfile returns one stored profile — the hardware an old
// benchmark ran on.
func (s *Server) handleHardwareProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_id", "the profile id must be a positive number")
		return
	}
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "no_store", "stored profiles are not available")
		return
	}
	row, err := s.store.HardwareProfile(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no hardware profile with that id")
		return
	}
	if err != nil {
		s.log.Error("reading a hardware profile", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "store", "the hardware profile could not be read")
		return
	}
	resp := HardwareResponse{ProfileID: row.ID, DetectedAt: row.CreatedAt, Fingerprint: row.Fingerprint, Profile: row.Profile}
	if prev, err := s.store.HardwareProfileBefore(r.Context(), row.ID); err == nil {
		resp.PreviousProfileID = prev.ID
		resp.Changed = prev.Fingerprint != row.Fingerprint
	}
	writeJSON(w, http.StatusOK, resp)
}

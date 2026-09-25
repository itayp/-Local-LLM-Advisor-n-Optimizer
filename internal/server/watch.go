package server

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/recommend"
	"advisor/internal/watch"
)

// The new-model watch (build-plan step 10):
//
//	GET  /api/watch/log   what the watch checked, when, what it found, and
//	                      what it suppressed and why (item 4) — the most
//	                      recent runs, newest first
//	POST /api/watch/run   run a check now, detached from the request (the
//	                      same shape POST /api/catalog/refresh already
//	                      uses), for a "check now" button and the CLI
//
// The scheduler itself is WatchScheduler, started once from cmd/advisor's
// main after the listener is up — the same shape RecordHardware and
// RecordBackends already run in. RunWatch is what both the scheduler and
// the manual endpoint call; it is also all `advisor watch` (if a later step
// adds one) would need.
//
// The check itself is internal/watch's job (Run): this file only assembles
// what Run needs from a live daemon — the refreshed catalogue, an engine
// built the same way GET /api/recommend builds one, this start's hardware,
// the purposes and watch settings saved in the settings table — and turns
// the result into API responses.

// watchState is the watch's own runtime state: the notifier this start
// uses (Default() in production; tests replace it), this start's own base
// URL for a notification's deep link (SetWatchBaseURL, called once main.go
// knows the address the daemon is listening on — "" until then, so a run
// before it is set simply sends no URL), and the one-run-at-a-time lock a
// scheduled and a manual run share.
type watchState struct {
	notifier watch.Notifier
	baseURL  string
	running  sync.Mutex
}

func (w *watchState) init() {
	w.notifier = watch.Default()
}

// SetWatchBaseURL records this start's own address for a notification's
// deep link — "that model's card, with 'Run benchmark' the next thing to
// click" (product rule 5: it never pulls or switches anything itself).
// main.go calls this once, after server.Listen has an address.
func (s *Server) SetWatchBaseURL(url string) {
	s.wch.baseURL = strings.TrimRight(url, "/")
}

// watchURL is the deep link for one catalogue model's card, or "" before
// SetWatchBaseURL has run.
func (s *Server) watchURL(catalogModelID int64) string {
	if s.wch.baseURL == "" {
		return ""
	}
	return s.wch.baseURL + "/models/" + strconv.FormatInt(catalogModelID, 10)
}

// RunWatch is one watch run, once (BUILD_PLAN.md step 10's whole "done
// when"): refresh the catalogue from the approved sources (item 1's first
// half, RefreshCatalog — the same one POST /api/catalog/refresh runs), then
// every curated size not yet notified about through Fit and Recommend
// against this machine and the purposes saved in settings (item 2), a
// desktop notification for one that qualifies and Settings.Mode is on
// (item 3), and a line in the watch log either way (item 4). trigger is
// "scheduler", "api" or "cli" — the same three RefreshCatalog already uses.
//
// One run at a time: a manual trigger while the scheduler is mid-run (or
// vice versa) gets ErrRefreshRunning, the same shape a concurrent catalogue
// refresh already answers with.
func (s *Server) RunWatch(ctx context.Context, trigger string) (watch.Report, error) {
	if s.store == nil {
		return watch.Report{}, errors.New("server: no database, nothing to watch with")
	}
	if !s.wch.running.TryLock() {
		return watch.Report{}, ErrRefreshRunning
	}
	defer s.wch.running.Unlock()

	settings := s.watchSettings(ctx)
	if !settings.Enabled {
		// The master switch: no refresh, no check, no log line at all
		// (watch.go's own doc comment on Settings.Enabled) — Run returns
		// immediately once it sees this.
		return watch.Run(ctx, watch.Options{Store: s.store, Settings: settings, Trigger: trigger, Log: s.log})
	}

	// Item 1's first half. A refresh that fails (offline, Hugging Face
	// unreachable) does not stop the run: whatever the catalogue already
	// holds is still checked, and the report says the refresh itself did
	// not finish (run.go reads RefreshError into the log the UI shows).
	refreshErr := ""
	if _, err := s.RefreshCatalog(ctx, trigger); err != nil {
		refreshErr = err.Error()
		s.log.Warn("watch: refreshing the catalogue before a check", "err", err)
	}

	cat, err := s.cat.load()
	if err != nil {
		return watch.Report{}, err
	}
	entries, rows, err := s.catalogue(ctx)
	if err != nil {
		return watch.Report{}, err
	}
	engine, err := recommend.New(entries)
	if err != nil {
		return watch.Report{}, err
	}
	m, mErr := s.machineFor(ctx)
	if mErr != nil {
		// Hardware detection has not finished, or failed. The maintainer
		// scan and the once-ever bookkeeping still run; checkOne's own
		// "nothing to compare against yet" (haveResult false) is what a
		// zero-value Machine below safely produces, since Engine.Recommend
		// is still called with it and simply cannot fit anything on an
		// unknown machine — every size logs as suppressed, not scored.
		s.log.Warn("watch: this computer is not known yet; sizes will be logged as suppressed", "err", mErr)
	}
	engine.Measurements = s.evidence(ctx, m, engine.Estimator)
	var publicLine func(int64, []catalog.Purpose) *catalog.PublicEntry
	if view, verr := s.publicView(ctx, rows); verr != nil {
		s.log.Warn("watch: reading public data; checking without it", "err", verr)
	} else if view != nil {
		engine.Public = view.Scoring()
		// The same View.Line GET /api/recommend calls after Recommend
		// returns (handleRecommend) — a qualifying candidate's notification
		// gets the same "Public data" line its card would (watch/reasons.go's
		// publicBullet), because Run calls Engine.Recommend itself and has
		// no result to post-process the way the HTTP handler does.
		publicLine = view.Line
	}

	installedRows, err := s.store.AllInstalledModels(ctx)
	if err != nil {
		return watch.Report{}, err
	}
	installed := make([]recommend.InstalledModel, 0, len(installedRows))
	for _, row := range installedRows {
		installed = append(installed, recommend.InstalledModel{
			Name: row.Name, SizeBytes: row.SizeBytes, ParameterSize: row.ParameterSize,
			CatalogModelID: row.CatalogModelID, CatalogFileID: row.CatalogFileID,
		})
	}

	client := s.cat.newClient()
	client.Log = s.log

	return watch.Run(ctx, watch.Options{
		Store: s.store, Catalogue: cat, HF: client,
		Engine: engine, Machine: m, Purposes: s.purposesSetting(ctx), Installed: installed,
		PublicLine: publicLine,
		Notifier:   s.wch.notifier, Settings: settings, Config: watch.DefaultConfig(),
		URL: s.watchURL, Trigger: trigger, RefreshError: refreshErr, Log: s.log,
	})
}

// WatchScheduler runs the new-model watch on its own schedule until ctx is
// done: a short, jittered pause after the daemon starts, then a check, then
// watch.Config.DefaultInterval between checks (also jittered, so a fleet of
// installs does not all read Hugging Face at the same minute —
// BUILD_PLAN.md step 10, item 1's "default daily, jittered"). main.go
// starts this once, in the background, after the listener is up — the same
// shape RecordHardware and RecordBackends already run in.
func (s *Server) WatchScheduler(ctx context.Context) {
	cfg := watch.DefaultConfig()
	if !sleepOrDone(ctx, jitteredDelay(watchInitialDelay, cfg.Jitter)) {
		return
	}
	for {
		if _, err := s.RunWatch(ctx, "scheduler"); err != nil && !errors.Is(err, ErrRefreshRunning) {
			s.log.Warn("watch: a scheduled check did not finish", "err", err)
		}
		interval := cfg.DefaultInterval
		if custom := s.watchSettings(ctx).Interval; custom > 0 {
			interval = custom
		}
		if !sleepOrDone(ctx, jitteredDelay(interval, cfg.Jitter)) {
			return
		}
	}
}

// watchInitialDelay is how long WatchScheduler waits after the daemon
// starts before its first check — long enough that it is not competing
// with hardware and backend detection for the first seconds of a cold
// start, short enough that a fresh install's watch log is not a whole day
// away. CHOSEN.
const watchInitialDelay = 30 * time.Second

// jitteredDelay is base, moved randomly earlier or later by up to
// fraction*base (Config.Jitter's own doc comment: 0..1). base <= 0 waits
// not at all.
func jitteredDelay(base time.Duration, fraction float64) time.Duration {
	if base <= 0 {
		return 0
	}
	spread := time.Duration(float64(base) * fraction)
	if spread <= 0 {
		return base
	}
	d := base + time.Duration(rand.Int63n(int64(2*spread))) - spread
	if d < 0 {
		return 0
	}
	return d
}

// sleepOrDone waits for d, or returns false early if ctx ends first.
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// WatchLogResponse is GET /api/watch/log.
type WatchLogResponse struct {
	Runs []WatchRunSummary `json:"runs"`
}

// WatchRunSummary is one watch_runs row, newest first — the plain columns
// for a list screen, plus the full report (run.go's Report, JSON-decoded
// back) for "what was checked, when, what was found, what was suppressed
// and why" (item 4).
type WatchRunSummary struct {
	ID         int64        `json:"id" source:"n/a"`
	CreatedAt  string       `json:"created_at"`
	FinishedAt string       `json:"finished_at"`
	Trigger    string       `json:"trigger"`
	Checked    int          `json:"checked" source:"n/a"`
	Notified   int          `json:"notified" source:"n/a"`
	Suppressed int          `json:"suppressed" source:"n/a"`
	Flagged    int          `json:"flagged" source:"n/a"`
	Report     watch.Report `json:"report"`
}

// handleWatchLog is GET /api/watch/log: the most recent watch runs, newest
// first.
func (s *Server) handleWatchLog(w http.ResponseWriter, r *http.Request) {
	out := WatchLogResponse{Runs: []WatchRunSummary{}}
	if s.store == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	rows, err := s.store.RecentWatchRuns(r.Context(), 20)
	if err != nil {
		s.log.Error("reading the watch log", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "the watch log could not be read")
		return
	}
	for _, row := range rows {
		sum := WatchRunSummary{
			ID: row.ID, CreatedAt: row.CreatedAt, FinishedAt: row.FinishedAt, Trigger: row.Trigger,
			Checked: row.Checked, Notified: row.Notified, Suppressed: row.Suppressed, Flagged: row.Flagged,
		}
		if err := decodeReport(row.ReportJSON, &sum.Report); err != nil {
			s.log.Warn("decoding a stored watch report", "run_id", row.ID, "err", err)
		}
		out.Runs = append(out.Runs, sum)
	}
	writeJSON(w, http.StatusOK, out)
}

// decodeReport unmarshals a watch_runs row's stored report_json into rep —
// "{}" (mustJSON's own fallback in internal/watch, and a fresh install's
// empty column) decodes to the zero Report, no error.
func decodeReport(reportJSON string, rep *watch.Report) error {
	if reportJSON == "" {
		return nil
	}
	return json.Unmarshal([]byte(reportJSON), rep)
}

// handleWatchRun is POST /api/watch/run: run a check now, detached from the
// request exactly like POST /api/catalog/refresh — closing the tab does
// not abandon it half-way.
func (s *Server) handleWatchRun(w http.ResponseWriter, r *http.Request) {
	type result struct {
		rep watch.Report
		err error
	}
	done := make(chan result, 1)
	go func() {
		rep, err := s.RunWatch(context.WithoutCancel(r.Context()), "api")
		done <- result{rep, err}
	}()
	select {
	case <-r.Context().Done():
		return
	case res := <-done:
		switch {
		case errors.Is(res.err, ErrRefreshRunning):
			writeError(w, http.StatusConflict, "watch_running", "a watch check is already running; its result will be in GET /api/watch/log")
		case res.err != nil:
			s.log.Error("watch check", "err", res.err)
			writeError(w, http.StatusInternalServerError, "watch_failed", "the watch check stopped: "+res.err.Error())
		default:
			writeJSON(w, http.StatusOK, res.rep)
		}
	}
}

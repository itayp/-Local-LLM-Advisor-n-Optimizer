package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"advisor/internal/backend"
	"advisor/internal/bench"
	"advisor/internal/estimate"
	"advisor/internal/recommend"
)

// The benchmark API (build-plan step 6, ARCHITECTURE.md D-49):
//
//	GET  /api/bench/plan?model=&num_ctx=&prompts=   what a run would do, and whether it is refused
//	POST /api/bench                                 start a run: {model, num_ctx, prompts, measure_anyway} → 202 + the run
//	GET  /api/bench/{id}                            the run; with Accept: text/event-stream, its progress as a stream
//	POST /api/bench/{id}/cancel                     stop it, unload the model, answer with the run as it ended
//	GET  /api/bench/history?model=&limit=           runs, newest first
//
// A run is one at a time and outlives the request that started it:
// closing the page does not stop it, cancel does.

// benchStreamKeepAlive is how often an idle progress stream sends a comment
// line, so nothing between the browser and the daemon closes it.
var benchStreamKeepAlive = 15 * time.Second

// cancelWait bounds how long POST /api/bench/{id}/cancel waits for the run
// to unload its model before answering with the run as it stands.
var cancelWait = 90 * time.Second

// RecoverBenchmarks closes runs a previous daemon left running (main calls
// it once at start).
func (s *Server) RecoverBenchmarks(ctx context.Context) {
	if s.bench == nil {
		return
	}
	if n, err := s.bench.Recover(ctx); err != nil {
		s.log.Warn("closing interrupted benchmark runs", "err", err)
	} else if n > 0 {
		s.log.Info("closed benchmark runs the previous start left running", "runs", n)
	}
}

// benchTarget is the machine and runtime a run is for: this start's
// hardware profile, the runtime path last seen on it, and an estimator
// calibrated by the benchmarks already stored for this hardware.
func (s *Server) benchTarget(w http.ResponseWriter, r *http.Request) (bench.Target, bool) {
	if s.bench == nil {
		writeError(w, http.StatusServiceUnavailable, "no_store", "benchmarks need the advisor's database, which is not available")
		return bench.Target{}, false
	}
	m, ok := s.machine(w, r)
	if !ok {
		return bench.Target{}, false
	}
	if s.hw.resp.ProfileID == 0 {
		writeError(w, http.StatusServiceUnavailable, "no_profile", "this computer's description could not be stored, so a test could not be attributed to it")
		return bench.Target{}, false
	}
	var b backend.Backend
	if all := s.backendList(); len(all) > 0 {
		b = all[0] // one runtime in the MVP
	}
	if b == nil {
		writeError(w, http.StatusServiceUnavailable, "backend_not_running", "no runtime is available to test with")
		return bench.Target{}, false
	}
	est, err := estimate.New()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "estimator", "the advisor's own data could not be read: "+err.Error())
		return bench.Target{}, false
	}
	ev, err := s.bench.Evidence(r.Context(), s.hw.resp.Fingerprint, b.Name())
	if err != nil {
		s.log.Warn("reading stored benchmarks", "err", err)
	}
	est.Calibration = est.Calibrate(ev.Observations)
	return bench.Target{Backend: b, Machine: m, Estimator: est, ProfileID: s.hw.resp.ProfileID, Fingerprint: s.hw.resp.Fingerprint}, true
}

// evidence is what the stored benchmarks of this hardware give the
// estimator and the engine: a calibration, and the configurations measured
// on the path the plan is for (with the default cache — the engine asks
// about no other), keyed the way the engine looks them up.
func (s *Server) evidence(ctx context.Context, m estimate.Machine, est *estimate.Estimator) map[recommend.MeasurementKey]estimate.Measurement {
	if s.bench == nil || s.hw.resp.Fingerprint == "" {
		return nil
	}
	var name string
	if all := s.backendList(); len(all) > 0 {
		name = all[0].Name()
	}
	ev, err := s.bench.Evidence(ctx, s.hw.resp.Fingerprint, name)
	if err != nil {
		s.log.Warn("reading stored benchmarks", "err", err)
		return nil
	}
	est.Calibration = est.Calibrate(ev.Observations)
	path := est.Place(m).Path
	out := map[recommend.MeasurementKey]estimate.Measurement{}
	for _, mc := range ev.Measured {
		if mc.Path == path && mc.KVCacheType == estimate.KVF16 {
			out[recommend.MeasurementKey{CatalogFileID: mc.CatalogFileID, NumCtx: mc.NumCtx}] = mc.Measurement
		}
	}
	return out
}

func benchRequestFromQuery(r *http.Request) (bench.Request, error) {
	q := r.URL.Query()
	req := bench.Request{Model: strings.TrimSpace(q.Get("model")), MeasureAnyway: truthy(q.Get("measure_anyway"))}
	if v := q.Get("num_ctx"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return req, errors.New("num_ctx must be a number of tokens")
		}
		req.NumCtx = n
	}
	for _, raw := range q["prompts"] {
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				req.Prompts = append(req.Prompts, p)
			}
		}
	}
	return req, nil
}

// handleBenchPlan is GET /api/bench/plan: what a run would do, without
// doing it — the prompts that fit, the estimate, how long it takes, and the
// refusal when the configuration would spill. A refused plan is still a 200:
// the refusal is part of the answer.
func (s *Server) handleBenchPlan(w http.ResponseWriter, r *http.Request) {
	req, err := benchRequestFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	t, ok := s.benchTarget(w, r)
	if !ok {
		return
	}
	plan, err := s.bench.Plan(r.Context(), t, req)
	var be *bench.Error
	if errors.As(err, &be) && be.Plan != nil {
		writeJSON(w, http.StatusOK, *be.Plan)
		return
	}
	if err != nil {
		s.writeBenchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// handleBenchStart is POST /api/bench.
func (s *Server) handleBenchStart(w http.ResponseWriter, r *http.Request) {
	var req bench.Request
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the request must be JSON: {\"model\": \"…\", \"num_ctx\": 8192, \"prompts\": [\"500\"], \"measure_anyway\": false}")
		return
	}
	t, ok := s.benchTarget(w, r)
	if !ok {
		return
	}
	run, err := s.bench.Start(r.Context(), t, req)
	if err != nil {
		s.writeBenchError(w, err)
		return
	}
	withVerdicts(&run, s.verdictPurposes(r.Context()))
	w.Header().Set("Location", fmt.Sprintf("/api/bench/%d", run.ID))
	writeJSON(w, http.StatusAccepted, run)
}

// handleBenchRun is GET /api/bench/{id}: the run as JSON (with its samples),
// or — asked for with Accept: text/event-stream, as a browser's EventSource
// does — its progress as a stream of server-sent events until it ends.
func (s *Server) handleBenchRun(w http.ResponseWriter, r *http.Request) {
	id, ok := benchID(w, r)
	if !ok {
		return
	}
	if s.bench == nil {
		writeError(w, http.StatusNotFound, "not_found", "no test with that id")
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		s.streamBench(w, r, id)
		return
	}
	run, err := s.bench.Get(r.Context(), id, true)
	if err != nil {
		s.writeBenchError(w, err)
		return
	}
	withVerdicts(&run, s.verdictPurposes(r.Context()))
	writeJSON(w, http.StatusOK, run)
}

// streamBench sends the run's progress as server-sent events ("event:
// progress", the Progress as JSON) until the run ends; a run that has
// already ended is one event.
func (s *Server) streamBench(w http.ResponseWriter, r *http.Request, id int64) {
	current, ch, unsubscribe, live := s.bench.Subscribe(id)
	defer unsubscribe()
	if !live {
		run, err := s.bench.Get(r.Context(), id, false)
		if err != nil {
			s.writeBenchError(w, err)
			return
		}
		current = bench.Progress{RunID: run.ID, Status: run.Status, Phase: run.Phase, Message: finishedMessage(run), Run: run}
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	purposes := s.verdictPurposes(r.Context())
	send := func(p bench.Progress) bool {
		withVerdicts(&p.Run, purposes)
		b, err := json.Marshal(p)
		if err != nil {
			s.log.Error("encoding benchmark progress", "err", err)
			return false
		}
		if _, err := fmt.Fprintf(w, "event: progress\ndata: %s\n\n", b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send(current) || !live {
		return
	}
	keepAlive := time.NewTicker(benchStreamKeepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case p, open := <-ch:
			if !open || !send(p) {
				return
			}
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": still running\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func finishedMessage(run bench.Run) string {
	switch run.Status {
	case bench.StatusDone:
		return "Finished"
	case bench.StatusCancelled:
		return "Cancelled"
	case bench.StatusFailed:
		return "Stopped: " + run.Error
	}
	return string(run.Status)
}

// handleBenchCancel is POST /api/bench/{id}/cancel: stop the run, unload the
// model, and answer with the run as it ended (whether the model was
// confirmed unloaded is in it).
func (s *Server) handleBenchCancel(w http.ResponseWriter, r *http.Request) {
	id, ok := benchID(w, r)
	if !ok {
		return
	}
	if s.bench == nil {
		writeError(w, http.StatusNotFound, "not_found", "no test with that id is running")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), cancelWait)
	defer cancel()
	run, err := s.bench.Cancel(ctx, id)
	if err != nil {
		s.writeBenchError(w, err)
		return
	}
	withVerdicts(&run, s.verdictPurposes(r.Context()))
	writeJSON(w, http.StatusOK, run)
}

// handleBenchHistory is GET /api/bench/history[?model=][&limit=].
func (s *Server) handleBenchHistory(w http.ResponseWriter, r *http.Request) {
	if s.bench == nil {
		writeJSON(w, http.StatusOK, bench.History{Runs: []bench.Run{}})
		return
	}
	f := bench.HistoryFilter{Model: strings.TrimSpace(r.URL.Query().Get("model"))}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be a positive number")
			return
		}
		f.Limit = n
	}
	h, err := s.bench.History(r.Context(), f)
	if err != nil {
		s.log.Error("reading benchmark history", "err", err)
		writeError(w, http.StatusInternalServerError, "store", "the history of tests could not be read")
		return
	}
	purposes := s.verdictPurposes(r.Context())
	for i := range h.Runs {
		withVerdicts(&h.Runs[i], purposes)
	}
	writeJSON(w, http.StatusOK, h)
}

func benchID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_id", "the test id must be a positive number")
		return 0, false
	}
	return id, true
}

// writeBenchError maps the harness's refusals onto statuses and codes; the
// message is already in words.
func (s *Server) writeBenchError(w http.ResponseWriter, err error) {
	var be *bench.Error
	if !errors.As(err, &be) {
		s.log.Error("benchmark", "err", err)
		writeError(w, http.StatusInternalServerError, "bench", "the test could not be run: "+err.Error())
		return
	}
	status, code := http.StatusInternalServerError, "bench"
	switch {
	case errors.Is(err, bench.ErrBadRequest):
		status, code = http.StatusBadRequest, "bad_request"
	case errors.Is(err, bench.ErrBusy):
		status, code = http.StatusConflict, "bench_running"
	case errors.Is(err, bench.ErrBackendNotRunning):
		status, code = http.StatusConflict, "backend_not_running"
	case errors.Is(err, bench.ErrModelNotInstalled):
		status, code = http.StatusNotFound, "model_not_installed"
	case errors.Is(err, bench.ErrNotTextModel):
		status, code = http.StatusUnprocessableEntity, "not_a_text_model"
	case errors.Is(err, bench.ErrNothingFits):
		status, code = http.StatusUnprocessableEntity, "nothing_fits"
	case errors.Is(err, bench.ErrRefused):
		status, code = http.StatusConflict, "would_spill"
		if be.Plan != nil && be.Plan.RefusalCode != "" {
			code = be.Plan.RefusalCode
		}
	case errors.Is(err, bench.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	}
	writeError(w, status, code, be.Message)
}

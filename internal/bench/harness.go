package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"advisor/internal/backend"
	"advisor/internal/figure"
	"advisor/internal/hardware"
	"advisor/internal/store"
	"advisor/internal/version"
)

// Harness runs benchmarks, one at a time (ARCHITECTURE.md D-45).
type Harness struct {
	store *store.Store
	suite *Suite
	cfg   Config
	log   *slog.Logger

	// Seams for tests: the clock, waiting, and the machine the sampler reads.
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	env   sysEnv

	mu     sync.Mutex
	active *activeRun
}

// New builds a harness on the store with the embedded suite.
func New(st *store.Store, log *slog.Logger) (*Harness, error) {
	suite, err := DefaultSuite()
	if err != nil {
		return nil, err
	}
	return NewWith(st, log, suite, DefaultConfig()), nil
}

// NewWith is New with a suite and constants of the caller's choosing.
func NewWith(st *store.Store, log *slog.Logger, suite *Suite, cfg Config) *Harness {
	if log == nil {
		log = slog.Default()
	}
	return &Harness{store: st, suite: suite, cfg: cfg, log: log, now: time.Now, sleep: sleepCtx, env: realSysEnv{}}
}

// Suite is the suite the harness runs.
func (h *Harness) Suite() *Suite { return h.suite }

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Recover closes runs a previous daemon left running: it stopped while they
// ran, so they did not finish, and a row that says "running" forever would
// be a lie. The daemon calls it once at start.
func (h *Harness) Recover(ctx context.Context) (int64, error) {
	if h.store == nil {
		return 0, nil
	}
	return h.store.InterruptBenchRuns(ctx, "the advisor stopped while this test was running")
}

// activeRun is the run in progress.
type activeRun struct {
	id     int64
	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	run       Run
	progress  Progress
	subs      map[chan Progress]bool
	started   time.Time
	planned   *figure.Rate
	cancelled bool
}

// Start plans req on t and, unless the plan refuses it, starts the run in
// the background and returns it as it stands. It returns an *Error for a
// run it will not start: ErrBusy while another runs, ErrRefused (with the
// plan) for a configuration that would spill and was not asked for anyway.
func (h *Harness) Start(ctx context.Context, t Target, req Request) (Run, error) {
	h.mu.Lock()
	busy := h.active != nil
	h.mu.Unlock()
	if busy {
		return Run{}, fail(ErrBusy, "a test is already running; wait for it to finish or cancel it")
	}
	p, err := h.prepare(ctx, t, req)
	if err != nil {
		return Run{}, err
	}
	if p.plan.Refusal != "" && !req.MeasureAnyway {
		e := fail(ErrRefused, "%s", p.plan.Refusal)
		e.Plan = &p.plan
		return Run{}, e
	}

	h.mu.Lock()
	if h.active != nil {
		h.mu.Unlock()
		return Run{}, fail(ErrBusy, "a test is already running; wait for it to finish or cancel it")
	}
	start := h.now().UTC().Truncate(time.Second)
	est := p.plan.Estimate
	run := Run{
		Status: StatusRunning, Phase: PhasePreparing, Request: req, StartedAt: start, Results: []PromptResult{},
		Resident: ResidentUnknown, Estimate: &est, ExpectedDuration: p.plan.Duration,
		Config: RunConfig{
			HardwareProfileID: t.ProfileID, HardwareFingerprint: t.Fingerprint,
			Backend: t.Backend.Name(), BackendVersion: p.status.Version, RuntimePath: hardware.PathUnknown,
			Model: p.installed.Name, ModelDigest: p.installed.Digest, Quantization: p.installed.Quantization,
			WeightsBytes: p.installed.SizeBytes, CatalogFileID: p.facts.CatalogFileID,
			NumCtx: p.plan.NumCtx, KVCacheType: "unknown",
			SuiteVersion: h.suite.Version, SuiteDigest: h.suite.Digest(), CompletionTokens: h.suite.CompletionTokens,
			Repeats: h.suite.Repeats, DaemonVersion: version.Version,
		},
	}
	if p.plan.Refusal != "" {
		run.Notes = append(run.Notes, "measured although the estimate said: "+p.plan.Refusal)
	}
	for _, pp := range p.plan.Prompts {
		if pp.Skip != "" {
			run.Skipped = append(run.Skipped, Skipped{Prompt: pp.ID, Why: pp.Skip})
		}
	}
	if h.store != nil {
		row, err := h.row(run, p)
		if err == nil {
			run.ID, err = h.store.InsertBenchRun(ctx, row)
		}
		if err != nil {
			h.mu.Unlock()
			return Run{}, fmt.Errorf("bench: recording the run: %w", err)
		}
	}
	runCtx, cancel := context.WithCancel(context.Background()) // detached: closing the page does not abandon the run
	a := &activeRun{id: run.ID, cancel: cancel, done: make(chan struct{}), run: run, subs: map[chan Progress]bool{},
		started: h.now(), planned: p.plan.Duration}
	a.progress = Progress{RunID: run.ID, Status: run.Status, Phase: run.Phase, Steps: p.plan.Requests,
		Message: "Getting ready: checking what is loaded", Remaining: p.plan.Duration, Run: run}
	h.active = a
	h.mu.Unlock()

	go h.execute(runCtx, a, p)
	return run, nil
}

// Cancel stops the run with this id, unloads its model, and waits (up to
// ctx) for it to finish. ErrNotFound when no such run is in progress.
func (h *Harness) Cancel(ctx context.Context, id int64) (Run, error) {
	h.mu.Lock()
	a := h.active
	h.mu.Unlock()
	if a == nil || a.id != id {
		return Run{}, fail(ErrNotFound, "no test with that id is running")
	}
	a.mu.Lock()
	a.cancelled = true
	a.mu.Unlock()
	a.cancel()
	select {
	case <-a.done:
	case <-ctx.Done():
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.run, nil
}

// Active is the id of the run in progress, 0 when none.
func (h *Harness) Active() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active == nil {
		return 0
	}
	return h.active.id
}

// Subscribe follows the run in progress with this id: the current progress,
// then every change on the channel until the run finishes, when the channel
// is closed. ok is false when no such run is in progress (it finished, or
// never was); the caller reads it from the store instead. A slow reader
// misses intermediate events, never the last: every event carries the
// whole run.
func (h *Harness) Subscribe(id int64) (current Progress, ch <-chan Progress, unsubscribe func(), ok bool) {
	h.mu.Lock()
	a := h.active
	h.mu.Unlock()
	if a == nil || a.id != id {
		return Progress{}, nil, func() {}, false
	}
	c := make(chan Progress, h.cfg.ProgressBuffer)
	a.mu.Lock()
	current = a.progress
	if current.Status.Finished() {
		close(c) // its last event is current: nothing more will come
	} else {
		a.subs[c] = true
	}
	a.mu.Unlock()
	return current, c, func() {
		a.mu.Lock()
		if a.subs[c] {
			delete(a.subs, c)
			close(c)
		}
		a.mu.Unlock()
	}, true
}

// publish records progress and sends it to every subscriber, dropping the
// oldest event of a reader that has fallen behind.
func (a *activeRun) publish(p Progress, final bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.progress = p
	a.broadcast(final)
}

// update changes the published progress in place and sends it.
func (a *activeRun) update(change func(*Progress)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	change(&a.progress)
	a.broadcast(false)
}

// broadcast sends a.progress to every subscriber; a.mu is held.
func (a *activeRun) broadcast(final bool) {
	p := a.progress
	for c := range a.subs {
		for {
			select {
			case c <- p:
			default:
				select {
				case <-c:
				default:
				}
				continue
			}
			break
		}
		if final {
			delete(a.subs, c)
			close(c)
		}
	}
}

// ---- the run ---------------------------------------------------------------------

// runState is the run as execute builds it, with the counters progress
// reports.
type runState struct {
	a        *activeRun
	p        *prepared
	run      Run
	step     int
	steps    int
	message  string
	sampler  *Sampler
	tpw      float64 // tokens per word of this model's tokenizer, from the warm-up
	report   backend.LoadReport
	reported bool
}

func (h *Harness) execute(ctx context.Context, a *activeRun, p *prepared) {
	s := &runState{a: a, p: p, run: a.run, steps: p.plan.Requests}
	b := p.target.Backend
	model := p.installed.Name
	var runErr error

	defer func() {
		if r := recover(); r != nil {
			runErr = fmt.Errorf("the harness failed: %v", r)
			h.log.Error("benchmark panicked", "run", a.id, "panic", r)
		}
		h.finish(s, runErr)
	}()

	// 1. Nothing of this model loaded, the machine at rest.
	h.progress(s, PhasePreparing, "Getting ready: freeing this model's memory if it is loaded")
	if loaded, err := b.Running(ctx); err == nil {
		var others []string
		for _, l := range loaded {
			if sameModel(l.Name, model) {
				if ok, why := h.unload(ctx, b, model); !ok {
					runErr = fmt.Errorf("%s was already loaded and could not be unloaded first: %s", model, why)
					return
				}
			} else {
				others = append(others, l.Name)
			}
		}
		if len(others) > 0 {
			s.run.Notes = append(s.run.Notes, fmt.Sprintf("%s was also loaded during the test; it shares the machine with the model being measured",
				strings.Join(others, " and ")))
		}
	}
	if err := h.sleep(ctx, h.cfg.Settle); err != nil {
		runErr = err
		return
	}
	set := chooseProbes(h.env, p.target.Machine.Profile, b)
	s.sampler = newSampler(set, h.cfg.SampleInterval, h.now, model)
	s.sampler.Baseline(ctx)
	sampleCtx, stopSampling := context.WithCancel(ctx)
	sampling := make(chan struct{})
	go func() {
		defer close(sampling)
		s.sampler.Run(sampleCtx, func(Sample) { h.tick(a, s.sampler) })
	}()
	defer func() {
		stopSampling()
		<-sampling
		h.flushSamples(s)
	}()

	// 2. The warm-up: the load, and everything done once. Timed like the
	// rest and discarded; its load time is the load's.
	var observe func(context.Context) backend.LoadReport
	if o, ok := b.(backend.LoadObserver); ok {
		observe = o.ObserveLoad()
	}
	first := p.prompts[0]
	n := 0
	for i := 0; i < h.suite.Warmups; i++ {
		h.progress(s, PhaseLoading, fmt.Sprintf("Loading %s and warming it up", model))
		t, err := h.request(ctx, b, model, h.suite.Request(first, n), s.run.Config.NumCtx)
		n++
		if err != nil {
			runErr = describe(err, model)
			return
		}
		if i == 0 {
			load := figure.MeasuredRate(math.Round(t.LoadMs), "ms")
			s.run.Load = &load
			if words := h.suite.Words(first, n-1); words > 0 && t.PromptTokens > 0 {
				s.tpw = float64(t.PromptTokens) / float64(words)
			}
		}
		s.step++
	}

	// 3. What the load was: where the runtime put the model, which path,
	// cache and attention it chose. Read from the runtime, never assumed. A
	// reading of the machine now, with the model loaded, is step 0's "after"
	// sample — taken here whatever the ticker's phase.
	s.sampler.tick(ctx)
	h.observeLoad(ctx, s, observe)
	h.checkFits(s)
	s.sampler.MarkMeasuring()
	h.save(ctx, s)

	// 4. The timed requests.
	for _, spec := range s.p.prompts {
		if skipped(s.run.Skipped, spec.ID) {
			continue
		}
		var timings []Timing
		for r := 1; r <= h.suite.Repeats; r++ {
			h.progress(s, PhaseMeasuring, fmt.Sprintf("Timing the %s-token prompt, %d of %d", commas(spec.Tokens), r, h.suite.Repeats))
			t, err := h.request(ctx, b, model, h.suite.Request(spec, n), s.run.Config.NumCtx)
			n++
			if err != nil {
				if ctx.Err() == nil && tooLong(err) {
					s.run.Skipped = append(s.run.Skipped, Skipped{Prompt: spec.ID,
						Why: "the runtime refused it as longer than the context it has: " + errText(err)})
					s.step += h.suite.Repeats - r + 1
					timings = nil
					break
				}
				runErr = describe(err, model)
				return
			}
			timings = append(timings, t)
			s.step++
		}
		if len(timings) == 0 {
			continue
		}
		// A prompt whose answers were all too short to time still timed its
		// reading: it stays, with its answering speed absent and why.
		s.run.Results = append(s.run.Results, summarise(spec.ID, timings, h.suite.CompletionTokens, h.cfg))
		h.headline(s)
		h.save(ctx, s)
	}
	switch _, ok := headlineResult(s.run.Results); {
	case len(s.run.Results) == 0:
		runErr = errors.New("no prompt could be timed; the reasons are listed with the prompts")
	case !ok:
		s.run.Notes = append(s.run.Notes, "the model stopped on its own too early on every prompt to time how fast it answers; "+
			"the reading speeds are measured, and the estimate of its answering speed stays as it was")
	}
}

// request sends one prompt and times it: the runtime's own counters, and
// the time to the first token as the client saw it.
func (h *Harness) request(ctx context.Context, b backend.Backend, model, prompt string, numCtx int) (Timing, error) {
	var t Timing
	var final *backend.GenerateEvent
	start := h.now()
	var first time.Time
	err := b.Generate(ctx, backend.GenerateRequest{
		Model: model, Prompt: prompt, Options: h.suite.Options(numCtx), KeepAlive: h.cfg.KeepAlive, Raw: true, NoTruncate: true,
	}, func(ev backend.GenerateEvent) error {
		if first.IsZero() && (ev.Response != "" || ev.Thinking != "") {
			first = h.now()
		}
		if ev.Done {
			e := ev
			final = &e
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	if ctx.Err() != nil {
		return t, ctx.Err()
	}
	if final == nil {
		return t, errors.New("the runtime ended the answer without reporting its timing")
	}
	t = Timing{
		PromptTokens: final.PromptEvalCount, CachedTokens: final.PromptEvalCached, CachedKnown: final.PromptEvalCachedKnown,
		PromptMs: ms(final.PromptEvalDuration), GenTokens: final.EvalCount, GenMs: ms(final.EvalDuration),
		LoadMs: ms(final.LoadDuration), DoneReason: final.DoneReason,
	}
	if !first.IsZero() {
		t.TTFTMs = math.Round(float64(first.Sub(start).Microseconds())) / 1000
	}
	return t, nil
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// observeLoad fills the run's configuration from what the runtime said about
// the load (its log), what it lists as loaded (/api/ps), and a fresh check
// of the runtime, which is also recorded — a load the benchmark caused is
// evidence of the path like any other (ARCHITECTURE.md D-31, D-39).
func (h *Harness) observeLoad(ctx context.Context, s *runState, observe func(context.Context) backend.LoadReport) {
	b := s.p.target.Backend
	cfg := &s.run.Config
	if observe != nil {
		s.report, s.reported = observe(ctx), true
	}
	r := s.report
	if r.KVCacheType != "" {
		cfg.KVCacheType = r.KVCacheType
	}
	cfg.FlashAttention, cfg.FlashAttentionKnown = r.FlashAttention, r.FlashAttentionKnown
	cfg.Parallel = r.Parallel
	if r.Parallel > 0 && r.ContextSize > 0 {
		cfg.EffectiveCtx = r.ContextSize / r.Parallel
	}

	if loaded, err := b.Running(ctx); err == nil {
		for _, l := range loaded {
			if !sameModel(l.Name, cfg.Model) {
				continue
			}
			s.run.RuntimeSizeBytes, s.run.RuntimeSizeVRAMBytes = l.SizeBytes, l.SizeVRAMBytes
			if l.ContextLength > 0 {
				cfg.EffectiveCtx = l.ContextLength
			}
			switch {
			case l.SizeVRAMBytes == 0:
				s.run.Resident = ResidentCPU
			case l.SizeVRAMBytes >= l.SizeBytes:
				s.run.Resident = ResidentGPU
			default:
				s.run.Resident = ResidentSplit
			}
		}
	}
	if r.Layers > 0 && r.LayersOnGPU < r.Layers && s.run.Resident == ResidentGPU {
		// The runtime's list and its log disagree; the log is the load's own account.
		s.run.Resident = ResidentSplit
	}

	status, err := b.Detect(ctx)
	if err == nil {
		if status.Version != "" {
			cfg.BackendVersion = status.Version
		}
		if h.store != nil {
			if _, err := h.store.RecordBackendOn(ctx, s.p.target.Fingerprint, b.Name(), status, ""); err != nil {
				h.log.Warn("recording the runtime check after a benchmark load", "err", err)
			}
		}
	}
	switch {
	case r.RuntimePath != "":
		cfg.RuntimePath = r.RuntimePath
		cfg.RuntimePathEvidence = "the runtime's log for this load: " + strings.Join(deviceList(r.Devices), ", ")
	case s.run.Resident == ResidentCPU:
		cfg.RuntimePath, cfg.RuntimePathEvidence = hardware.PathCPU, "the runtime lists none of the model in graphics memory"
	case err == nil && status.RuntimePaths[0] != "":
		cfg.RuntimePath, cfg.RuntimePathEvidence = status.RuntimePaths[0], status.Detail
	default:
		cfg.RuntimePath = hardware.PathUnknown
		cfg.RuntimePathEvidence = "the runtime did not say which path it took"
		if s.reported && r.Why != "" {
			cfg.RuntimePathEvidence += ": " + r.Why
		}
	}
	if s.run.Resident == ResidentSplit {
		s.run.Notes = append(s.run.Notes, fmt.Sprintf("the runtime put %s of this model in graphics memory and ran the rest on the processor",
			shareWords(s.run.RuntimeSizeVRAMBytes, s.run.RuntimeSizeBytes, r)))
	}
	if cfg.KVCacheType == "unknown" {
		why := "the runtime's log could not be read"
		if s.reported && r.Why != "" {
			why = r.Why
		}
		s.run.Notes = append(s.run.Notes, "which cache setting the runtime used is not known ("+why+"), so this measurement is kept but does not replace the estimate")
	}
}

func deviceList(devices []string) []string {
	if len(devices) == 0 {
		return []string{"no device named"}
	}
	return devices
}

func shareWords(vram, total uint64, r backend.LoadReport) string {
	if r.Layers > 0 {
		return fmt.Sprintf("%d of %d layers", r.LayersOnGPU, r.Layers)
	}
	if total > 0 {
		return fmt.Sprintf("%.0f%%", 100*float64(vram)/float64(total))
	}
	return "part"
}

// checkFits drops the prompts this model's tokenizer makes too long for the
// context, now that the warm-up said how many tokens its words take.
func (h *Harness) checkFits(s *runState) {
	ctxTokens := s.run.Config.EffectiveCtx
	if ctxTokens == 0 {
		ctxTokens = s.run.Config.NumCtx
	}
	if s.tpw <= 0 {
		return
	}
	for _, spec := range s.p.prompts {
		predicted := int(math.Ceil(s.tpw * float64(h.suite.Words(spec, 1))))
		need := predicted + h.suite.CompletionTokens + h.cfg.ContextMargin
		if need > ctxTokens && !skipped(s.run.Skipped, spec.ID) {
			s.run.Skipped = append(s.run.Skipped, Skipped{Prompt: spec.ID, Why: fmt.Sprintf(
				"this model's tokenizer makes it about %s tokens, which with the answer does not fit the %s-token context the runtime is running",
				commas(predicted), commas(ctxTokens))})
			s.steps -= h.suite.Repeats
		}
	}
}

func skipped(list []Skipped, id string) bool {
	for _, s := range list {
		if s.Prompt == id {
			return true
		}
	}
	return false
}

// headline sets the run's headline figures from the shortest prompt timed
// with an answering speed.
func (h *Harness) headline(s *runState) {
	r, ok := headlineResult(s.run.Results)
	if !ok {
		return
	}
	s.run.Headline = r.Prompt
	s.run.GenTPS, s.run.PromptTPS, s.run.TTFT = r.GenTPS, r.PromptTPS, r.TTFT
}

// unload asks the runtime to free the model and waits until its list of
// loaded models no longer has it — on two readings in a row, a poll apart:
// a load still in progress when a run is cancelled during its warm-up can
// appear in the list after the first request to unload, so the request is
// repeated whenever the model shows up again, and one empty reading is not
// taken as the last word.
func (h *Harness) unload(ctx context.Context, b backend.Backend, model string) (bool, string) {
	deadline := h.now().Add(h.cfg.UnloadWait)
	var lastErr error
	asked := false
	absent := 0
	for {
		loaded, err := b.Running(ctx)
		if err != nil {
			lastErr = err
			absent = 0
		} else {
			present := false
			for _, l := range loaded {
				if sameModel(l.Name, model) {
					present = true
				}
			}
			if !present && asked {
				if absent++; absent >= 2 {
					return true, ""
				}
			} else {
				absent = 0
			}
			if present || !asked {
				if err := b.Unload(ctx, model); err != nil {
					lastErr = err
				}
				asked = true
			}
		}
		if !h.now().Before(deadline) {
			if lastErr != nil {
				return false, lastErr.Error()
			}
			return false, fmt.Sprintf("it was still loaded %s after asking", h.cfg.UnloadWait)
		}
		if err := h.sleep(ctx, h.cfg.UnloadPoll); err != nil {
			return false, err.Error()
		}
	}
}

// finish ends a run whatever happened: unload the model (with a context of
// its own — a cancelled run still has to unload), summarise the sampler,
// write the measurement back when the run completed, store the row, and
// tell every subscriber.
func (h *Harness) finish(s *runState, runErr error) {
	a := s.a
	a.mu.Lock()
	cancelled := a.cancelled
	a.mu.Unlock()

	h.progress(s, PhaseUnloading, "Freeing the model's memory")
	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.UnloadWait+10*time.Second)
	defer cancel()
	ok, why := h.unload(ctx, s.p.target.Backend, s.p.installed.Name)
	s.run.Unloaded = &ok
	if !ok {
		s.run.Notes = append(s.run.Notes, "the model could not be confirmed unloaded afterwards: "+why)
	}

	if s.sampler != nil {
		h.applySamples(s)
	}
	switch {
	case cancelled:
		s.run.Status = StatusCancelled
	case runErr != nil:
		s.run.Status, s.run.Error = StatusFailed, errText(runErr)
	default:
		s.run.Status = StatusDone
	}
	now := h.now().UTC().Truncate(time.Second)
	s.run.FinishedAt = &now
	s.run.Phase = PhaseFinished
	if s.run.Status == StatusDone {
		h.writeBack(ctx, s)
	}
	h.save(ctx, s)
	if h.store != nil {
		if c, err := h.comparison(ctx, s.run); err == nil {
			s.run.Comparison = c
		}
	}

	msg := map[Status]string{StatusDone: "Finished", StatusCancelled: "Cancelled", StatusFailed: "Stopped: " + s.run.Error}[s.run.Status]
	a.mu.Lock()
	a.run = s.run
	a.mu.Unlock()
	a.publish(h.progressOf(s, PhaseFinished, msg), true)

	h.mu.Lock()
	if h.active == a {
		h.active = nil
	}
	h.mu.Unlock()
	close(a.done)
}

// applySamples turns the sampler's summary into the run's figures.
func (h *Harness) applySamples(s *runState) {
	sum := s.sampler.summary()
	var notes []string
	if set := s.sampler.set; set.note != "" {
		notes = append(notes, set.note)
	}
	if sum.peakVRAMDelta != nil {
		switch {
		case sum.othersChanged:
			notes = append(notes, "another model was loaded or unloaded during the test, so how much memory this one took is not reported")
		case s.run.Resident == ResidentSplit:
			// The rise covers the graphics part alone; the whole model is not measured.
			v := figure.MeasuredBytes(*sum.peakVRAMDelta)
			s.run.PeakVRAM, s.run.MemorySource = &v, sum.memorySource
			notes = append(notes, "the model was split, so the graphics memory it took is only part of it")
		default:
			v := figure.MeasuredBytes(*sum.peakVRAMDelta)
			s.run.PeakVRAM, s.run.MemorySource = &v, sum.memorySource
		}
	}
	if sum.peakRAM != nil {
		v := figure.MeasuredBytes(*sum.peakRAM)
		s.run.PeakRAM = &v
	}
	if sum.meanUtil != nil {
		v := figure.MeasuredRate(math.Round(*sum.meanUtil), "%")
		s.run.GPUUtil = &v
	}
	if sum.maxTemp != nil {
		v := figure.MeasuredRate(math.Round(*sum.maxTemp), "°C")
		s.run.PeakTemp = &v
	}
	if sum.meanPower != nil {
		v := figure.MeasuredRate(math.Round(*sum.meanPower), "W")
		s.run.Power = &v
	}
	for tool, err := range sum.failures {
		if tool == "api/ps" {
			continue
		}
		notes = append(notes, tool+" did not answer: "+err)
	}
	s.run.SamplerNote = strings.Join(notes, "; ")
}

// tick refreshes the published progress with the sampler's latest reading
// and the clock. It runs on the sampler's goroutine, so it touches only the
// published progress (under its lock), never the run being built.
func (h *Harness) tick(a *activeRun, sm *Sampler) {
	a.mu.Lock()
	p := a.progress
	a.mu.Unlock()
	if p.Status.Finished() {
		return
	}
	a.update(func(p *Progress) {
		p.Last = sm.Last()
		p.ElapsedSeconds = math.Round(h.now().Sub(a.started).Seconds()*10) / 10
		p.Remaining = remaining(a.planned, p.Step, p.Steps)
	})
}

// progress publishes the run's state. An empty phase keeps the current
// one, an empty message the current message. Called on the run's own
// goroutine only.
func (h *Harness) progress(s *runState, phase Phase, message string) {
	if phase != "" {
		s.run.Phase = phase
	}
	if message != "" {
		s.message = message
	}
	s.a.mu.Lock()
	s.a.run = s.run
	s.a.mu.Unlock()
	s.a.publish(h.progressOf(s, s.run.Phase, s.message), false)
}

func (h *Harness) progressOf(s *runState, phase Phase, message string) Progress {
	p := Progress{RunID: s.run.ID, Status: s.run.Status, Phase: phase, Message: message, Step: s.step, Steps: s.steps,
		ElapsedSeconds: math.Round(h.now().Sub(s.a.started).Seconds()*10) / 10, Run: s.run}
	if s.sampler != nil {
		p.Last = s.sampler.Last()
	}
	if !s.run.Status.Finished() {
		p.Remaining = remaining(s.a.planned, s.step, s.steps)
	}
	return p
}

// remaining is the planned duration's share still to come: the time left,
// estimated.
func remaining(planned *figure.Rate, step, steps int) *figure.Rate {
	if planned == nil || steps <= 0 {
		return nil
	}
	left := 1 - float64(step)/float64(steps)
	if left < 0 {
		left = 0
	}
	r := figure.EstimatedRange(math.Round(planned.Low*left), math.Round(planned.High*left), "s")
	return &r
}

// save writes the run's row and any samples not yet written. A failure is
// logged, not fatal: the run in memory is still the truth until it ends.
func (h *Harness) save(ctx context.Context, s *runState) {
	h.flushSamples(s)
	if h.store == nil || s.run.ID == 0 {
		return
	}
	row, err := h.row(s.run, s.p)
	if err == nil {
		err = h.store.UpdateBenchRun(ctx, row)
	}
	if err != nil {
		h.log.Error("storing a benchmark run", "run", s.run.ID, "err", err)
	}
}

func (h *Harness) flushSamples(s *runState) {
	if s.sampler == nil || h.store == nil || s.run.ID == 0 {
		return
	}
	samples := s.sampler.Drain()
	if len(samples) == 0 {
		return
	}
	rows := make([]store.BenchSampleRow, 0, len(samples))
	for _, x := range samples {
		rows = append(rows, store.BenchSampleRow{RunID: s.run.ID, SampledAt: x.At.Format(time.RFC3339Nano), Tool: x.Tool,
			Device: x.Device, GPUUtil: x.GPUUtil, VRAMUsed: x.VRAMUsed, RAMUsed: x.RAMUsed, TempC: x.TempC, PowerW: x.PowerW})
	}
	if err := h.store.AddBenchSamples(context.Background(), rows); err != nil {
		h.log.Error("storing benchmark samples", "run", s.run.ID, "err", err)
	}
}

// row is the run as a benchmark_runs row.
func (h *Harness) row(run Run, p *prepared) (store.BenchRunRow, error) {
	c := run.Config
	enc := func(v any) (string, error) {
		b, err := json.Marshal(v)
		return string(b), err
	}
	cfgJSON, err := enc(storedConfig{RunConfig: c, Request: run.Request, Skipped: run.Skipped, Load: run.Load,
		Expected: run.ExpectedDuration, Replaced: run.Replaced, Headline: run.Headline, Power: run.Power,
		GPUUtil: run.GPUUtil, PeakTemp: run.PeakTemp})
	if err != nil {
		return store.BenchRunRow{}, err
	}
	results, err := enc(run.Results)
	if err != nil {
		return store.BenchRunRow{}, err
	}
	model, err := enc(p.facts)
	if err != nil {
		return store.BenchRunRow{}, err
	}
	est := ""
	if run.Estimate != nil {
		if est, err = enc(run.Estimate); err != nil {
			return store.BenchRunRow{}, err
		}
	}
	notes, err := enc(nonNil(run.Notes))
	if err != nil {
		return store.BenchRunRow{}, err
	}
	row := store.BenchRunRow{
		ID: run.ID, CreatedAt: run.StartedAt.Format(time.RFC3339), Status: string(run.Status),
		HardwareProfileID: c.HardwareProfileID, HardwareFingerprint: c.HardwareFingerprint,
		BackendName: c.Backend, BackendVersion: c.BackendVersion, RuntimePath: string(pathOf(c.RuntimePath)),
		ModelName: c.Model, ModelDigest: c.ModelDigest, InstalledModelID: p.row.ID, CatalogFileID: c.CatalogFileID,
		Quantization: c.Quantization, WeightsBytes: c.WeightsBytes, NumCtx: c.NumCtx,
		KVCacheType: c.KVCacheType, FlashAttention: c.FlashAttention, FlashAttentionKnown: c.FlashAttentionKnown,
		SuiteVersion: c.SuiteVersion, DaemonVersion: c.DaemonVersion, ConfigKey: c.Key(),
		StartedAt:  run.StartedAt.Format(time.RFC3339),
		ConfigJSON: cfgJSON, ResultsJSON: results, ModelJSON: model, EstimateJSON: est, NotesJSON: notes,
		Resident: string(run.Resident), MemorySource: run.MemorySource, SamplerNote: run.SamplerNote,
		Unloaded: run.Unloaded, MeasureAnyway: run.Request.MeasureAnyway, Error: run.Error,
	}
	if c.EffectiveCtx > 0 {
		v := c.EffectiveCtx
		row.EffectiveCtx = &v
	}
	if run.FinishedAt != nil {
		row.FinishedAt = run.FinishedAt.Format(time.RFC3339)
	}
	if run.GenTPS != nil {
		row.GenTPSMedian = ptr(run.GenTPS.Value)
	}
	if run.PromptTPS != nil {
		row.PromptTPSMedian = ptr(run.PromptTPS.Value)
	}
	if run.TTFT != nil {
		row.TTFTMsMedian = ptr(run.TTFT.Value)
	}
	if run.Load != nil {
		row.LoadMs = ptr(run.Load.Value)
	}
	if run.PeakVRAM != nil {
		row.PeakVRAMBytes = ptr(run.PeakVRAM.Value)
	}
	if run.PeakRAM != nil {
		row.PeakRAMBytes = ptr(run.PeakRAM.Value)
	}
	if run.RuntimeSizeBytes > 0 {
		row.PsSizeBytes, row.PsSizeVRAMBytes = ptr(run.RuntimeSizeBytes), ptr(run.RuntimeSizeVRAMBytes)
	}
	return row, nil
}

func ptr[T any](v T) *T { return &v }

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- errors in words --------------------------------------------------------------

// tooLong reports whether the runtime refused a prompt as longer than its
// context (Ollama: "the prompt is longer than the context length currently
// available to the model"; llama-server: "exceeds the available context").
func tooLong(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "longer than the context") || strings.Contains(msg, "exceeds the available context") ||
		strings.Contains(msg, "exceeds the context")
}

// describe turns a request's error into the run's reason, in words.
func describe(err error, model string) error {
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case tooLong(err):
		return fmt.Errorf("even the shortest prompt is longer than the context %s is running with: %s", model, errText(err))
	case strings.Contains(strings.ToLower(err.Error()), "out of memory") ||
		strings.Contains(strings.ToLower(err.Error()), "requires more system memory"):
		return fmt.Errorf("%s did not fit in memory when the runtime loaded it: %s", model, errText(err))
	}
	return fmt.Errorf("the runtime stopped answering: %s", errText(err))
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

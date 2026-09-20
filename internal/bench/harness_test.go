package bench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"advisor/internal/backend"
	"advisor/internal/estimate"
	"advisor/internal/figure"
	"advisor/internal/hardware"
	"advisor/internal/store"
)

// fakeBackend is a runtime that answers like Ollama 0.34 does, without a
// network: it "loads" a model on its first request, streams an answer, and
// reports its own timing — prompt read at promptTPS, answer at genTPS.
type fakeBackend struct {
	mu        sync.Mutex
	installed []backend.Installed
	info      backend.ModelInfo
	loaded    []backend.Loaded
	requests  []backend.GenerateRequest
	unloads   int
	notRun    bool
	// sizeVRAM is what the "load" puts in graphics memory (of sizeBytes).
	sizeBytes, sizeVRAM uint64
	promptTPS, genTPS   float64
	jitter              []float64 // multiplies genTPS per request, cycling
	block               bool      // Generate waits for the context to end
	started             chan struct{}
	loadReport          backend.LoadReport
	tooLongAbove        int // prompt tokens above which the runtime refuses the prompt
	// sticky: Unload does not take (the model stays loaded)
	sticky bool
	delay  time.Duration // how long each answer takes, in real time
	// answer is how many tokens the model writes for a prompt of this many
	// tokens before it stops on its own; nil: the whole budget, 256.
	answer func(promptTokens int) int
}

func (f *fakeBackend) Name() string { return "ollama" }
func (f *fakeBackend) Detect(context.Context) (backend.Status, error) {
	if f.notRun {
		return backend.Status{State: backend.StateInstalledNotRunning}, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	st := backend.Status{State: backend.StateRunning, Version: "0.34.2", Host: "http://127.0.0.1:11434"}
	if len(f.loaded) > 0 {
		st.RuntimePaths = map[int]hardware.RuntimePath{0: hardware.PathCUDA}
	}
	return st, nil
}
func (f *fakeBackend) Models(context.Context) ([]backend.Installed, error) { return f.installed, nil }
func (f *fakeBackend) Show(context.Context, string) (backend.ModelInfo, error) {
	return f.info, nil
}
func (f *fakeBackend) Running(context.Context) ([]backend.Loaded, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]backend.Loaded(nil), f.loaded...), nil
}
func (f *fakeBackend) setLoaded(l ...backend.Loaded) {
	f.mu.Lock()
	f.loaded = l
	f.mu.Unlock()
}
func (f *fakeBackend) Pull(context.Context, backend.ModelSource, func(backend.PullProgress)) error {
	return nil
}
func (f *fakeBackend) Unload(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unloads++
	if f.sticky {
		return nil
	}
	var keep []backend.Loaded
	for _, l := range f.loaded {
		if !sameModel(l.Name, name) {
			keep = append(keep, l)
		}
	}
	f.loaded = keep
	return nil
}
func (f *fakeBackend) Delete(context.Context, string) error                         { return nil }
func (f *fakeBackend) Install(context.Context, func(backend.InstallProgress)) error { return nil }
func (f *fakeBackend) Start(context.Context) error                                  { return nil }
func (f *fakeBackend) ObserveLoad() func(context.Context) backend.LoadReport {
	return func(context.Context) backend.LoadReport { return f.loadReport }
}

func (f *fakeBackend) Generate(ctx context.Context, req backend.GenerateRequest, on func(backend.GenerateEvent) error) error {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	n := len(f.requests)
	var load time.Duration
	present := false
	for _, l := range f.loaded {
		present = present || sameModel(l.Name, req.Model)
	}
	if !present {
		load = 1500 * time.Millisecond
		ctxLen, _ := req.Options["num_ctx"].(int)
		f.loaded = append(f.loaded, backend.Loaded{Name: req.Model, SizeBytes: f.sizeBytes, SizeVRAMBytes: f.sizeVRAM, ContextLength: ctxLen})
	}
	block, started := f.block, f.started
	f.mu.Unlock()
	if block {
		if started != nil {
			close(started)
		}
		<-ctx.Done()
		return ctx.Err()
	}
	if !req.Raw || !req.NoTruncate {
		return errors.New("benchmark requests must be raw and must not be truncated")
	}
	words := len(strings.Fields(req.Prompt))
	tokens := int(float64(words)*1.2) + 1
	if f.tooLongAbove > 0 && tokens > f.tooLongAbove {
		return errors.New("ollama: generate: 400 Bad Request: the prompt is longer than the context length currently available to the model")
	}
	time.Sleep(f.delay)
	gen := f.genTPS
	if len(f.jitter) > 0 {
		gen *= f.jitter[(n-1)%len(f.jitter)]
	}
	if err := on(backend.GenerateEvent{Response: "The"}); err != nil {
		return err
	}
	answer, reason := 256, "length"
	if f.answer != nil {
		if answer = f.answer(tokens); answer < 256 {
			reason = "stop"
		}
	}
	return on(backend.GenerateEvent{Done: true, DoneReason: reason, LoadDuration: load,
		PromptEvalCount: tokens, PromptEvalCached: 1, PromptEvalCachedKnown: true,
		PromptEvalDuration: time.Duration(float64(tokens-1) / f.promptTPS * float64(time.Second)),
		EvalCount:          answer, EvalDuration: time.Duration(float64(answer) / gen * float64(time.Second))})
}

// llamaInfo is what Ollama's /api/show says about llama3.1:8b.
func llamaInfo() backend.ModelInfo {
	return backend.ModelInfo{Name: "llama3.1:8b", Architecture: "llama", Capabilities: []string{"completion", "tools"},
		ParameterSize: "8.0B", Quantization: "Q4_K_M", Details: map[string]any{
			"general.architecture": "llama", "general.parameter_count": float64(8030261312), "general.file_type": float64(15),
			"llama.block_count": float64(32), "llama.context_length": float64(131072), "llama.embedding_length": float64(4096),
			"llama.attention.head_count": float64(32), "llama.attention.head_count_kv": float64(8),
		}}
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		installed: []backend.Installed{{Name: "llama3.1:8b", Digest: "sha256:46e0c10c039e", SizeBytes: 4_920_753_328,
			Family: "llama", ParameterSize: "8.0B", Quantization: "Q4_K_M"}},
		info: llamaInfo(), sizeBytes: 5_463_000_000, sizeVRAM: 5_463_000_000, promptTPS: 3000, genTPS: 120, delay: 3 * time.Millisecond,
		loadReport: backend.LoadReport{Read: true, RuntimePath: hardware.PathCUDA, Devices: []string{"CUDA0", "CPU"},
			FlashAttention: true, FlashAttentionKnown: true, KVCacheType: "f16", LayersOnGPU: 33, Layers: 33,
			ContextSize: 8192, Parallel: 1, Evidence: []string{"load_tensors: CUDA0 model buffer size = 4403.49 MiB"}},
	}
}

func goldenProfile(t *testing.T, name string) hardware.Profile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "hardware", "testdata", "golden", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var p hardware.Profile
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

type rig struct {
	h   *Harness
	st  *store.Store
	b   *fakeBackend
	t   Target
	env *fakeSysEnv
}

// newRig is a harness on a fresh store, the Windows RTX 5070 Ti desktop of
// the fleet, and a fake Ollama with llama3.1:8b installed. nvidia-smi
// answers 413 MiB used before the load and 5,900 MiB after.
func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "advisor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p := goldenProfile(t, "WindowsNVIDIADesktop")
	row, err := st.AddHardwareProfile(ctx, p, "test")
	if err != nil {
		t.Fatal(err)
	}
	b := newFakeBackend()
	if err := st.UpsertInstalledModels(ctx, "ollama", b.installed); err != nil {
		t.Fatal(err)
	}
	est, err := estimate.New()
	if err != nil {
		t.Fatal(err)
	}
	suite, err := DefaultSuite()
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.SampleInterval, cfg.Settle, cfg.UnloadPoll, cfg.UnloadWait = 2*time.Millisecond, 0, time.Millisecond, 300*time.Millisecond
	h := NewWith(st, nil, suite, cfg)
	// A clock that moves 3 ms every time it is read, so the time to the
	// first token is never zero however fast the fake answers.
	var clockMu sync.Mutex
	clock := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		clock = clock.Add(3 * time.Millisecond)
		return clock
	}
	env := &fakeSysEnv{os: "windows", paths: map[string]string{"nvidia-smi": "nvidia-smi"}, mem: [2]uint64{32 << 30, 20 << 30}}
	env.cmds = map[string]func() (string, error){"nvidia-smi": func() (string, error) {
		if loaded, _ := b.Running(ctx); len(loaded) > 0 {
			return "0, 96, 5900, 61, 250.0\n", nil
		}
		return "0, 2, 413, 40, 22.5\n", nil
	}}
	h.env = env
	return &rig{h: h, st: st, b: b, env: env, t: Target{Backend: b, Machine: estimate.Machine{Profile: p}, Estimator: est,
		ProfileID: row.ID, Fingerprint: row.Fingerprint}}
}

// wait follows a run to its end through the progress stream.
func (r *rig) wait(t *testing.T, id int64) (Run, []Progress) {
	t.Helper()
	_, ch, unsubscribe, ok := r.h.Subscribe(id)
	var events []Progress
	if ok {
		defer unsubscribe()
		timeout := time.After(10 * time.Second)
	loop:
		for {
			select {
			case p, open := <-ch:
				if !open {
					break loop
				}
				events = append(events, p)
			case <-timeout:
				t.Fatal("the run did not finish")
			}
		}
	}
	for r.h.Active() == id {
		time.Sleep(time.Millisecond)
	}
	run, err := r.h.Get(context.Background(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	return run, events
}

// A whole run: one warm-up and three timed requests per prompt, medians
// and spreads from the runtime's own counters, the context the runtime ran,
// the resources, the model unloaded, everything stored.
func TestARunMeasuresEveryPromptAndStoresTheWholeContext(t *testing.T) {
	r := newRig(t)
	r.b.jitter = []float64{1, 1.02, 0.99, 1.01}
	started, err := r.h.Start(context.Background(), r.t, Request{Model: "llama3.1:8b", NumCtx: 8192})
	if err != nil {
		t.Fatal(err)
	}
	if started.ID == 0 || started.Status != StatusRunning || started.Estimate == nil || started.ExpectedDuration == nil {
		t.Fatalf("started: %+v", started)
	}
	run, events := r.wait(t, started.ID)

	if run.Status != StatusDone || len(run.Results) != 3 || len(run.Skipped) != 0 {
		t.Fatalf("run: %s %q, results %d, skipped %+v", run.Status, run.Error, len(run.Results), run.Skipped)
	}
	// 1 warm-up + 3 × 3 timed requests, all raw, the numbered lead first,
	// the suite's options.
	if len(r.b.requests) != 10 {
		t.Fatalf("%d requests", len(r.b.requests))
	}
	for i, req := range r.b.requests {
		if !strings.HasPrefix(req.Prompt, strconv.Itoa(i)+".\n\n") || req.Options["num_ctx"] != 8192 || req.Options["temperature"] != float64(0) ||
			req.Options["num_predict"] != 256 || req.Options["seed"] != 42 || req.KeepAlive != "5m" {
			t.Fatalf("request %d: %q… %v", i, req.Prompt[:10], req.Options)
		}
	}
	head := run.Results[0]
	if head.Prompt != "500" || head.Runs != 3 || len(head.Timings) != 3 || head.GenTokens != 256 {
		t.Fatalf("headline result %+v", head)
	}
	// Generation: 120 tok/s × {1.02, 0.99, 1.01} (the warm-up took the 1):
	// median 121.2, spread (122.4 − 118.8) / 121.2 = 3%.
	if head.GenTPS.Source != figure.Measured || head.GenTPS.IsRange() || head.GenTPS.Value != 121.2 || head.SpreadPct != 3 {
		t.Fatalf("generation %+v spread %v", head.GenTPS, head.SpreadPct)
	}
	// Prompt: the cached token is not counted — exactly 3000 tok/s.
	if head.PromptTPS == nil || head.PromptTPS.Value < 2999.9 || head.PromptTPS.Value > 3000.1 || head.TTFT == nil || head.TTFT.Value <= 0 || head.TTFT.Unit != "ms" {
		t.Fatalf("prompt %+v ttft %+v", head.PromptTPS, head.TTFT)
	}
	if run.Headline != "500" || run.GenTPS.Value != 121.2 || run.Load == nil || run.Load.Value != 1500 {
		t.Fatalf("run headline %s %+v load %+v", run.Headline, run.GenTPS, run.Load)
	}

	c := run.Config
	if c.RuntimePath != hardware.PathCUDA || c.KVCacheType != "f16" || !c.FlashAttention || !c.FlashAttentionKnown ||
		c.EffectiveCtx != 8192 || c.Parallel != 1 || c.BackendVersion != "0.34.2" || c.ModelDigest != "sha256:46e0c10c039e" ||
		c.SuiteVersion != "2" || c.SuiteDigest == "" || c.HardwareProfileID == 0 || c.HardwareFingerprint == "" || c.Repeats != 3 {
		t.Fatalf("config %+v", c)
	}
	if run.Resident != ResidentGPU || run.RuntimeSizeBytes != 5_463_000_000 {
		t.Fatalf("resident %q size %d", run.Resident, run.RuntimeSizeBytes)
	}
	// nvidia-smi: 5,900 − 413 MiB taken; 96% busy; 61 °C; 250 W.
	if run.PeakVRAM == nil || run.PeakVRAM.Value != (5900-413)<<20 || run.PeakVRAM.Source != figure.Measured ||
		!strings.Contains(run.MemorySource, "nvidia-smi") {
		t.Fatalf("peak vram %+v (%s)", run.PeakVRAM, run.MemorySource)
	}
	if run.GPUUtil == nil || run.GPUUtil.Value != 96 || run.PeakTemp == nil || run.PeakTemp.Value != 61 || run.Power == nil ||
		run.PeakRAM == nil || run.PeakRAM.Value != 12<<30 {
		t.Fatalf("resources util %+v temp %+v power %+v ram %+v", run.GPUUtil, run.PeakTemp, run.Power, run.PeakRAM)
	}
	if run.Unloaded == nil || !*run.Unloaded {
		t.Fatalf("the model must be unloaded at the end: %v", run.Unloaded)
	}
	if loaded, _ := r.b.Running(context.Background()); len(loaded) != 0 {
		t.Fatalf("still loaded: %+v", loaded)
	}
	if len(run.Samples) == 0 {
		t.Fatal("samples should be stored")
	}
	// The model is not in the curated list: kept, not written back, and said.
	if run.Replaced || !hasNote(run.Notes, "curated list does not know") {
		t.Fatalf("replaced %v notes %v", run.Replaced, run.Notes)
	}

	// The stream: phases in order, the last event the finished run.
	if len(events) < 3 {
		t.Fatalf("%d progress events", len(events))
	}
	last := events[len(events)-1]
	if last.Status != StatusDone || last.Phase != PhaseFinished || last.Run.ID != run.ID || len(last.Run.Results) != 3 {
		t.Fatalf("last event %+v", last)
	}
	sawMeasuring := false
	for _, e := range events {
		sawMeasuring = sawMeasuring || e.Phase == PhaseMeasuring
	}
	if !sawMeasuring {
		t.Fatal("the stream should show the measuring phase")
	}

	// The backend check after the load was recorded: the path is now
	// established for the recommendation engine.
	if row, err := r.st.LastObservedRuntimePaths(context.Background(), "ollama", r.t.Fingerprint); err != nil || row.RuntimePaths[0] != hardware.PathCUDA {
		t.Fatalf("observed path: %+v %v", row, err)
	}

	// History, and a second run of the same configuration compared with it.
	second, err := r.h.Start(context.Background(), r.t, Request{Model: "llama3.1:8b", NumCtx: 8192})
	if err != nil {
		t.Fatal(err)
	}
	run2, _ := r.wait(t, second.ID)
	if run2.Comparison == nil || run2.Comparison.RunID != run.ID || run2.Config.Key() != run.Config.Key() {
		t.Fatalf("comparison %+v", run2.Comparison)
	}
	hist, err := r.h.History(context.Background(), HistoryFilter{})
	if err != nil || len(hist.Runs) != 2 || hist.Runs[0].ID != run2.ID || hist.Runs[0].Samples != nil {
		t.Fatalf("history %+v %v", hist, err)
	}
	// Evidence: the runs calibrate the cuda path.
	ev, err := r.h.Evidence(context.Background(), r.t.Fingerprint, "ollama")
	if err != nil || len(ev.Observations) != 2 || !ev.Observations[0].Resident || ev.Observations[0].GenerationTPS != run2.GenTPS.Value {
		t.Fatalf("evidence %+v %v", ev, err)
	}
	if cal := r.t.Estimator.Calibrate(ev.Observations); cal == nil || len(cal.Points[hardware.PathCUDA]) != 1 {
		t.Fatalf("calibration %+v", cal)
	}

	// The next plan of this configuration shows the latest measurement in
	// the estimate's place; a plan at another context has none.
	plan, err := r.h.Plan(context.Background(), r.t, Request{Model: "llama3.1:8b", NumCtx: 8192})
	if err != nil || plan.Measured == nil || plan.Measured.RunID != run2.ID || plan.Measured.GenTPS != *run2.GenTPS || plan.Measured.At.IsZero() {
		t.Fatalf("plan measured %+v %v", plan.Measured, err)
	}
	if plan, _ := r.h.Plan(context.Background(), r.t, Request{Model: "llama3.1:8b", NumCtx: 4096}); plan.Measured != nil {
		t.Fatalf("another context's plan: %+v", plan.Measured)
	}
}

// A model that stops on its own before MinAnswerTokens times its reading
// but not its answering: the prompt stays with its reading speed and says
// why the answering speed is absent, and the run's headline moves to the
// next prompt that has one. When no prompt has one, the run still finishes,
// says so, and replaces no estimate.
func TestShortAnswersTimeTheReadingNotTheAnswering(t *testing.T) {
	r := newRig(t)
	r.b.answer = func(tokens int) int {
		if tokens < 1000 {
			return 30 // the 500-token prompt: the model stops after 30 tokens
		}
		return 256
	}
	started, err := r.h.Start(context.Background(), r.t, Request{Model: "llama3.1:8b", NumCtx: 4096})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := r.wait(t, started.ID)
	if run.Status != StatusDone || len(run.Results) != 2 {
		t.Fatalf("run %s %q results %+v", run.Status, run.Error, run.Results)
	}
	short := run.Results[0]
	if short.Prompt != "500" || short.GenTPS != nil || !strings.Contains(short.GenUnknown, "after 30 tokens") || short.PromptTPS == nil || short.TTFT == nil {
		t.Fatalf("the short prompt: %+v", short)
	}
	if run.Headline != "2000" || run.GenTPS == nil || *run.GenTPS != *run.Results[1].GenTPS {
		t.Fatalf("headline %q %+v", run.Headline, run.GenTPS)
	}
	back, err := r.h.Get(context.Background(), run.ID, false)
	if err != nil || back.Headline != "2000" || back.GenTPS == nil || back.Results[0].GenTPS != nil || back.Results[0].GenUnknown == "" {
		t.Fatalf("stored: %+v %v", back, err)
	}

	r.b.answer = func(int) int { return 12 }
	started, err = r.h.Start(context.Background(), r.t, Request{Model: "llama3.1:8b", NumCtx: 4096})
	if err != nil {
		t.Fatal(err)
	}
	run, _ = r.wait(t, started.ID)
	if run.Status != StatusDone || run.GenTPS != nil || run.Replaced || !hasNote(run.Notes, "too early on every prompt") ||
		run.Results[0].PromptTPS == nil || run.Comparison != nil {
		t.Fatalf("no answering speed at all: %s %+v notes %v", run.Status, run.GenTPS, run.Notes)
	}
}

func hasNote(notes []string, s string) bool {
	for _, n := range notes {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// Prompts that do not fit the context are planned out, and a prompt the
// runtime refuses as too long is skipped with its reason, not a failure.
func TestPromptsThatDoNotFitAreSkippedWithAReason(t *testing.T) {
	r := newRig(t)
	plan, err := r.h.Plan(context.Background(), r.t, Request{Model: "llama3.1:8b"})
	if err != nil {
		t.Fatal(err)
	}
	// On a 16 GB card Ollama runs a model at 4,096 unless told otherwise.
	if plan.NumCtx != 4096 || plan.NumCtxSource != "ollama_default" {
		t.Fatalf("plan context %d (%s)", plan.NumCtx, plan.NumCtxSource)
	}
	if plan.Prompts[0].Runs != 3 || plan.Prompts[1].Runs != 3 || plan.Prompts[2].Runs != 0 || !strings.Contains(plan.Prompts[2].Skip, "7,792") {
		t.Fatalf("prompts %+v", plan.Prompts)
	}
	if plan.Requests != 7 || plan.Duration == nil || plan.Duration.Source != figure.Estimated || plan.Duration.Unit != "s" {
		t.Fatalf("requests %d duration %+v", plan.Requests, plan.Duration)
	}

	// The runtime refuses the 2,000-token prompt at run time.
	r.b.tooLongAbove = 1500
	started, err := r.h.Start(context.Background(), r.t, Request{Model: "llama3.1:8b", NumCtx: 8192})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := r.wait(t, started.ID)
	if run.Status != StatusDone || len(run.Results) != 1 || len(run.Skipped) != 2 || !strings.Contains(run.Skipped[0].Why, "longer than the context") {
		t.Fatalf("run %s %q results %d skipped %+v", run.Status, run.Error, len(run.Results), run.Skipped)
	}

	if _, err := r.h.Plan(context.Background(), r.t, Request{Model: "llama3.1:8b", NumCtx: 512}); !errors.Is(err, ErrNothingFits) {
		t.Fatalf("nothing fits 512: %v", err)
	}
	if _, err := r.h.Plan(context.Background(), r.t, Request{Model: "llama3.1:8b", Prompts: []string{"9000"}}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("unknown prompt: %v", err)
	}
}

// A configuration the estimator says would spill is refused — unless the
// request says measure_anyway — and the refusal says why in words.
func TestASpillingConfigurationIsRefusedUnlessMeasuredAnyway(t *testing.T) {
	r := newRig(t)
	big := r.b.installed[0]
	big.Name, big.Digest, big.SizeBytes = "llama3.3:70b", "sha256:70b", 26_000_000_000
	r.b.installed = append(r.b.installed, big)
	info := llamaInfo()
	info.Details["llama.block_count"] = float64(80)
	info.Details["general.parameter_count"] = float64(70_553_706_496)
	r.b.info = info

	_, err := r.h.Start(context.Background(), r.t, Request{Model: "llama3.3:70b", NumCtx: 4096})
	var e *Error
	if !errors.As(err, &e) || !errors.Is(err, ErrRefused) || e.Plan == nil || e.Plan.RefusalCode != "would_spill" ||
		!strings.Contains(e.Message, "processor") {
		t.Fatalf("refusal: %v", err)
	}
	if len(r.b.requests) != 0 {
		t.Fatal("a refused run sends nothing to the model")
	}

	r.b.sizeVRAM = 14_000_000_000
	r.b.sizeBytes = 45_000_000_000
	r.b.loadReport.LayersOnGPU = 25
	r.b.loadReport.Layers = 81
	started, err := r.h.Start(context.Background(), r.t, Request{Model: "llama3.3:70b", NumCtx: 4096, Prompts: []string{"500"}, MeasureAnyway: true})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := r.wait(t, started.ID)
	if run.Status != StatusDone || run.Resident != ResidentSplit || !hasNote(run.Notes, "measured although") || !hasNote(run.Notes, "25 of 81 layers") {
		t.Fatalf("measured anyway: %s resident %s notes %v", run.Status, run.Resident, run.Notes)
	}
	if run.PeakVRAM == nil || !strings.Contains(run.SamplerNote, "only part of it") {
		t.Fatalf("a split model's graphics memory is only part of it: %+v %q", run.PeakVRAM, run.SamplerNote)
	}
}

// Cancel stops the run where it is, unloads the model — also when the
// cancel lands during the load — and waits until the runtime no longer
// lists it.
func TestCancelUnloadsTheModel(t *testing.T) {
	r := newRig(t)
	r.b.block = true
	r.b.started = make(chan struct{})
	started, err := r.h.Start(context.Background(), r.t, Request{Model: "llama3.1:8b", NumCtx: 4096})
	if err != nil {
		t.Fatal(err)
	}
	<-r.b.started // the warm-up is loading the model

	if _, err := r.h.Start(context.Background(), r.t, Request{Model: "llama3.1:8b"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second run while one runs: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	run, err := r.h.Cancel(ctx, started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusCancelled || run.Unloaded == nil || !*run.Unloaded {
		t.Fatalf("cancelled run: %s unloaded %v", run.Status, run.Unloaded)
	}
	if loaded, _ := r.b.Running(context.Background()); len(loaded) != 0 || r.b.unloads == 0 {
		t.Fatalf("after cancel: loaded %+v, unloads %d", loaded, r.b.unloads)
	}
	stored, err := r.h.Get(context.Background(), started.ID, false)
	if err != nil || stored.Status != StatusCancelled || stored.Unloaded == nil || !*stored.Unloaded || stored.FinishedAt == nil {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	if _, err := r.h.Cancel(ctx, started.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancelling a finished run: %v", err)
	}

	// A runtime that will not let go: the run says so.
	r2 := newRig(t)
	r2.b.block, r2.b.sticky = true, true
	r2.b.started = make(chan struct{})
	s2, _ := r2.h.Start(context.Background(), r2.t, Request{Model: "llama3.1:8b", NumCtx: 4096})
	<-r2.b.started
	run2, _ := r2.h.Cancel(ctx, s2.ID)
	if run2.Unloaded == nil || *run2.Unloaded || !hasNote(run2.Notes, "could not be confirmed unloaded") {
		t.Fatalf("sticky: %v %v", run2.Unloaded, run2.Notes)
	}
}

// What the harness refuses before starting, each with its reason.
func TestPlanErrors(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.h.Plan(ctx, r.t, Request{Model: "mistral:7b"}); !errors.Is(err, ErrModelNotInstalled) {
		t.Errorf("not installed: %v", err)
	}
	if _, err := r.h.Plan(ctx, r.t, Request{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("no model: %v", err)
	}
	r.b.info.Capabilities = []string{"embedding"}
	if _, err := r.h.Plan(ctx, r.t, Request{Model: "llama3.1:8b"}); !errors.Is(err, ErrNotTextModel) {
		t.Errorf("embedding model: %v", err)
	}
	r.b.notRun = true
	if _, err := r.h.Plan(ctx, r.t, Request{Model: "llama3.1:8b"}); !errors.Is(err, ErrBackendNotRunning) {
		t.Errorf("not running: %v", err)
	}
}

// A run the daemon was doing when it stopped is closed as failed at the
// next start.
func TestRecoverClosesInterruptedRuns(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	id, err := r.st.InsertBenchRun(ctx, store.BenchRunRow{Status: "running", HardwareProfileID: r.t.ProfileID,
		BackendName: "ollama", ModelName: "llama3.1:8b", NumCtx: 4096, ConfigJSON: `{"model":"llama3.1:8b"}`})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.h.Recover(ctx); err != nil || n != 1 {
		t.Fatalf("recovered %d %v", n, err)
	}
	run, err := r.h.Get(ctx, id, false)
	if err != nil || run.Status != StatusFailed || !strings.Contains(run.Error, "stopped") {
		t.Fatalf("%+v %v", run, err)
	}
}

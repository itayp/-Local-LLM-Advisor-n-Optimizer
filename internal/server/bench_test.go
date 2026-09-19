package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"advisor/internal/backend"
	"advisor/internal/bench"
	"advisor/internal/estimate"
	"advisor/internal/figure"
	"advisor/internal/hardware"
)

// benchOllama is a fake Ollama that can run a benchmark: it loads a model
// on its first request and answers with its own timing (800 tok/s reading,
// 150 tok/s answering), and reports the load as Ollama 0.34's log would.
type benchOllama struct {
	*fakeBackend
	mu      sync.Mutex
	loaded  []backend.Loaded
	block   bool
	started chan struct{}
}

func newBenchOllama() *benchOllama {
	return &benchOllama{fakeBackend: &fakeBackend{name: "ollama",
		status: backend.Status{State: backend.StateRunning, Version: "0.34.2", Host: "http://127.0.0.1:11434"},
		models: []backend.Installed{{Name: "llama3.2:1b", Digest: "sha256:1b", SizeBytes: 1_321_098_329, Family: "llama",
			ParameterSize: "1.2B", Quantization: "Q8_0"}},
	}}
}

func (o *benchOllama) Detect(ctx context.Context) (backend.Status, error) {
	st, err := o.fakeBackend.Detect(ctx)
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.loaded) > 0 {
		st.RuntimePaths = map[int]hardware.RuntimePath{0: hardware.PathCUDA}
	}
	return st, err
}

func (o *benchOllama) Show(context.Context, string) (backend.ModelInfo, error) {
	return backend.ModelInfo{Name: "llama3.2:1b", Capabilities: []string{"completion", "tools"}}, nil
}

func (o *benchOllama) Running(context.Context) ([]backend.Loaded, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]backend.Loaded(nil), o.loaded...), nil
}

func (o *benchOllama) Unload(context.Context, string) error {
	o.mu.Lock()
	o.loaded = nil
	o.mu.Unlock()
	return nil
}

func (o *benchOllama) ObserveLoad() func(context.Context) backend.LoadReport {
	return func(context.Context) backend.LoadReport {
		return backend.LoadReport{Read: true, RuntimePath: hardware.PathCUDA, Devices: []string{"CUDA0"}, KVCacheType: "f16",
			FlashAttention: true, FlashAttentionKnown: true, LayersOnGPU: 17, Layers: 17, ContextSize: 8192, Parallel: 1}
	}
}

func (o *benchOllama) Generate(ctx context.Context, req backend.GenerateRequest, on func(backend.GenerateEvent) error) error {
	o.mu.Lock()
	var load time.Duration
	if len(o.loaded) == 0 {
		load = 700 * time.Millisecond
		o.loaded = []backend.Loaded{{Name: req.Model, SizeBytes: 1_900_000_000, SizeVRAMBytes: 1_900_000_000, ContextLength: 8192}}
	}
	block, started := o.block, o.started
	o.mu.Unlock()
	if block {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	time.Sleep(2 * time.Millisecond)
	tokens := len(strings.Fields(req.Prompt))*6/5 + 1
	if err := on(backend.GenerateEvent{Response: "The"}); err != nil {
		return err
	}
	return on(backend.GenerateEvent{Done: true, DoneReason: "length", LoadDuration: load, PromptEvalCount: tokens,
		PromptEvalCached: 1, PromptEvalCachedKnown: true, PromptEvalDuration: time.Duration(float64(tokens-1) / 800 * float64(time.Second)),
		EvalCount: 256, EvalDuration: 256 * time.Second / 150})
}

// benchTestServer is the recommendation test server (a refreshed one-model
// catalogue, an RTX 3090) with a runtime that can benchmark, llama3.2:1b
// installed and mapped onto the catalogue, and a harness that does not wait.
func benchTestServer(t *testing.T, o *benchOllama) (*Server, *httptest.Server) {
	t.Helper()
	srv, ts := recommendTestServer(t, o.fakeBackend)
	srv.backendList = func() []backend.Backend { return []backend.Backend{o} }
	srv.RecordBackends(context.Background())
	suite, err := bench.DefaultSuite()
	if err != nil {
		t.Fatal(err)
	}
	cfg := bench.DefaultConfig()
	cfg.SampleInterval, cfg.Settle, cfg.UnloadPoll, cfg.UnloadWait = 5*time.Millisecond, 0, time.Millisecond, time.Second
	srv.bench = bench.NewWith(srv.store, srv.log, suite, cfg)
	return srv, ts
}

// events reads a run's progress stream to its end.
func events(t *testing.T, url string) []bench.Progress {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var out []bench.Progress
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			var p bench.Progress
			if err := json.Unmarshal([]byte(data), &p); err != nil {
				t.Fatalf("event %q: %v", data, err)
			}
			out = append(out, p)
		}
	}
	return out
}

func postJSON(t *testing.T, url, body string, want int, into any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("POST %s: %d, want %d: %s", url, resp.StatusCode, want, b)
	}
	if into != nil {
		if err := json.NewDecoder(bytes.NewReader(b)).Decode(into); err != nil {
			t.Fatal(err)
		}
	}
}

// The whole API: plan, start, follow the stream to the end, read the run
// back with its samples, find it in the history — and the measurement now
// stands where the estimate of that configuration was.
func TestBenchmarkEndpoints(t *testing.T) {
	o := newBenchOllama()
	_, ts := benchTestServer(t, o)

	var plan bench.Plan
	getJSON(t, ts.URL+"/api/bench/plan?model=llama3.2:1b", http.StatusOK, &plan)
	// A 24 GiB card: Ollama's default context is 32k, every prompt fits.
	if plan.NumCtx != 32768 || plan.ModelSource != "catalogue" || plan.Refusal != "" || plan.Requests != 10 || plan.Duration == nil {
		t.Fatalf("plan %+v", plan)
	}
	getJSON(t, ts.URL+"/api/bench/plan?model=llama3.2:1b&num_ctx=512", http.StatusOK, &plan)
	if plan.RefusalCode != "nothing_fits" {
		t.Fatalf("a context nothing fits: %+v", plan)
	}

	var started bench.Run
	postJSON(t, ts.URL+"/api/bench", `{"model":"llama3.2:1b","num_ctx":8192}`, http.StatusAccepted, &started)
	evs := events(t, ts.URL+"/api/bench/"+itoa(started.ID))
	last := evs[len(evs)-1]
	if last.Status != bench.StatusDone || last.Phase != bench.PhaseFinished || len(last.Run.Results) != 3 {
		t.Fatalf("last event: %s %s %q, %d results", last.Status, last.Phase, last.Message, len(last.Run.Results))
	}

	var run bench.Run
	getJSON(t, ts.URL+"/api/bench/"+itoa(started.ID), http.StatusOK, &run)
	if run.Status != bench.StatusDone || !run.Replaced || run.Config.CatalogFileID == 0 || run.Config.RuntimePath != hardware.PathCUDA ||
		run.GenTPS == nil || run.GenTPS.Value != 150 || run.GenTPS.Source != figure.Measured || run.Unloaded == nil || !*run.Unloaded || len(run.Samples) == 0 {
		t.Fatalf("run: %+v", run)
	}
	// A finished run's stream is one event.
	if evs := events(t, ts.URL+"/api/bench/"+itoa(started.ID)); len(evs) != 1 || evs[0].Status != bench.StatusDone {
		t.Fatalf("finished stream: %+v", evs)
	}

	var hist bench.History
	getJSON(t, ts.URL+"/api/bench/history", http.StatusOK, &hist)
	if len(hist.Runs) != 1 || hist.Runs[0].ID != run.ID || len(hist.Runs[0].Samples) != 0 {
		t.Fatalf("history %+v", hist)
	}

	// Product rule 4's second sentence: the estimate of the measured
	// configuration is the measurement now; another context is still an
	// estimate, calibrated by it.
	var cat CatalogResponse
	getJSON(t, ts.URL+"/api/catalog", http.StatusOK, &cat)
	id := itoa(cat.Families[0].Sizes[0].ID)
	var fit ModelFitResponse
	getJSON(t, ts.URL+"/api/models/"+id+"/fit?ctx=8192", http.StatusOK, &fit)
	g := fit.Fits[0].Estimate.Speed.Generation
	if g.Source != figure.Measured || g.Value != 150 || fit.Fits[0].Estimate.Basis.SpeedSource != estimate.SpeedMeasured {
		t.Fatalf("measured configuration: %+v", fit.Fits[0].Estimate.Speed)
	}
	getJSON(t, ts.URL+"/api/models/"+id+"/fit?ctx=16384", http.StatusOK, &fit)
	s := fit.Fits[0].Estimate.Speed
	if s.Generation.Source != figure.Estimated || !s.Calibrated || s.CalibratedFrom != "llama3.2:1b" {
		t.Fatalf("a neighbouring configuration: %+v", s)
	}
}

func TestBenchmarkCancelAndErrors(t *testing.T) {
	o := newBenchOllama()
	o.block, o.started = true, make(chan struct{})
	_, ts := benchTestServer(t, o)

	var started bench.Run
	postJSON(t, ts.URL+"/api/bench", `{"model":"llama3.2:1b","num_ctx":8192}`, http.StatusAccepted, &started)
	<-o.started
	postJSON(t, ts.URL+"/api/bench", `{"model":"llama3.2:1b"}`, http.StatusConflict, nil)

	var run bench.Run
	postJSON(t, ts.URL+"/api/bench/"+itoa(started.ID)+"/cancel", ``, http.StatusOK, &run)
	if run.Status != bench.StatusCancelled || run.Unloaded == nil || !*run.Unloaded {
		t.Fatalf("cancelled: %+v", run)
	}
	if loaded, _ := o.Running(context.Background()); len(loaded) != 0 {
		t.Fatalf("still loaded after cancel: %+v", loaded)
	}
	postJSON(t, ts.URL+"/api/bench/"+itoa(started.ID)+"/cancel", ``, http.StatusNotFound, nil)

	postJSON(t, ts.URL+"/api/bench", `{"model":"mistral:7b"}`, http.StatusNotFound, nil)
	postJSON(t, ts.URL+"/api/bench", `{"model":"llama3.2:1b","colour":"blue"}`, http.StatusBadRequest, nil)
	postJSON(t, ts.URL+"/api/bench", `not json`, http.StatusBadRequest, nil)
	postJSON(t, ts.URL+"/api/bench/history", ``, http.StatusMethodNotAllowed, nil)
	postJSON(t, ts.URL+"/api/bench/plan", ``, http.StatusMethodNotAllowed, nil)
	getJSON(t, ts.URL+"/api/bench/abc", http.StatusBadRequest, nil)
	getJSON(t, ts.URL+"/api/bench/99999", http.StatusNotFound, nil)
	getJSON(t, ts.URL+"/api/bench/plan", http.StatusBadRequest, nil)
}

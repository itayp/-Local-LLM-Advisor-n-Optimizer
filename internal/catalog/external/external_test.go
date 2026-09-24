package external

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/figure"
	"advisor/internal/store"
)

const testCatalogue = `
quants: [Q4_K_M]
families:
  - id: qwen3.5
    display_name: Qwen3.5
    maintainer: Qwen (Alibaba Cloud)
    license: {spdx: Apache-2.0}
    purposes: [chat, reasoning]
    reviewed_at: 2026-09-18
    source: https://huggingface.co/Qwen/Qwen3.5-9B
    sizes:
      - parameters: 4000000000
        context_length: 262144
        ollama_tag: qwen3.5:4b
        hf_repo: bartowski/Qwen_Qwen3.5-4B-GGUF
        hf_base_repo: Qwen/Qwen3.5-4B
      - parameters: 9000000000
        context_length: 262144
        ollama_tag: qwen3.5:9b
        hf_repo: bartowski/Qwen_Qwen3.5-9B-GGUF
        hf_base_repo: Qwen/Qwen3.5-9B
  - id: llama3.2
    display_name: Llama 3.2
    maintainer: Meta
    license: {name: Llama 3.2 Community License Agreement, url: "https://www.llama.com/llama3_2/license/"}
    purposes: [chat, writing]
    reviewed_at: 2026-09-18
    source: https://huggingface.co/meta-llama/Llama-3.2-3B-Instruct
    sizes:
      - parameters: 3210000000
        context_length: 131072
        ollama_tag: llama3.2:3b
        hf_repo: bartowski/Llama-3.2-3B-Instruct-GGUF
        hf_base_repo: meta-llama/Llama-3.2-3B-Instruct
  - id: llama3.1
    display_name: Llama 3.1
    maintainer: Meta
    license: {name: Llama 3.1 Community License Agreement, url: "https://www.llama.com/llama3_1/license/"}
    purposes: [chat, writing]
    reviewed_at: 2026-09-18
    source: https://huggingface.co/meta-llama/Llama-3.1-8B-Instruct
    sizes:
      - parameters: 8030000000
        context_length: 131072
        ollama_tag: llama3.1:8b
        hf_repo: bartowski/Meta-Llama-3.1-8B-Instruct-GGUF
        hf_base_repo: meta-llama/Llama-3.1-8B-Instruct
  - id: glm-4.7-flash
    display_name: GLM-4.7-Flash
    maintainer: Z.ai
    license: {spdx: MIT}
    purposes: [coding, chat, reasoning]
    reviewed_at: 2026-09-18
    source: https://huggingface.co/zai-org/GLM-4.7-Flash
    sizes:
      - parameters: 31000000000
        active_parameters: 3000000000
        context_length: 202752
        ollama_tag: glm-4.7-flash
        hf_repo: bartowski/zai-org_GLM-4.7-Flash-GGUF
        hf_base_repo: zai-org/GLM-4.7-Flash
  - id: nemotron-3-nano
    display_name: Nemotron 3 Nano
    maintainer: NVIDIA
    license: {name: NVIDIA Open Model License, url: "https://www.nvidia.com/en-us/agreements/enterprise-software/nvidia-open-model-license/"}
    purposes: [chat, reasoning]
    reviewed_at: 2026-09-18
    source: https://huggingface.co/nvidia/NVIDIA-Nemotron-3-Nano-30B-A3B-BF16
    sizes:
      - parameters: 30000000000
        active_parameters: 3500000000
        context_length: 1048576
        ollama_tag: nemotron-3-nano:30b
        hf_repo: bartowski/nvidia_Nemotron-3-Nano-30B-A3B-GGUF
        hf_base_repo: nvidia/NVIDIA-Nemotron-3-Nano-30B-A3B-BF16
  - id: gemma4
    display_name: Gemma 4
    maintainer: Google DeepMind
    license: {spdx: Apache-2.0}
    purposes: [chat, vision, writing]
    reviewed_at: 2026-09-18
    source: https://huggingface.co/google/gemma-4-31B-it
    sizes:
      - parameters: 30700000000
        context_length: 262144
        ollama_tag: gemma4:31b
        hf_repo: bartowski/google_gemma-4-31B-it-GGUF
        hf_base_repo: google/gemma-4-31B-it
  - id: llama3.3
    display_name: Llama 3.3
    maintainer: Meta
    license: {name: Llama 3.3 Community License Agreement, url: "https://www.llama.com/llama3_3/license/"}
    purposes: [chat, writing]
    reviewed_at: 2026-09-18
    source: https://huggingface.co/meta-llama/Llama-3.3-70B-Instruct
    sizes:
      - parameters: 70600000000
        context_length: 131072
        ollama_tag: llama3.3:70b
        hf_repo: bartowski/Llama-3.3-70B-Instruct-GGUF
        hf_base_repo: meta-llama/Llama-3.3-70B-Instruct
`

const testAliases = `
arena:
  - {name: llama-3.3-70b-instruct, family: llama3.3, parameters: 70600000000, reviewed_at: 2026-09-24}
  - {name: llama-3.1-8b-instruct, family: llama3.1, parameters: 8030000000, reviewed_at: 2026-09-24}
  - {name: gemma-4-31b, family: gemma4, parameters: 30700000000, reviewed_at: 2026-09-24}
  - {name: glm-4.7-flash, family: glm-4.7-flash, parameters: 31000000000, reviewed_at: 2026-09-24}
  - {name: nvidia-nemotron-3-nano-30b-a3b-bf16, family: nemotron-3-nano, parameters: 30000000000, reviewed_at: 2026-09-24}
epoch:
  - {name: Llama-3.3-70B-Instruct, family: llama3.3, parameters: 70600000000, reviewed_at: 2026-09-24}
  - {name: glm-4.7-flash, family: glm-4.7-flash, parameters: 31000000000, reviewed_at: 2026-09-24}
  - {name: Llama-3.2-3B-Instruct, family: llama3.2, parameters: 3210000000, reviewed_at: 2026-09-24}
`

// hub is a fake of the three sources: the Hub's model API, the Dataset
// Viewer and Epoch's download, served from testdata/.
type hub struct {
	t        *testing.T
	mu       sync.Mutex
	requests []string
	zip      []byte
	extra    func(w http.ResponseWriter, r *http.Request) bool // a test's override; true = handled
	srv      *httptest.Server
}

func newHub(t *testing.T) *hub {
	h := &hub{t: t, zip: buildZip(t, "gpqa_diamond.csv", "aider_polyglot_external.csv")}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests = append(h.requests, r.URL.RequestURI())
	h.mu.Unlock()
	if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
		h.t.Errorf("a request carried credentials: %v", r.Header)
	}
	if !strings.HasPrefix(r.Header.Get("User-Agent"), "test-advisor/") {
		h.t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
	}
	if h.extra != nil && h.extra(w, r) {
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/models/"):
		if got := r.URL.Query()["expand"]; strings.Join(got, ",") != "evalResults,cardData,createdAt,lastModified" {
			h.t.Errorf("expand = %v", got)
		}
		name := strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/api/models/"), "/", "_")
		b, err := os.ReadFile(filepath.Join("testdata", "hf", name+".json"))
		if err != nil {
			http.Error(w, `{"error":"Repository not found"}`, http.StatusNotFound)
			return
		}
		etag := `"` + name + `-v1"`
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		_, _ = w.Write(b)
	case r.URL.Path == "/splits":
		serveFile(w, "arena/splits.json")
	case r.URL.Path == "/filter":
		where := r.URL.Query().Get("where")
		if where == `"category"='overall'` && r.URL.Query().Get("config") == "text" {
			serveFile(w, "arena/text-overall.json")
			return
		}
		_, _ = w.Write([]byte(`{"features":[{"name":"model_name"},{"name":"rating"},{"name":"category"},{"name":"leaderboard_publish_date"}],"rows":[],"num_rows_total":0}`))
	case r.URL.Path == "/data/benchmark_data.zip":
		if r.Header.Get("If-None-Match") == `"zip-v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"zip-v1"`)
		w.Header().Set("Last-Modified", "Tue, 22 Sep 2026 06:00:00 GMT")
		_, _ = w.Write(h.zip)
	default:
		http.NotFound(w, r)
	}
}

func (h *hub) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.requests...)
}

func serveFile(w http.ResponseWriter, name string) {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_, _ = w.Write(b)
}

func buildZip(t *testing.T, files ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join("testdata", "epoch", f))
		if err != nil {
			t.Fatal(err)
		}
		w, _ := zw.Create("benchmark_data/" + f)
		_, _ = w.Write(b)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type fixture struct {
	t     *testing.T
	hub   *hub
	cat   *catalog.Catalogue
	cfg   *Config
	al    Aliases
	st    *store.Store
	ids   map[store.CatalogKey]int64
	clock time.Time
}

// newFixture: the embedded external.yaml pointed at the fake hub, the test
// catalogue synced into a fresh store.
func newFixture(t *testing.T, enabled ...string) *fixture {
	t.Helper()
	f := &fixture{t: t, hub: newHub(t), clock: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)}
	var err error
	if f.cat, err = catalog.Parse([]byte(testCatalogue)); err != nil {
		t.Fatal(err)
	}
	if f.cfg, err = DefaultConfig(); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(f.hub.srv.URL)
	on := map[string]bool{}
	for _, e := range enabled {
		on[e] = true
	}
	for i := range f.cfg.Sources {
		s := &f.cfg.Sources[i]
		s.URL, s.Hosts, s.Enabled = f.hub.srv.URL, []string{u.Host}, len(on) == 0 || on[s.ID]
		if s.ID == SourceEpoch {
			s.URL += "/data/benchmark_data.zip"
		}
	}
	if f.al, err = ParseAliases([]byte(testAliases)); err != nil {
		t.Fatal(err)
	}
	if err := Check(f.cat, f.cfg, f.al); err != nil {
		t.Fatal(err)
	}
	if f.st, err = store.Open(context.Background(), filepath.Join(t.TempDir(), "advisor.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.st.Close() })
	if f.ids, err = f.st.SyncCatalogModels(context.Background(), f.cat); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) run(force bool) Report {
	f.t.Helper()
	u, _ := url.Parse(f.hub.srv.URL)
	fe := NewFetcher("test-advisor/0", []string{u.Host})
	fe.MinInterval = 0
	fe.Sleep = func(context.Context, time.Duration) error { return nil }
	rep, err := Run(context.Background(), Options{
		Catalogue: f.cat, Config: f.cfg, Aliases: f.al, Store: f.st, Fetcher: fe, Trigger: "test", Force: force,
		Now: func() time.Time { return f.clock },
	})
	if err != nil {
		f.t.Fatalf("Run: %v", err)
	}
	return rep
}

func (f *fixture) id(family string, params uint64) int64 {
	return f.ids[store.CatalogKey{FamilyID: family, Parameters: params}]
}

func (f *fixture) values() []catalog.External {
	f.t.Helper()
	v, err := f.st.ExternalValues(context.Background(), false)
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}

func source(rep Report, id string) SourceReport {
	for _, s := range rep.Sources {
		if s.ID == id {
			return s
		}
	}
	return SourceReport{}
}

func TestTheEmbeddedFilesAreValid(t *testing.T) {
	cat, err := catalog.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	al, err := DefaultAliases()
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(cat, cfg, al); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{SourceHFEvals, SourceArena, SourceEpoch} {
		if _, ok := cfg.Source(id); !ok {
			t.Errorf("external.yaml lacks the approved source %s", id)
		}
	}
	for _, h := range []string{"artificialanalysis.ai", "openrouter.ai"} {
		if !cfg.Excluded(h) || !cfg.Excluded("www."+h) {
			t.Errorf("%s must be excluded", h)
		}
	}
}

func TestConfigRefusesWhatTheNoteDoesNotApprove(t *testing.T) {
	base, err := os.ReadFile("../../../data/catalog/external.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"a host the code does not permit", "hosts: [epoch.ai]", "hosts: [epoch.ai, artificialanalysis.ai]", "not one the code permits"},
		{"a source with no client", "- id: epoch\n", "- id: artificial_analysis\n", "client for"},
		{"an Epoch file of other people's results", "\n    file: gpqa_diamond.csv", "\n    file: gpqa_external.csv", "only Epoch's own runs"},
		{"a metric with no plain words", "tests: people's votes comparing answers to coding questions", "tests: \"\"", "plain words"},
		{"an unknown key", "cadence_hours: 168", "cadence_hours: 168\n    scrape: true", "scrape"},
		{"a missing required column", "      category: category\n", "", "columns.category"},
	} {
		src := strings.Replace(string(base), tc.from, tc.to, 1)
		if src == string(base) {
			t.Fatalf("%s: the edit did not apply", tc.name)
		}
		_, err := ParseConfig([]byte(src))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
}

func TestAliasesMustNameARealSize(t *testing.T) {
	f := newFixture(t)
	bad, err := ParseAliases([]byte(`
arena:
  - {name: x, family: llama3.3, parameters: 8000000000, reviewed_at: 2026-09-24}
  - {name: x, family: nope, parameters: 1, reviewed_at: 2026-09-24}
hf_evals:
  - {name: y, family: llama3.3, parameters: 70600000000, reviewed_at: 2026-09-24}
`))
	if err != nil {
		t.Fatal(err)
	}
	p := strings.Join(bad.Validate(f.cat, f.cfg), "\n")
	for _, want := range []string{"no size of 8000000000", "listed twice", `family "nope"`, "hf_evals needs no aliases"} {
		if !strings.Contains(p, want) {
			t.Errorf("problems %q lack %q", p, want)
		}
	}
}

func TestHuggingFaceEvalResults(t *testing.T) {
	f := newFixture(t, SourceHFEvals)
	rep := f.run(false)
	sr := source(rep, SourceHFEvals)
	if sr.Status != StatusRead || sr.Error != "" {
		t.Fatalf("status %s, error %q", sr.Status, sr.Error)
	}
	vals := f.values()
	bySize := map[int64]map[string]catalog.External{}
	for _, v := range vals {
		if bySize[v.ModelID] == nil {
			bySize[v.ModelID] = map[string]catalog.External{}
		}
		bySize[v.ModelID][v.Metric] = v
	}

	// The shaped Qwen3.5 9B answer: the cases real answers have not shown yet.
	q9 := bySize[f.id("qwen3.5", 9_000_000_000)]
	gpqa, ok := q9["hf:Idavidrein/gpqa/diamond"]
	if !ok || gpqa.Value != 81.7 || gpqa.Provenance != "maker" || gpqa.SourceDate != "2026-03-02" ||
		gpqa.Attribution != "Reported by Qwen (Alibaba Cloud) on Hugging Face" || gpqa.License != "HF-ToS; repo:apache-2.0" {
		t.Errorf("the maker's merged result: %+v", gpqa)
	}
	if hle := q9["hf:cais/hle/hle"]; hle.Provenance != "verified" || hle.Attribution != "Verified on Hugging Face" || hle.SourceDate != "2026-04-18" {
		t.Errorf("the verified result: %+v", hle)
	}
	if _, ok := q9["hf:TIGER-Lab/MMLU-Pro/mmlu_pro"]; ok {
		t.Error("a result in an open pull request must be counted, not stored")
	}
	if _, ok := q9["hf:SWE-bench/SWE-bench_Verified/swe_bench_%_resolved"]; ok || len(sr.Dropped) != 1 || !strings.Contains(sr.Dropped[0], "artificialanalysis.ai") {
		t.Errorf("a result sourced from an excluded publisher must be dropped: %v", sr.Dropped)
	}

	// Real answers (captured 2026-09-24): GLM-4.7-Flash's two merged results,
	// its SWE-bench one still in a pull request; Gemma 4 31B's one merged
	// vision result among twelve pending.
	glm := bySize[f.id("glm-4.7-flash", 31_000_000_000)]
	if g := glm["hf:Idavidrein/gpqa/diamond"]; g.Value != 75.2 || g.SourceDate != "2026-01-27" || g.Provenance != "maker" || g.Attribution != "Reported by Z.ai on Hugging Face" || g.License != "HF-ToS; repo:mit" {
		t.Errorf("GLM-4.7-Flash GPQA: %+v", g)
	}
	if h := glm["hf:cais/hle/hle"]; h.Value != 14.4 || h.SourceDate != "2026-01-28" {
		t.Errorf("GLM-4.7-Flash HLE: %+v", h)
	}
	if len(glm) != 2 {
		t.Errorf("GLM-4.7-Flash stored %d values, want 2 (SWE-bench is in a pull request)", len(glm))
	}
	if m := bySize[f.id("gemma4", 30_700_000_000)]["hf:MMMU/MMMU_Pro/mmmu_pro_vision"]; m.Value != 76.9 || m.SourceDate != "2026-05-12" {
		t.Errorf("Gemma 4 31B MMMU-Pro: %+v", m)
	}
	if len(vals) != 5 {
		t.Errorf("stored %d values, want 5", len(vals))
	}
	// Pending: the shaped one, and every real result still in a pull request
	// (Qwen3.5 4B 15, GLM 1, Gemma 31B 12, Llama 3.1 8B 11 — one of them a
	// file the Hub could not read — Llama 3.3 12, Nemotron 1).
	if sr.Pending != 53 {
		t.Errorf("pending = %d, want 53", sr.Pending)
	}
	if len(sr.UnknownMetrics) != 1 || sr.UnknownMetrics[0] != "hf:openai/gsm8k/main" {
		t.Errorf("unknown metrics = %v", sr.UnknownMetrics)
	}
	// The shaped "not a number", and Nemotron's three files the Hub itself
	// could not parse — said in the Hub's words, on one line.
	hubErr := 0
	for _, u := range sr.Unreadable {
		if strings.Contains(u, "Hugging Face could not read the repo's own .eval_results/") && strings.Contains(u, "expected string, received undefined at [0].dataset.task_id") {
			hubErr++
		}
	}
	if len(sr.Unreadable) != 4 || hubErr != 3 {
		t.Errorf("unreadable = %v", sr.Unreadable)
	}
	if len(sr.Failures) != 0 {
		t.Errorf("failures = %v", sr.Failures)
	}
	rows, _ := f.st.CatalogModels(context.Background(), false)
	for _, r := range rows {
		want := map[string]string{"qwen3.5:9b": "2026-03-02", "llama3.2:3b": "2024-09-18", "glm-4.7-flash": "2026-01-19"}[r.Model.Size.OllamaTag]
		if want != "" && r.Model.ReleasedAt != want {
			t.Errorf("%s released_at = %q, want %q", r.Model.Size.OllamaTag, r.Model.ReleasedAt, want)
		}
	}
	if !contains(sr.Hits, "qwen3.5:9b") || !contains(sr.Hits, "glm-4.7-flash") || !contains(sr.Misses, "llama3.2:3b") {
		t.Errorf("hits %v, misses %v", sr.Hits, sr.Misses)
	}

	// Within the cadence, nothing is requested.
	n := len(f.hub.seen())
	if got := source(f.run(false), SourceHFEvals); got.Status != StatusSkipped || len(f.hub.seen()) != n {
		t.Errorf("a second run within the cadence: status %s, %d new requests", got.Status, len(f.hub.seen())-n)
	}
	// Forced, an unchanged repo is a 304 and its values stay.
	f.clock = f.clock.Add(time.Hour)
	got := source(f.run(true), SourceHFEvals)
	if got.Stats.NotModified < 2 || len(f.values()) != 5 {
		t.Errorf("forced re-read: %+v; values %d", got.Stats, len(f.values()))
	}
}

// A repo that could not be read is a failure in words naming the field to
// check — and the source is due again at the next refresh, not after its
// cadence: a gap is not left for a day.
func TestAFailedPartMakesTheSourceDueAgain(t *testing.T) {
	f := newFixture(t, SourceHFEvals)
	missing := true
	f.hub.extra = func(w http.ResponseWriter, r *http.Request) bool {
		if missing && strings.HasSuffix(r.URL.Path, "/Qwen3.5-4B") {
			http.Error(w, `{"error":"Repository not found"}`, http.StatusNotFound)
			return true
		}
		return false
	}
	sr := source(f.run(false), SourceHFEvals)
	if len(sr.Failures) != 1 || !strings.Contains(sr.Failures[0], "hf_base_repo") || !strings.Contains(sr.Failures[0], "qwen3.5:4b") {
		t.Fatalf("failures = %v", sr.Failures)
	}
	missing = false
	f.clock = f.clock.Add(time.Minute)
	if sr = source(f.run(false), SourceHFEvals); sr.Status == StatusSkipped || len(sr.Failures) != 0 {
		t.Errorf("a source read in part must be read again at once: %+v", sr)
	}
	f.clock = f.clock.Add(time.Minute)
	if sr = source(f.run(false), SourceHFEvals); sr.Status != StatusSkipped {
		t.Errorf("once whole, the cadence holds again: %s", sr.Status)
	}
}

func TestArena(t *testing.T) {
	f := newFixture(t, SourceArena)
	var ms []Metric
	for _, m := range f.cfg.Metrics {
		if m.Source != SourceArena || m.Metric == "arena:text/overall" || m.Metric == "arena:text/coding" {
			ms = append(ms, m)
		}
	}
	f.cfg.Metrics = ms
	rep := f.run(false)
	sr := source(rep, SourceArena)
	if sr.Error != "" {
		t.Fatal(sr.Error)
	}
	// Real rows (the text board, 13 September 2026): three aliased sizes.
	vals := f.values()
	if len(vals) != 3 {
		t.Fatalf("stored %d values, want the three aliased rows: %+v", len(vals), vals)
	}
	for _, v := range vals {
		if v.Provenance != "crowd" || v.SourceDate != "2026-09-13" || v.License != "CC-BY-4.0" ||
			!strings.Contains(v.Attribution, "2026-09-13") || v.Detail["board_rows"] != 6.0 {
			t.Errorf("row %+v", v)
		}
		if v.SourceModel == "gemma-4-31b" && (v.Value != 1441.680670089327 || v.ModelID != f.id("gemma4", 30_700_000_000) || v.Detail["votes"] != 5894.0 || v.Detail["rank"] != 64.0) {
			t.Errorf("gemma 4 31b: %+v", v)
		}
	}
	// Open-licence names nothing maps are candidates, with the family they
	// look like when there is one; the proprietary row is not listed.
	want := map[string]string{"qwen3.5-35b-a3b": "qwen3.5", "qwen3.8-27b": ""}
	if len(sr.Candidates) != len(want) {
		t.Errorf("candidates = %+v", sr.Candidates)
	}
	for _, c := range sr.Candidates {
		if s, ok := want[c.Name]; !ok || c.Suggest != s {
			t.Errorf("candidate %+v", c)
		}
	}
	// coding has no rows here: a failure in words; the aliases are then not judged.
	if len(sr.Failures) != 1 || !strings.Contains(sr.Failures[0], `category "coding"`) {
		t.Errorf("failures = %v", sr.Failures)
	}

	// With only the overall board, the aliases the board never lists are reported.
	f.cfg.Metrics = []Metric{mustMetric(t, f.cfg, SourceArena, "arena:text/overall")}
	f.clock = f.clock.Add(25 * time.Hour)
	sr = source(f.run(false), SourceArena)
	if strings.Join(sr.AliasesNotSeen, ",") != "llama-3.1-8b-instruct,llama-3.3-70b-instruct" {
		t.Errorf("aliases not seen = %v", sr.AliasesNotSeen)
	}
	// Same config, same board date: the board is not re-stored.
	f.clock = f.clock.Add(25 * time.Hour)
	if sr = source(f.run(false), SourceArena); sr.Status != StatusUnchanged || sr.Stored != 0 || len(f.values()) != 3 {
		t.Errorf("unchanged board: %+v", sr)
	}
}

// The dataset viewer answers HTTP 500 "the dataset index is loading" while
// it rebuilds (the first coverage report, 2026-09-24): the fetcher waits and
// asks again rather than failing the board.
func TestArenaWaitsOutALoadingIndex(t *testing.T) {
	f := newFixture(t, SourceArena)
	f.cfg.Metrics = []Metric{mustMetric(t, f.cfg, SourceArena, "arena:text/overall")}
	failures := 2
	f.hub.extra = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/filter" && failures > 0 {
			failures--
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"the dataset index is loading, this may take longer than usual"}`))
			return true
		}
		return false
	}
	sr := source(f.run(false), SourceArena)
	if len(sr.Failures) != 0 || len(f.values()) != 3 || sr.Stats.Retries != 2 {
		t.Errorf("a loading index must be waited out: %+v, %d values", sr, len(f.values()))
	}
}

func TestArenaPagesAndRefusesAMissingColumn(t *testing.T) {
	f := newFixture(t, SourceArena)
	f.cfg.Metrics = []Metric{mustMetric(t, f.cfg, SourceArena, "arena:text/overall")}
	var rows []string
	for i := 0; i < 149; i++ {
		rows = append(rows, `{"row":{"model_name":"m`+strconv.Itoa(i)+`","license":"Proprietary","rating":1000,"category":"overall","leaderboard_publish_date":"2026-09-15"}}`)
	}
	rows = append(rows, `{"row":{"model_name":"llama-3.1-8b-instruct","license":"x","rating":1211.4,"category":"overall","leaderboard_publish_date":"2026-09-16"}}`)
	f.hub.extra = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/filter" {
			return false
		}
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		n, _ := strconv.Atoi(r.URL.Query().Get("length"))
		end := min(off+n, len(rows))
		_, _ = w.Write([]byte(`{"features":[{"name":"model_name"},{"name":"license"},{"name":"rating"},{"name":"category"},{"name":"leaderboard_publish_date"}],"rows":[` +
			strings.Join(rows[off:end], ",") + `],"num_rows_total":150}`))
		return true
	}
	sr := source(f.run(false), SourceArena)
	vals := f.values()
	if sr.Error != "" || len(vals) != 1 || vals[0].SourceDate != "2026-09-16" || vals[0].Detail["board_rows"] != 150.0 {
		t.Fatalf("paged read: %+v, %+v", sr, vals)
	}

	f.hub.extra = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/filter" {
			return false
		}
		_, _ = w.Write([]byte(`{"features":[{"name":"model"},{"name":"score"}],"rows":[{"row":{"model":"x"}}],"num_rows_total":1}`))
		return true
	}
	f.clock = f.clock.Add(48 * time.Hour)
	sr = source(f.run(false), SourceArena)
	if len(sr.Failures) != 1 || !strings.Contains(sr.Failures[0], "no column") || len(f.values()) != 1 {
		t.Errorf("a changed shape must fail in words and keep the stored rows: %v, %d values", sr.Failures, len(f.values()))
	}
}

func TestEpochReadsOnlyEpochsOwnRuns(t *testing.T) {
	f := newFixture(t, SourceEpoch)
	sr := source(f.run(false), SourceEpoch)
	if sr.Error != "" {
		t.Fatal(sr.Error)
	}
	// Real rows of gpqa_diamond.csv (captured 2026-09-24), a few of them.
	vals := f.values()
	got := map[string]catalog.External{}
	for _, v := range vals {
		got[v.SourceModel] = v
	}
	l33 := got["Llama-3.3-70B-Instruct"]
	if len(vals) != 2 || l33.Value != 0.4744318181818182 || l33.SourceDate != "2025-01-27" || l33.Provenance != "independent" ||
		!strings.Contains(l33.Attribution, "accessed 24 September 2026") || l33.Metric != "epoch:gpqa_diamond" {
		t.Errorf("Epoch's own file only: %+v (all %+v)", l33, vals)
	}
	// glm-4.7-flash is aliased; its "_none" run (thinking off) is not.
	if g := got["glm-4.7-flash"]; g.Value != 0.6054292929292929 || g.ModelID != f.id("glm-4.7-flash", 31_000_000_000) {
		t.Errorf("glm-4.7-flash: %+v", g)
	}
	// math_level_5.csv, otis_mock_aime_2024_2025.csv and swe_bench_verified.csv are not in the ZIP.
	if len(sr.Failures) != 3 || !strings.Contains(sr.Failures[0], "the ZIP has no") {
		t.Errorf("failures = %v", sr.Failures)
	}
	// A name that only looks like a catalogue family is a candidate with a
	// suggestion for the curator — never a match.
	cands := map[string]string{}
	for _, c := range sr.Candidates {
		cands[c.Name] = c.Suggest
	}
	if cands["glm-4.7-flash_none"] != "glm-4.7-flash" {
		t.Errorf("candidates = %+v", sr.Candidates)
	}
	// A forced re-read of an unchanged ZIP is a 304.
	f.clock = f.clock.Add(time.Hour)
	if sr = source(f.run(true), SourceEpoch); sr.Status != StatusUnchanged || len(f.values()) != 2 {
		t.Errorf("unchanged ZIP: %+v", sr)
	}
	// A ZIP that no longer marks other people's results is refused whole.
	f.hub.zip = buildZip(t, "gpqa_diamond.csv")
	f.hub.extra = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/data/benchmark_data.zip" {
			_, _ = w.Write(f.hub.zip)
			return true
		}
		return false
	}
	f.clock = f.clock.Add(8 * 24 * time.Hour)
	sr = source(f.run(false), SourceEpoch)
	if sr.Status != StatusFailed || !strings.Contains(sr.Error, "no longer marks") || len(f.values()) != 2 {
		t.Errorf("refusal: %+v, values %d", sr, len(f.values()))
	}
}

// Several runs of one model in one file: the latest stands. (Shaped: the
// real file has one run per name today.)
func TestEpochKeepsTheLatestRun(t *testing.T) {
	f := newFixture(t, SourceEpoch)
	csv := "Model version,Best score (across scorers),Started at\n" +
		"Llama-3.3-70B-Instruct,0.482,2025-01-10T10:00:00Z\n" +
		"Llama-3.3-70B-Instruct,0.497,2025-06-02T08:30:00Z\n"
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"gpqa_diamond.csv": csv, "x_external.csv": "Model version\nx\n"} {
		w, _ := zw.Create("benchmark_data/" + name)
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.hub.zip = buf.Bytes()
	f.run(false)
	vals := f.values()
	if len(vals) != 1 || vals[0].Value != 0.497 || vals[0].SourceDate != "2025-06-02" {
		t.Errorf("the later of two runs: %+v", vals)
	}
}

func TestUnreachableKeepsWhatWasStored(t *testing.T) {
	f := newFixture(t)
	f.run(false)
	before := len(f.values())
	if before == 0 {
		t.Fatal("nothing stored")
	}
	f.hub.srv.Close()
	f.clock = f.clock.Add(8 * 24 * time.Hour)
	rep := f.run(false)
	for _, sr := range rep.Sources {
		if sr.Status != StatusFailed || !strings.Contains(sr.Error, "could not be reached") {
			t.Errorf("%s: %+v", sr.ID, sr)
		}
	}
	if len(f.values()) != before {
		t.Errorf("values %d → %d: a failed read must keep the last good rows", before, len(f.values()))
	}
	states, _ := f.st.ExternalStates(context.Background())
	v := NewView(f.cfg, f.values(), f.present(), states)
	if u := v.Updated(); !strings.Contains(u, "last updated 24 September 2026") || !strings.Contains(u, "The latest check of Epoch AI Benchmarking Hub failed: Epoch AI could not be reached") {
		t.Errorf("Updated() = %q", u)
	}
}

// The requests are a function of the three data files alone: nothing about
// the machine, the installed models or the purposes is in them (criterion 4).
func TestRequestsAreTheSameOnEveryInstall(t *testing.T) {
	var runs [][]string
	for i := 0; i < 2; i++ {
		f := newFixture(t)
		if i == 1 {
			// A different machine: a model installed, a different data folder.
			if err := f.st.UpsertInstalledModels(context.Background(), "ollama", nil); err != nil {
				t.Fatal(err)
			}
		}
		f.run(false)
		runs = append(runs, f.hub.seen())
	}
	sort.Strings(runs[0])
	sort.Strings(runs[1])
	if strings.Join(runs[0], "\n") != strings.Join(runs[1], "\n") {
		t.Errorf("requests differ between installs:\n%v\n%v", runs[0], runs[1])
	}
}

func (f *fixture) present() map[int64]bool {
	out := map[int64]bool{}
	for _, id := range f.ids {
		out[id] = true
	}
	return out
}

func TestTheViewNeverCrossesSizesAndAlwaysSaysWhose(t *testing.T) {
	f := newFixture(t)
	f.run(false)
	states, _ := f.st.ExternalStates(context.Background())
	v := NewView(f.cfg, f.values(), f.present(), states)

	// Qwen3.5 9B has public values; its 4B sibling has none, and gets none.
	if e := v.Entries(f.id("qwen3.5", 4_000_000_000)); len(e) != 0 {
		t.Errorf("the 4B borrowed its sibling's scores: %+v", e)
	}
	for _, id := range f.ids {
		for _, e := range v.Entries(id) {
			b, err := json.Marshal(e)
			if err != nil {
				t.Fatalf("an entry could not be served: %v", err)
			}
			if e.Value.Origin.Attribution == "" || e.Value.Origin.Date == "" || e.Position == "" || e.Tests == "" || e.ProvenanceWords == "" {
				t.Errorf("an entry without its words: %s", b)
			}
		}
	}
	for _, e := range v.Entries(f.id("qwen3.5", 9_000_000_000)) {
		want := map[string]string{"maker": "Qwen (Alibaba Cloud) on Hugging Face", "verified": "Hugging Face"}[string(e.Value.Origin.Provenance)]
		if e.Value.Origin.Publisher != want || e.Scored != (e.Value.Origin.Provenance == figure.ProvenanceVerified) {
			t.Errorf("hf entry %s: publisher %q scored %v", e.Metric, e.Value.Origin.Publisher, e.Scored)
		}
	}
	// Gemma 4 31B: Arena's text board, where it leads the three rated sizes
	// here, and its maker's one merged result, shown but never scored.
	g31 := v.Entries(f.id("gemma4", 30_700_000_000))
	if len(g31) != 2 {
		t.Fatalf("gemma4:31b entries = %+v", g31)
	}
	for _, e := range g31 {
		switch e.SourceID {
		case SourceArena:
			if e.Position != "1st for everyday chat of the 3 models here that Arena has rated." || e.ProvenanceWords != "rated by people comparing answers on Arena" || !e.Scored {
				t.Errorf("arena entry: %+v", e)
			}
		case SourceHFEvals:
			if e.Scored || e.Value.Origin.Publisher != "Google DeepMind on Hugging Face" {
				t.Errorf("maker entry: %+v", e)
			}
		}
	}
	// Llama 3.3 70B: Epoch alone, second of the two sizes Epoch tested here.
	l33 := v.Entries(f.id("llama3.3", 70_600_000_000))
	if len(l33) != 1 || l33[0].Position != "2nd for reasoning of the 2 models here that Epoch AI has tested." || l33[0].Value.Origin.Publisher != "Epoch AI" {
		t.Errorf("llama3.3 entries = %+v", l33)
	}
	// The maker's own numbers are shown, never scored.
	scoring := v.Scoring()
	for _, pms := range scoring {
		for _, pm := range pms {
			if pm.Source == SourceHFEvals && pm.Metric == "hf:Idavidrein/gpqa/diamond" {
				t.Errorf("a maker's self-report reached the engine: %+v", pm)
			}
		}
	}
	chat := scoring[catalog.PurposeChat]
	if len(chat) != 1 || len(chat[0].Values) != 3 {
		t.Errorf("chat scoring = %+v", chat)
	}
	// Epoch's GPQA is a percent metric published as fractions: compared as percent.
	for _, pm := range scoring[catalog.PurposeReasoning] {
		if pm.Metric == "epoch:gpqa_diamond" && pm.Values[f.id("llama3.3", 70_600_000_000)] != 47.44318181818182 {
			t.Errorf("epoch values = %+v", pm.Values)
		}
	}
	if line := v.Line(f.id("llama3.3", 70_600_000_000), []catalog.Purpose{catalog.PurposeCoding, catalog.PurposeReasoning}); line == nil || line.SourceID != SourceEpoch {
		t.Errorf("line for coding+reasoning = %+v", line)
	}
	if line := v.Line(f.id("llama3.2", 3_210_000_000), []catalog.Purpose{catalog.PurposeCoding}); line != nil {
		t.Errorf("no public value speaks to coding for this size, so no line: %+v", line)
	}
	// A disabled source's values are neither shown nor scored.
	for i := range f.cfg.Sources {
		f.cfg.Sources[i].Enabled = f.cfg.Sources[i].ID != SourceArena
	}
	v = NewView(f.cfg, f.values(), f.present(), states)
	for _, e := range v.Entries(f.id("gemma4", 30_700_000_000)) {
		if e.SourceID == SourceArena {
			t.Error("a switched-off source is still shown")
		}
	}
}

func TestPositionWords(t *testing.T) {
	for _, tc := range []struct {
		rank, n int
		want    string
	}{
		{1, 9, "Among the strongest for coding of the 9 models here that Arena has rated."},
		{4, 9, "In the middle for coding of the 9 models here that Arena has rated."},
		{7, 9, "Among the weaker for coding of the 9 models here that Arena has rated."},
		{3, 5, "3rd for coding of the 5 models here that Arena has rated."},
		{1, 1, "No other model here has been rated by Arena yet, so it cannot be ranked for coding."},
	} {
		if got := position(SourceArena, catalog.PurposeCoding, tc.rank, tc.n); got != tc.want {
			t.Errorf("position(%d of %d) = %q, want %q", tc.rank, tc.n, got, tc.want)
		}
	}
}

func TestFetcherStaysOnItsHosts(t *testing.T) {
	var elsewhere *httptest.Server
	elsewhere = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer elsewhere.Close()
	calls := 0
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, elsewhere.URL+"/x", http.StatusFound)
		case "/busy":
			if calls == 1 {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer home.Close()
	u, _ := url.Parse(home.URL)
	fe := NewFetcher("test-advisor/0", []string{u.Host})
	var slept []time.Duration
	fe.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	fe.MinInterval = 0
	if _, err := fe.Get(context.Background(), elsewhere.URL+"/x", Validators{}); !errors.Is(err, ErrHost) {
		t.Errorf("a host not on the list: %v", err)
	}
	if _, err := fe.Get(context.Background(), home.URL+"/redirect", Validators{}); !errors.Is(err, ErrHost) {
		t.Errorf("a redirect off the list: %v", err)
	}
	calls = 0
	resp, err := fe.Get(context.Background(), home.URL+"/busy", Validators{})
	if err != nil || string(resp.Body) != "ok" || len(slept) != 1 || slept[0] != 7*time.Second {
		t.Errorf("Retry-After: %v %v slept %v", resp, err, slept)
	}
}

func mustMetric(t *testing.T, cfg *Config, source, metric string) Metric {
	t.Helper()
	m, ok := cfg.Metric(source, metric)
	if !ok {
		t.Fatalf("no metric %s", metric)
	}
	return m
}

// Every public value in the view marshals with its origin (figure.Public
// refuses otherwise) and is a provenance the schema knows.
func TestProvenancesAreTheFour(t *testing.T) {
	f := newFixture(t)
	f.run(false)
	for _, v := range f.values() {
		if !figure.Provenance(v.Provenance).Valid() {
			t.Errorf("stored provenance %q", v.Provenance)
		}
	}
}

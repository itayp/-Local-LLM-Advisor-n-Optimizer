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
  - {name: llama-3.2-3b-instruct, family: llama3.2, parameters: 3210000000, reviewed_at: 2026-09-24}
  - {name: qwen3.5-9b, family: qwen3.5, parameters: 9000000000, reviewed_at: 2026-09-24}
epoch:
  - {name: Llama-3.3-70B-Instruct, family: llama3.3, parameters: 70600000000, reviewed_at: 2026-09-24}
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
	byMetric := map[string]catalog.External{}
	for _, v := range vals {
		if v.ModelID != f.id("qwen3.5", 9_000_000_000) {
			t.Errorf("a value landed on another size: %+v", v)
		}
		byMetric[v.Metric] = v
	}
	gpqa, ok := byMetric["hf:Idavidrein/gpqa/gpqa_diamond"]
	if !ok || gpqa.Value != 81.7 || gpqa.Provenance != "maker" || gpqa.SourceDate != "2026-03-02" ||
		gpqa.Attribution != "Reported by Qwen (Alibaba Cloud) on Hugging Face" || gpqa.License != "HF-ToS; repo:apache-2.0" {
		t.Errorf("the maker's merged result: %+v", gpqa)
	}
	if hle := byMetric["hf:cais/hle/default"]; hle.Provenance != "verified" || hle.Attribution != "Verified on Hugging Face" || hle.SourceDate != "2026-04-18" {
		t.Errorf("the verified result: %+v", hle)
	}
	if _, ok := byMetric["hf:TIGER-Lab/MMLU-Pro/mmlu_pro"]; ok || sr.Pending != 1 {
		t.Errorf("a result in an open pull request must be counted (%d), not stored", sr.Pending)
	}
	if _, ok := byMetric["hf:SWE-bench/SWE-bench_Verified/default"]; ok || len(sr.Dropped) != 1 || !strings.Contains(sr.Dropped[0], "artificialanalysis.ai") {
		t.Errorf("a result sourced from an excluded publisher must be dropped: %v", sr.Dropped)
	}
	if len(sr.UnknownMetrics) != 1 || sr.UnknownMetrics[0] != "hf:openai/gsm8k/main" {
		t.Errorf("unknown metrics = %v", sr.UnknownMetrics)
	}
	if len(sr.Unreadable) != 1 {
		t.Errorf("unreadable = %v", sr.Unreadable)
	}
	if len(vals) != 2 {
		t.Errorf("stored %d values, want 2", len(vals))
	}
	// Qwen3.5 4B's repo (and the fake hub's other missing ones) do not
	// exist: a failure in words per size, naming the field to check.
	if len(sr.Failures) != 3 || !strings.Contains(sr.Failures[0], "hf_base_repo") || !strings.Contains(sr.Failures[0], "qwen3.5:4b") {
		t.Errorf("failures = %v", sr.Failures)
	}
	rows, _ := f.st.CatalogModels(context.Background(), false)
	for _, r := range rows {
		want := map[string]string{"qwen3.5:9b": "2026-03-02", "llama3.2:3b": "2024-09-18"}[r.Model.Size.OllamaTag]
		if r.Model.ReleasedAt != want {
			t.Errorf("%s released_at = %q, want %q", r.Model.Size.OllamaTag, r.Model.ReleasedAt, want)
		}
	}
	if !contains(sr.Hits, "qwen3.5:9b") || !contains(sr.Misses, "llama3.2:3b") {
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
	if got.Stats.NotModified < 2 || len(f.values()) != 2 {
		t.Errorf("forced re-read: %+v; values %d", got.Stats, len(f.values()))
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
	vals := f.values()
	if len(vals) != 3 {
		t.Fatalf("stored %d values, want the three aliased rows: %+v", len(vals), vals)
	}
	for _, v := range vals {
		if v.Provenance != "crowd" || v.SourceDate != "2026-09-15" || v.License != "CC-BY-4.0" ||
			!strings.Contains(v.Attribution, "2026-09-15") || v.Detail["board_rows"] != 5.0 {
			t.Errorf("row %+v", v)
		}
		if v.SourceModel == "llama-3.3-70b-instruct" && (v.Value != 1318.6 || v.ModelID != f.id("llama3.3", 70_600_000_000) || v.Detail["votes"] != 51876.0) {
			t.Errorf("llama 3.3: %+v", v)
		}
	}
	// qwen3.8-27b is open-licence and unmapped: a candidate, with its family
	// suggested; frontier-hosted-1 is proprietary: not listed.
	if len(sr.Candidates) != 1 || sr.Candidates[0].Name != "qwen3.8-27b" {
		t.Errorf("candidates = %+v", sr.Candidates)
	}
	// coding has no rows: a failure in words; the aliases are then not judged.
	if len(sr.Failures) != 1 || !strings.Contains(sr.Failures[0], `category "coding"`) {
		t.Errorf("failures = %v", sr.Failures)
	}

	// With only the overall board, the alias the board never lists is reported.
	f.cfg.Metrics = []Metric{mustMetric(t, f.cfg, SourceArena, "arena:text/overall")}
	f.clock = f.clock.Add(25 * time.Hour)
	sr = source(f.run(false), SourceArena)
	if len(sr.AliasesNotSeen) != 1 || sr.AliasesNotSeen[0] != "qwen3.5-9b" {
		t.Errorf("aliases not seen = %v", sr.AliasesNotSeen)
	}
	// Same config, same board date: the board is not re-stored.
	f.clock = f.clock.Add(25 * time.Hour)
	if sr = source(f.run(false), SourceArena); sr.Status != StatusUnchanged || sr.Stored != 0 || len(f.values()) != 3 {
		t.Errorf("unchanged board: %+v", sr)
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
	vals := f.values()
	got := map[string]catalog.External{}
	for _, v := range vals {
		got[v.SourceModel] = v
	}
	l33 := got["Llama-3.3-70B-Instruct"]
	if len(vals) != 2 || l33.Value != 0.497 || l33.SourceDate != "2025-06-02" || l33.Provenance != "independent" ||
		!strings.Contains(l33.Attribution, "accessed 24 September 2026") || l33.Metric != "epoch:gpqa_diamond" {
		t.Errorf("the later of two runs, from Epoch's own file only: %+v (all %+v)", l33, vals)
	}
	// math_level_5.csv and swe_bench_verified.csv are not in the ZIP.
	if len(sr.Failures) != 2 || !strings.Contains(sr.Failures[0], "the ZIP has no") {
		t.Errorf("failures = %v", sr.Failures)
	}
	if len(sr.Candidates) != 1 || sr.Candidates[0].Name != "Qwen3.5-27B" || sr.Candidates[0].Suggest != "qwen3.5" {
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
	l33 := v.Entries(f.id("llama3.3", 70_600_000_000))
	if len(l33) != 2 {
		t.Fatalf("llama3.3 entries = %+v", l33)
	}
	for _, e := range l33 {
		switch e.SourceID {
		case SourceArena:
			if e.Position != "1st for everyday chat of the 3 models here that Arena has rated." || e.ProvenanceWords != "rated by people comparing answers on Arena" || !e.Scored {
				t.Errorf("arena entry: %+v", e)
			}
		case SourceEpoch:
			if e.Position != "1st for reasoning of the 2 models here that Epoch AI has tested." || e.Value.Origin.Publisher != "Epoch AI" {
				t.Errorf("epoch entry: %+v", e)
			}
		}
	}
	// The maker's own numbers are shown, never scored.
	scoring := v.Scoring()
	for _, pms := range scoring {
		for _, pm := range pms {
			if pm.Source == SourceHFEvals && pm.Metric == "hf:Idavidrein/gpqa/gpqa_diamond" {
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
		if pm.Metric == "epoch:gpqa_diamond" && pm.Values[f.id("llama3.3", 70_600_000_000)] != 49.7 {
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
	for _, e := range v.Entries(f.id("llama3.3", 70_600_000_000)) {
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

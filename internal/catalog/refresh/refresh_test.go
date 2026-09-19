package refresh

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"advisor/internal/backend"
	"advisor/internal/catalog"
	"advisor/internal/catalog/hf"
	"advisor/internal/store"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "gguf", "testdata", name+".gguf.head.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// withFileType is a real header with general.file_type rewritten, so one
// captured header can stand in for several quants of the same model.
func withFileType(t *testing.T, b []byte, ft uint32) []byte {
	t.Helper()
	key := []byte("general.file_type")
	i := bytes.Index(b, key)
	if i < 0 {
		t.Fatal("no general.file_type in fixture")
	}
	out := slices.Clone(b)
	binary.LittleEndian.PutUint32(out[i+len(key)+4:], ft) // key, then the uint32 type, then the value
	return out
}

// hub is a fake Hugging Face: listings with ETags, resolve URLs redirecting
// to byte-range content. Only what is in repos exists.
type hub struct {
	srv   *httptest.Server
	repos map[string]map[string][]byte // repo → path → content
	total map[string]uint64            // repo → gguf.total
	gated map[string]bool

	mu       sync.Mutex
	content  []string // content paths requested (after the redirect)
	listings int
}

func newHub(t *testing.T) *hub {
	h := &hub{repos: map[string]map[string][]byte{}, total: map[string]uint64{}, gated: map[string]bool{}}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hub) etag(repo string) string {
	var names []string
	for p, b := range h.repos[repo] {
		names = append(names, fmt.Sprintf("%s:%d", p, len(b)))
	}
	sort.Strings(names)
	return fmt.Sprintf(`"%x"`, strings.Join(names, ","))
}

func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/models/"):
		repo := strings.TrimPrefix(r.URL.Path, "/api/models/")
		h.mu.Lock()
		h.listings++
		h.mu.Unlock()
		if h.gated[repo] {
			http.Error(w, "gated", http.StatusUnauthorized)
			return
		}
		files, ok := h.repos[repo]
		if !ok {
			http.Error(w, `{"error":"Repository not found"}`, http.StatusNotFound)
			return
		}
		et := h.etag(repo)
		w.Header().Set("ETag", et)
		if r.Header.Get("If-None-Match") == et {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		sib := []map[string]any{{"rfilename": "README.md", "size": 100}}
		for p, b := range files {
			sib = append(sib, map[string]any{"rfilename": p, "size": len(b),
				"lfs": map[string]any{"sha256": fmt.Sprintf("%x", len(b)) + "-" + p, "size": len(b)}})
		}
		info := map[string]any{"id": repo, "sha": "c0ffee", "gated": false, "siblings": sib}
		if tot := h.total[repo]; tot > 0 {
			info["gguf"] = map[string]any{"total": tot}
		}
		_ = json.NewEncoder(w).Encode(info)
	case strings.Contains(r.URL.Path, "/resolve/c0ffee/"):
		http.Redirect(w, r, strings.Replace(r.URL.Path, "/resolve/c0ffee/", "/cdn/", 1), http.StatusFound)
	case strings.Contains(r.URL.Path, "/cdn/"):
		repo, p, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/cdn/")
		b, ok := h.repos[repo][p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Range") == "" {
			http.Error(w, "the test hub only serves ranges", http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		h.content = append(h.content, p)
		h.mu.Unlock()
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
	default:
		http.NotFound(w, r)
	}
}

func (h *hub) client() *hf.Client {
	c := hf.New("advisor-test")
	c.BaseURL = h.srv.URL
	c.MinInterval = 0
	c.Log = quiet()
	c.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	return c
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

const testCatalogue = `
quants: [Q4_K_M, Q6_K, Q8_0]
families:
  - id: llama3.2
    display_name: Llama 3.2
    maintainer: Meta
    license: {name: Llama 3.2 Community License Agreement, url: "https://www.llama.com/llama3_2/license/"}
    purposes: [chat, writing]
    reviewed_at: 2026-09-18
    source: https://huggingface.co/meta-llama/Llama-3.2-1B-Instruct
    sizes:
      - parameters: 1230000000
        context_length: 131072
        ollama_tag: llama3.2:1b
        hf_repo: bartowski/Llama-3.2-1B-Instruct-GGUF
  - id: vision
    display_name: A Vision Model
    maintainer: Someone
    license: {spdx: Apache-2.0}
    purposes: [vision]
    reviewed_at: 2026-09-18
    source: https://example.com/card
    sizes:
      - parameters: 752000000
        context_length: 131072
        ollama_tag: vision:1b
        hf_repo: o/vision-GGUF
  - id: broken
    display_name: Broken
    maintainer: Someone
    license: {spdx: MIT}
    purposes: [chat]
    reviewed_at: 2026-09-18
    source: https://example.com/card
    sizes:
      - parameters: 1000000000
        context_length: 4096
        ollama_tag: broken:1b
        hf_repo: o/does-not-exist
      - parameters: 2000000000
        context_length: 4096
        ollama_tag: broken:2b
        hf_repo: o/gated
      - parameters: 3000000000
        context_length: 4096
        ollama_tag: broken:3b
        hf_repo: o/malformed
      - parameters: 4000000000
        context_length: 4096
        ollama_tag: broken:4b
        hf_repo: o/no-tracked-quant
`

func setup(t *testing.T) (*hub, *store.Store, *catalog.Catalogue) {
	t.Helper()
	h := newHub(t)
	llama := fixture(t, "llama3.2-1b") // Q8_0
	h.repos["bartowski/Llama-3.2-1B-Instruct-GGUF"] = map[string][]byte{
		"Llama-3.2-1B-Instruct-Q8_0.gguf":   llama,
		"Llama-3.2-1B-Instruct-Q4_K_M.gguf": withFileType(t, llama, 15),
		// Split in two, in a folder, as bartowski publishes large quants.
		"Llama-3.2-1B-Instruct-Q6_K/Llama-3.2-1B-Instruct-Q6_K-00001-of-00002.gguf": withFileType(t, llama, 18),
		"Llama-3.2-1B-Instruct-Q6_K/Llama-3.2-1B-Instruct-Q6_K-00002-of-00002.gguf": make([]byte, 5000),
		"Llama-3.2-1B-Instruct-IQ2_M.gguf":                                          withFileType(t, llama, 29), // not tracked
		"Llama-3.2-1B-Instruct-imatrix.gguf":                                        []byte("not a model"),
	}
	h.total["bartowski/Llama-3.2-1B-Instruct-GGUF"] = 1235814432
	vision := fixture(t, "minicpm-v4.6")
	proj := fixture(t, "minicpm-v4.6-projector")
	h.repos["o/vision-GGUF"] = map[string][]byte{
		"model-Q4_K_M.gguf":      vision,
		"mmproj-model-bf16.gguf": proj,
		"mmproj-model-f16.gguf":  proj,
	}
	h.repos["o/malformed"] = map[string][]byte{"m-Q4_K_M.gguf": []byte("GGML this is not a GGUF header at all")}
	h.repos["o/no-tracked-quant"] = map[string][]byte{"m-IQ1_S.gguf": llama}
	h.repos["o/gated"] = map[string][]byte{}
	h.gated["o/gated"] = true

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "advisor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cat, err := catalog.Parse([]byte(testCatalogue))
	if err != nil {
		t.Fatal(err)
	}
	return h, st, cat
}

func TestRefreshResolvesEverySizeWithoutDownloadingWeights(t *testing.T) {
	h, st, cat := setup(t)
	ctx := context.Background()
	client := h.client()
	rep, err := Run(ctx, Options{Catalogue: cat, Store: st, HF: client, Log: quiet(), Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sizes != 6 || rep.Resolved != 2 || len(rep.Failures) != 4 {
		t.Fatalf("report: sizes=%d resolved=%d failures=%+v", rep.Sizes, rep.Resolved, rep.Failures)
	}
	wantFail := map[string]string{
		"broken:1b": "no repo or file by that name",
		"broken:2b": "needs a Hugging Face login",
		"broken:3b": "not valid GGUF",
		"broken:4b": "no GGUF file with a tracked quant",
	}
	for _, f := range rep.Failures {
		if !strings.Contains(f.Error, wantFail[f.OllamaTag]) {
			t.Errorf("%s failed with %q, want it to say %q", f.OllamaTag, f.Error, wantFail[f.OllamaTag])
		}
	}

	// Only the tracked quants' first parts and one projector were read —
	// never an untracked quant, a second part, the imatrix, or the bf16
	// projector — and every read stopped at the tokenizer, including the one
	// whose file_type comes after it (D-37).
	got := slices.Clone(h.content)
	slices.Sort(got)
	got = slices.Compact(got)
	want := []string{
		"Llama-3.2-1B-Instruct-Q4_K_M.gguf",
		"Llama-3.2-1B-Instruct-Q6_K/Llama-3.2-1B-Instruct-Q6_K-00001-of-00002.gguf",
		"Llama-3.2-1B-Instruct-Q8_0.gguf",
		"m-Q4_K_M.gguf",
		"mmproj-model-f16.gguf",
		"model-Q4_K_M.gguf",
	}
	if !slices.Equal(got, want) {
		t.Errorf("files read:\n got  %v\n want %v", got, want)
	}
	// Five headers parsed; the sixth read found a malformed file.
	if rep.HeaderReads != 5 || rep.CacheHits != 0 {
		t.Errorf("header reads %d, cache hits %d", rep.HeaderReads, rep.CacheHits)
	}
	// Every header read stops at the tokenizer: one 64 KiB range each (the
	// projector is smaller), against 6–11 MB each for a full header.
	if rep.BytesRead > 6*64<<10+64<<10 {
		t.Errorf("read %d bytes in all; want one 64 KiB range per file", rep.BytesRead)
	}

	rows, err := st.CatalogModels(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	byTag := map[string]store.CatalogModelRow{}
	for _, r := range rows {
		byTag[r.Model.Size.OllamaTag] = r
	}
	llama := byTag["llama3.2:1b"].Model
	if llama.RefreshedAt == "" || llama.RefreshError != "" || llama.HFSHA != "c0ffee" || llama.ParametersCounted != 1235814432 {
		t.Errorf("llama model row: %+v", llama)
	}
	quants := map[string]catalog.File{}
	for _, f := range llama.Files {
		quants[f.Quant] = f
	}
	if len(quants) != 3 {
		t.Fatalf("llama files: %+v", llama.Files)
	}
	q6 := quants["Q6_K"]
	llamaBytes := uint64(len(fixture(t, "llama3.2-1b")))
	if q6.Parts != 2 || q6.Bytes != llamaBytes+5000 || q6.Header.FileTypeName != "Q6_K" || q6.Role != catalog.RoleModel {
		t.Errorf("Q6_K (split): %+v", q6)
	}
	q8 := quants["Q8_0"]
	h8 := q8.Header
	if h8.Architecture != "llama" || h8.BlockCount != 16 || h8.HeadCount != 32 || h8.HeadCountKV != 8 ||
		h8.KeyLength != 64 || h8.EmbeddingLength != 2048 || h8.ContextLength != 131072 || h8.FileType != 7 ||
		h8.HasVision || h8.Complete || !h8.HeadCountKVStated || h8.GGUFVersion != 3 || h8.TensorCount != 147 {
		t.Errorf("Q8_0 header: %+v", h8)
	}
	if wantBPW := float64(llamaBytes) * 8 / 1235814432; math.Abs(q8.BitsPerWeight-wantBPW) > 1e-9 {
		t.Errorf("bits per weight %v, want %v (bytes × 8 / Hugging Face's parameter count)", q8.BitsPerWeight, wantBPW)
	}

	vis := byTag["vision:1b"].Model
	if len(vis.Files) != 2 {
		t.Fatalf("vision files: %+v", vis.Files)
	}
	for _, f := range vis.Files {
		switch f.Role {
		case catalog.RoleModel:
			// Its writer put general.file_type after the tokenizer, so the
			// read stopped without it: not stated (-1), never guessed.
			if f.Header.Architecture != "qwen35" || f.Header.FullAttentionInterval != 4 || !f.Header.HasVision ||
				f.Header.Complete || f.Header.KeyLength != 256 || f.Header.FileType != -1 || f.Header.FileTypeName != "" {
				t.Errorf("vision weights: %+v", f.Header)
			}
			if f.BitsPerWeight == 0 {
				t.Error("no bits per weight: YAML parameters should stand in when the Hub gives no count")
			}
		case catalog.RoleProjector:
			if f.Quant != "F16" || f.Header.Architecture != "clip" || f.BitsPerWeight != 0 {
				t.Errorf("projector: %+v", f)
			}
		}
	}
	// families.yaml says 131072; the header says 262144. A warning, not a failure.
	if !slices.ContainsFunc(rep.Warnings, func(w string) bool {
		return strings.Contains(w, "vision:1b") && strings.Contains(w, "context_length 131072") && strings.Contains(w, "262144")
	}) {
		t.Errorf("no context-length warning in %v", rep.Warnings)
	}
	broken := byTag["broken:1b"].Model
	if broken.RefreshError == "" || broken.RefreshedAt != "" {
		t.Errorf("failed size row: %+v", broken)
	}

	// The header of a stored file carries every kept pair, and no tokenizer.
	hj, err := st.CatalogFileHeaderJSON(ctx, q8.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(hj, []byte(`"llama.rope.freq_base"`)) || bytes.Contains(hj, []byte(`"tokenizer.ggml.tokens":`)) ||
		!bytes.Contains(hj, []byte(`"stopped_at":"tokenizer.ggml.model"`)) {
		t.Errorf("header_json = %.400s", hj)
	}

	latest, err := st.LatestCatalogRefresh(ctx)
	if err != nil || latest.Trigger != "cli" || latest.Sizes != 6 || latest.Resolved != 2 {
		t.Errorf("stored refresh: %+v, %v", latest, err)
	}
}

func TestSecondRefreshUsesTheCaches(t *testing.T) {
	h, st, cat := setup(t)
	ctx := context.Background()
	client := h.client()
	first, err := Run(ctx, Options{Catalogue: cat, Store: st, HF: client, Log: quiet(), Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	h.content = nil
	second, err := Run(ctx, Options{Catalogue: cat, Store: st, HF: client, Log: quiet(), Trigger: "api"})
	if err != nil {
		t.Fatal(err)
	}
	// Only the malformed file is read again: a failure is never cached.
	if !slices.Equal(h.content, []string{"m-Q4_K_M.gguf"}) || second.HeaderReads != 0 || second.CacheHits != first.HeaderReads {
		t.Errorf("second refresh read %v; header reads %d, cache hits %d (first run read %d)",
			h.content, second.HeaderReads, second.CacheHits, first.HeaderReads)
	}
	if second.NotModified != 4 { // every repo that answered the first time is a 304
		t.Errorf("not-modified listings: %d", second.NotModified)
	}
	if second.Resolved != first.Resolved {
		t.Errorf("resolved %d then %d", first.Resolved, second.Resolved)
	}

	// A quant that leaves the repo, and a size that leaves the catalogue,
	// are marked absent — never deleted.
	delete(h.repos["bartowski/Llama-3.2-1B-Instruct-GGUF"], "Llama-3.2-1B-Instruct-Q4_K_M.gguf")
	smaller, err := catalog.Parse([]byte(strings.Split(testCatalogue, "  - id: broken")[0]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, Options{Catalogue: smaller, Store: st, HF: client, Log: quiet(), Trigger: "cli"}); err != nil {
		t.Fatal(err)
	}
	all, err := st.CatalogModels(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		inYAML := r.Model.FamilyID != "broken"
		if r.Model.Present != inYAML {
			t.Errorf("%s present=%v", r.Model.Size.OllamaTag, r.Model.Present)
		}
		for _, f := range r.Model.Files {
			if f.Quant == "Q4_K_M" && r.Model.FamilyID == "llama3.2" && f.Present {
				t.Error("a file gone from its repo is still present")
			}
		}
	}
}

func TestInstalledModelsAreMappedAndUnknownOnesFlagged(t *testing.T) {
	h, st, cat := setup(t)
	ctx := context.Background()
	if err := st.UpsertInstalledModels(ctx, "ollama", []backend.Installed{
		{Name: "llama3.2:1b", Family: "llama", ParameterSize: "1.2B", Quantization: "Q8_0"},
		{Name: "llama3.2:latest", Family: "llama", ParameterSize: "1.2B", Quantization: "Q4_K_M"},
		{Name: "llama3.2:1b-instruct-q2_K", Family: "llama", ParameterSize: "1.2B", Quantization: "Q2_K"},
		{Name: "hf.co/bartowski/Llama-3.2-1B-Instruct-GGUF:Q6_K", Family: "llama", ParameterSize: "1.24B", Quantization: "Q6_K"},
		{Name: "qwen3:4b", Family: "qwen3", ParameterSize: "4.0B", Quantization: "Q4_K_M"},
		{Name: "minicpm-v4.6:latest", Family: "qwen35", ParameterSize: "752M", Quantization: "Q4_K_M"},
	}); err != nil {
		t.Fatal(err)
	}
	// Before any refresh the catalogue is empty: every model is unknown.
	unknown, err := MapInstalled(ctx, st)
	if err != nil || len(unknown) != 6 {
		t.Fatalf("before a refresh: %d unknown, %v", len(unknown), err)
	}

	rep, err := Run(ctx, Options{Catalogue: cat, Store: st, HF: h.client(), Log: quiet(), Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, u := range rep.Unknown {
		names = append(names, u.Name)
		if !strings.Contains(u.Note, "families.yaml") {
			t.Errorf("%s: note %q does not tell the curator what to do", u.Name, u.Note)
		}
	}
	if !slices.Equal(names, []string{"minicpm-v4.6:latest", "qwen3:4b"}) {
		t.Errorf("unknown installed: %v", names)
	}

	rows, err := st.AllInstalledModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"llama3.2:1b": "file", "llama3.2:latest": "file", "llama3.2:1b-instruct-q2_K": "model",
		"hf.co/bartowski/Llama-3.2-1B-Instruct-GGUF:Q6_K": "file", "qwen3:4b": "unknown", "minicpm-v4.6:latest": "unknown",
	}
	for _, r := range rows {
		if r.CatalogMatch != want[r.Name] {
			t.Errorf("%s: match %q, want %q (%s)", r.Name, r.CatalogMatch, want[r.Name], r.CatalogNote)
		}
		if (r.CatalogMatch == "file") != (r.CatalogFileID != 0) || (r.CatalogMatch == "unknown") != (r.CatalogModelID == 0) {
			t.Errorf("%s: ids model=%d file=%d for match %q", r.Name, r.CatalogModelID, r.CatalogFileID, r.CatalogMatch)
		}
	}
	un, err := st.UnknownInstalledModels(ctx)
	if err != nil || len(un) != 2 {
		t.Errorf("UnknownInstalledModels: %d, %v", len(un), err)
	}
}

func TestRefreshStopsWhenHuggingFaceIsUnreachable(t *testing.T) {
	_, st, cat := setup(t)
	client := hf.New("advisor-test")
	client.BaseURL, client.MinInterval, client.Log = "http://127.0.0.1:1", 0, quiet()
	client.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	rep, err := Run(context.Background(), Options{Catalogue: cat, Store: st, HF: client, Log: quiet(), Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stopped == "" || len(rep.Failures) != 1 || rep.Resolved != 0 || rep.Sizes != 6 {
		t.Errorf("report: stopped=%q failures=%d resolved=%d sizes=%d", rep.Stopped, len(rep.Failures), rep.Resolved, rep.Sizes)
	}
	if client.Stats().Requests != client.MaxAttempts {
		t.Errorf("%d requests; one size's attempts, then stop", client.Stats().Requests)
	}
}

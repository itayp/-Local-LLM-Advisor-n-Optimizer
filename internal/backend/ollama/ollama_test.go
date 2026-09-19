package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"advisor/internal/backend"
)

// fakeEnv is the env seam's test double: no real environment variables, no
// real filesystem, no real $PATH — internal/hardware's tests take the same
// approach (fixtures instead of the live machine) for the same reason.
type fakeEnv struct {
	vars  map[string]string
	paths map[string]string // lookPath(file) -> resolved path, when present
	files map[string]int64  // existing regular files -> size
	dirs  map[string]bool   // existing directories
	home  string
}

func (e fakeEnv) getenv(k string) string { return e.vars[k] }

func (e fakeEnv) lookPath(f string) (string, error) {
	if p, ok := e.paths[f]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}

func (e fakeEnv) statSize(p string) (int64, bool, bool) {
	if e.dirs[p] {
		return 0, true, true
	}
	if sz, ok := e.files[p]; ok {
		return sz, false, true
	}
	return 0, false, false
}

func (e fakeEnv) userHomeDir() (string, error) { return e.home, nil }

func backendWithEnv(e env) *Backend {
	return &Backend{env: e}
}

// newTestBackend points a Backend at server via OLLAMA_HOST, with an env
// that finds no binary on disk (so the not-running fallback is exercised
// only when the test wants it to be).
func newTestBackend(t *testing.T, server *httptest.Server) *Backend {
	t.Helper()
	return backendWithEnv(fakeEnv{vars: map[string]string{"OLLAMA_HOST": server.URL}})
}

func TestHostHonoursOLLAMA_HOST(t *testing.T) {
	b := backendWithEnv(fakeEnv{vars: map[string]string{"OLLAMA_HOST": "127.0.0.1:9999"}})
	if got, want := b.host(), "http://127.0.0.1:9999"; got != want {
		t.Fatalf("host() = %q, want %q", got, want)
	}
	b2 := backendWithEnv(fakeEnv{})
	if got, want := b2.host(), "http://127.0.0.1:11434"; got != want {
		t.Fatalf("default host() = %q, want %q", got, want)
	}
	b3 := backendWithEnv(fakeEnv{vars: map[string]string{"OLLAMA_HOST": "https://example.com/"}})
	if got, want := b3.host(), "https://example.com"; got != want {
		t.Fatalf("host() with scheme = %q, want %q", got, want)
	}
}

func TestDetectRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			json.NewEncoder(w).Encode(map[string]string{"version": "0.34.2"})
		case "/api/ps":
			json.NewEncoder(w).Encode(psResp{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	b := newTestBackend(t, srv)
	status, err := b.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != backend.StateRunning {
		t.Fatalf("State = %v, want running", status.State)
	}
	if status.Version != "0.34.2" {
		t.Fatalf("Version = %q", status.Version)
	}
	if status.Host != srv.URL {
		t.Fatalf("Host = %q, want %q", status.Host, srv.URL)
	}
}

func TestDetectNotInstalled(t *testing.T) {
	// Nothing listens here, and the fake env finds no binary anywhere.
	b := backendWithEnv(fakeEnv{vars: map[string]string{"OLLAMA_HOST": "127.0.0.1:1"}})
	status, err := b.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != backend.StateNotInstalled {
		t.Fatalf("State = %v, want not_installed", status.State)
	}
}

func TestDetectInstalledNotRunning(t *testing.T) {
	// findBinary's $PATH lookup key is OS-specific (install_windows.go
	// looks up "ollama.exe"; install_darwin.go and install_linux.go look
	// up "ollama"). This test exercises Detect's shared fallback logic on
	// every OS CI runs on, so the fake PATH answers both names — the point
	// under test is "found on PATH", not which OS's exact binary name.
	b := backendWithEnv(fakeEnv{
		vars: map[string]string{"OLLAMA_HOST": "127.0.0.1:1"},
		paths: map[string]string{
			"ollama":     "/usr/local/bin/ollama",
			"ollama.exe": "/usr/local/bin/ollama.exe",
		},
	})
	status, err := b.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != backend.StateInstalledNotRunning {
		t.Fatalf("State = %v, want installed_not_running", status.State)
	}
	if status.Detail == "" {
		t.Fatal("Detail should say where the binary was found")
	}
}

func TestDetectCapturesRelevantEnvButNeverSetsIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"version": "0.34.2"})
	}))
	defer srv.Close()
	b := backendWithEnv(fakeEnv{vars: map[string]string{
		"OLLAMA_HOST":              srv.URL,
		"HSA_OVERRIDE_GFX_VERSION": "11.0.0",
		"UNRELATED_VAR":            "should not appear",
	}})
	status, err := b.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Env["HSA_OVERRIDE_GFX_VERSION"] != "11.0.0" {
		t.Fatalf("Env = %v, want HSA_OVERRIDE_GFX_VERSION captured", status.Env)
	}
	if _, ok := status.Env["UNRELATED_VAR"]; ok {
		t.Fatalf("Env captured an unrelated variable: %v", status.Env)
	}
}

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(tagsResp{Models: []tagModel{
			{Model: "llama3.1:8b", Size: 4_920_000_000, Digest: "sha256:abc", Details: struct {
				Family            string   `json:"family"`
				Families          []string `json:"families"`
				ParameterSize     string   `json:"parameter_size"`
				QuantizationLevel string   `json:"quantization_level"`
			}{Family: "llama", ParameterSize: "8.0B", QuantizationLevel: "Q4_K_M"}},
		}})
	}))
	defer srv.Close()

	b := newTestBackend(t, srv)
	models, err := b.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Name != "llama3.1:8b" || models[0].SizeBytes != 4_920_000_000 {
		t.Fatalf("Models() = %+v", models)
	}
	if models[0].Quantization != "Q4_K_M" {
		t.Fatalf("Quantization = %q", models[0].Quantization)
	}
}

func TestModelsErrorsWhenNotRunning(t *testing.T) {
	b := backendWithEnv(fakeEnv{vars: map[string]string{"OLLAMA_HOST": "127.0.0.1:1"}})
	if _, err := b.Models(context.Background()); err == nil {
		t.Fatal("Models() against a backend that is not running should error")
	}
}

// TestShowPrefersKeyLengthOverSpec is step 0's single most important
// finding for anything that reads model_info (ARCHITECTURE.md D-20): a
// model that states attention.key_length must have it available, distinct
// from embedding_length / head_count, in ModelInfo.Details — the estimator
// (step 5) is the one that decides which to use, but Show must not collapse
// or drop the field before it gets there.
func TestShowPrefersKeyLengthOverSpec(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			http.NotFound(w, r)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["model"] != "qwen3:4b" {
			t.Errorf("show request model = %v", req["model"])
		}
		json.NewEncoder(w).Encode(showResp{
			Capabilities: []string{"completion"},
			ModelInfo: map[string]any{
				"general.architecture":          "qwen3",
				"qwen3.block_count":             float64(36),
				"qwen3.attention.head_count":    float64(32),
				"qwen3.attention.head_count_kv": float64(8),
				"qwen3.embedding_length":        float64(2560),
				"qwen3.attention.key_length":    float64(128), // the real head_dim; 2560/32=80 would be wrong
				"qwen3.context_length":          float64(32768),
			},
		})
	}))
	defer srv.Close()

	b := newTestBackend(t, srv)
	info, err := b.Show(context.Background(), "qwen3:4b")
	if err != nil {
		t.Fatal(err)
	}
	if info.Architecture != "qwen3" {
		t.Fatalf("Architecture = %q", info.Architecture)
	}
	if info.ContextLength != 32768 {
		t.Fatalf("ContextLength = %d, want 32768", info.ContextLength)
	}
	keyLen, ok := miNum(info.Details, info.Architecture, "attention.key_length")
	if !ok || keyLen != 128 {
		t.Fatalf("Details attention.key_length = %v, %v, want 128, true", keyLen, ok)
	}
}

func TestShowHandlesPerLayerArraysAsMax(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(showResp{
			ModelInfo: map[string]any{
				"general.architecture":             "somearch",
				"somearch.attention.head_count_kv": []any{float64(4), float64(8), float64(8)},
			},
		})
	}))
	defer srv.Close()
	b := newTestBackend(t, srv)
	info, err := b.Show(context.Background(), "m")
	if err != nil {
		t.Fatal(err)
	}
	n, ok := miNum(info.Details, info.Architecture, "attention.head_count_kv")
	if !ok || n != 8 {
		t.Fatalf("per-layer array should reduce to its max: got %v, %v", n, ok)
	}
}

func TestRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(psResp{Models: []psModel{
			{Model: "llama3.1:8b", Size: 6_000_000_000, SizeVRAM: 5_800_000_000, ContextLength: 4096, ExpiresAt: "2026-09-18T20:00:00Z"},
		}})
	}))
	defer srv.Close()
	b := newTestBackend(t, srv)
	loaded, err := b.Running(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].SizeVRAMBytes != 5_800_000_000 || loaded[0].ContextLength != 4096 {
		t.Fatalf("Running() = %+v", loaded)
	}
}

func TestPullStreamsProgressAndSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			http.NotFound(w, r)
			return
		}
		flusher, _ := w.(http.Flusher)
		enc := json.NewEncoder(w)
		enc.Encode(map[string]any{"status": "pulling manifest"})
		if flusher != nil {
			flusher.Flush()
		}
		enc.Encode(map[string]any{"status": "pulling digest", "total": 1000, "completed": 500})
		if flusher != nil {
			flusher.Flush()
		}
		enc.Encode(map[string]any{"status": "success"})
	}))
	defer srv.Close()

	b := newTestBackend(t, srv)
	var updates []backend.PullProgress
	err := b.Pull(context.Background(), backend.ModelSource{Kind: backend.SourceOllamaTag, OllamaTag: "llama3.1:8b"},
		func(p backend.PullProgress) { updates = append(updates, p) })
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 3 {
		t.Fatalf("got %d progress updates, want 3: %+v", len(updates), updates)
	}
	if updates[1].Completed != 500 || updates[1].Total != 1000 {
		t.Fatalf("updates[1] = %+v", updates[1])
	}
	if updates[2].Status != "success" {
		t.Fatalf("final status = %q", updates[2].Status)
	}
}

func TestPullRejectsHuggingFaceSource(t *testing.T) {
	b := newTestBackend(t, httptest.NewServer(http.NotFoundHandler()))
	err := b.Pull(context.Background(), backend.ModelSource{Kind: backend.SourceHuggingFace, HFRepo: "org/repo", HFFile: "m.gguf"}, nil)
	if !errors.Is(err, backend.ErrUnsupportedSource) {
		t.Fatalf("Pull with a Hugging Face source: err = %v, want ErrUnsupportedSource", err)
	}
}

func TestPullPropagatesRuntimeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"error": "model not found"})
	}))
	defer srv.Close()
	b := newTestBackend(t, srv)
	err := b.Pull(context.Background(), backend.ModelSource{Kind: backend.SourceOllamaTag, OllamaTag: "nope:latest"}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestGenerateStreamsEventsAndFinalTiming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["model"] != "llama3.1:8b" || req["prompt"] != "hi" {
			t.Errorf("generate request = %v", req)
		}
		enc := json.NewEncoder(w)
		enc.Encode(genLine{Response: "Hel"})
		enc.Encode(genLine{Response: "lo"})
		enc.Encode(genLine{Done: true, DoneReason: "stop", EvalCount: 2, EvalDuration: int64(50 * time.Millisecond)})
	}))
	defer srv.Close()

	b := newTestBackend(t, srv)
	var got []backend.GenerateEvent
	err := b.Generate(context.Background(), backend.GenerateRequest{Model: "llama3.1:8b", Prompt: "hi"},
		func(e backend.GenerateEvent) error { got = append(got, e); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[0].Response != "Hel" || got[1].Response != "lo" {
		t.Fatalf("streamed text = %q, %q", got[0].Response, got[1].Response)
	}
	last := got[2]
	if !last.Done || last.DoneReason != "stop" || last.EvalCount != 2 {
		t.Fatalf("final event = %+v", last)
	}
	if last.EvalDuration != 50*time.Millisecond {
		t.Fatalf("EvalDuration = %v, want 50ms", last.EvalDuration)
	}
}

func TestGenerateRequiresModel(t *testing.T) {
	b := newTestBackend(t, httptest.NewServer(http.NotFoundHandler()))
	if err := b.Generate(context.Background(), backend.GenerateRequest{}, nil); err == nil {
		t.Fatal("Generate with no model should error")
	}
}

func TestUnloadSendsKeepAliveZero(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(genLine{Done: true})
	}))
	defer srv.Close()
	b := newTestBackend(t, srv)
	if err := b.Unload(context.Background(), "llama3.1:8b"); err != nil {
		t.Fatal(err)
	}
	if gotBody["model"] != "llama3.1:8b" {
		t.Fatalf("model = %v", gotBody["model"])
	}
	if ka, ok := gotBody["keep_alive"].(float64); !ok || ka != 0 {
		t.Fatalf("keep_alive = %v, want 0", gotBody["keep_alive"])
	}
}

func TestNameIsRegisteredOnPackageInit(t *testing.T) {
	if _, ok := backend.Lookup(Name); !ok {
		t.Fatal("ollama should have registered itself in the package-level registry via init()")
	}
}

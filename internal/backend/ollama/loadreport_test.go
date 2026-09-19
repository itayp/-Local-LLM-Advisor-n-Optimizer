package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"advisor/internal/backend"
	"advisor/internal/hardware"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The load a benchmark causes, as Ollama v0.34.2's log shows it
// (testdata/README.md says where each line's format comes from).
func TestParseLoadReport(t *testing.T) {
	cases := []struct {
		file               string
		path               hardware.RuntimePath
		devices            []string
		fa, faKnown        bool
		kv                 string
		onGPU, layers, ctx int
		parallel           int
	}{
		{"windows-cuda-load.log", hardware.PathCUDA, []string{"CUDA0", "CPU"}, true, true, "f16", 33, 33, 8192, 1},
		{"darwin-metal-load.log", hardware.PathMetal, []string{"CPU", "MTL0"}, true, true, "q8_0", 29, 29, 4096, 1},
		{"linux-vulkan-split.log", hardware.PathVulkan, []string{"Vulkan0", "CPU"}, false, true, "f16", 20, 33, 16384, 1},
		// Two loads: the report is the last one's, nothing of the first leaks in.
		{"two-loads.log", hardware.PathCUDA, []string{"CUDA0", "CPU"}, true, true, "f16", 33, 33, 8192, 1},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			r := parseLoadReport(readFixture(t, c.file))
			if r.RuntimePath != c.path || strings.Join(r.Devices, ",") != strings.Join(c.devices, ",") {
				t.Errorf("path %q devices %v, want %q %v", r.RuntimePath, r.Devices, c.path, c.devices)
			}
			if r.FlashAttention != c.fa || r.FlashAttentionKnown != c.faKnown {
				t.Errorf("flash attention %v (known %v), want %v (known %v)", r.FlashAttention, r.FlashAttentionKnown, c.fa, c.faKnown)
			}
			if r.KVCacheType != c.kv || r.LayersOnGPU != c.onGPU || r.Layers != c.layers || r.ContextSize != c.ctx || r.Parallel != c.parallel {
				t.Errorf("kv %q, layers %d/%d, ctx %d, parallel %d", r.KVCacheType, r.LayersOnGPU, r.Layers, r.ContextSize, r.Parallel)
			}
			if len(r.Evidence) < 4 {
				t.Errorf("evidence should quote the lines read: %v", r.Evidence)
			}
		})
	}
}

// "flash_attn = auto" on its own decides nothing: the value is unknown
// until llama.cpp's resolve line says what auto became.
func TestParseLoadReportAutoWithoutResolutionIsUnknown(t *testing.T) {
	r := parseLoadReport("llama_context: flash_attn    = auto\nload_tensors:        CUDA0 model buffer size =  100.00 MiB\n")
	if r.FlashAttentionKnown {
		t.Fatalf("auto alone must stay unknown: %+v", r)
	}
	if r.KVCacheType != "" || r.Layers != 0 {
		t.Fatalf("nothing else was stated: %+v", r)
	}
	if r := parseLoadReport("load_tensors:   CPU_Mapped model buffer size =  4000.00 MiB\n"); r.RuntimePath != hardware.PathCPU {
		t.Fatalf("only processor buffers: the load is on the processor, got %q", r.RuntimePath)
	}
}

// ObserveLoad reads only what was written after the mark: an earlier load
// in the same log says nothing about this one.
func TestObserveLoadReadsOnlyWhatFollowsTheMark(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "server.log")
	if err := os.WriteFile(logPath, []byte(readFixture(t, "linux-vulkan-split.log")), 0o644); err != nil {
		t.Fatal(err)
	}
	b := backendWithEnv(fakeEnv{home: dir})
	b.setSupervisedLog(logPath)

	report := b.ObserveLoad()
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(readFixture(t, "windows-cuda-load.log")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	r := report(context.Background())
	if !r.Read || r.RuntimePath != hardware.PathCUDA || r.Layers != 33 || r.KVCacheType != "f16" {
		t.Fatalf("report %+v", r)
	}
	for _, line := range r.Evidence {
		if strings.Contains(line, "Vulkan") {
			t.Fatalf("a line from before the mark leaked in: %q", line)
		}
	}

	// Nothing written since the mark: read, but nothing to say.
	quiet := b.ObserveLoad()
	if r := quiet(context.Background()); !r.Read || r.RuntimePath != "" || r.Why == "" {
		t.Fatalf("an unchanged log: %+v", r)
	}
}

// Detect with a model on the graphics card must not read the processor's
// backend line — loaded after the CUDA one by builds that load backends as
// libraries — as the path the model took.
func TestRuntimePathsFromPSIgnoresTheProcessorBackendWhenVRAMIsUsed(t *testing.T) {
	paths, detail := runtimePathsFromPS(4_900_000_000, readFixture(t, "windows-cuda-load.log"))
	if paths[0] != hardware.PathCUDA {
		t.Fatalf("paths[0] = %q (%s), want cuda", paths[0], detail)
	}
	// Without the model buffer lines, the backend-loading lines still point
	// at CUDA, not at the processor's backend loaded after it.
	log := "load_backend: loaded CUDA backend from ggml-cuda.dll\nload_backend: loaded CPU backend from ggml-cpu.dll\n"
	if paths, _ := runtimePathsFromPS(1, log); paths[0] != hardware.PathCUDA {
		t.Fatalf("paths[0] = %q, want cuda", paths[0])
	}
	if p, _, ok := pathFromLog("load_tensors:  MTL0_Mapped model buffer size =  1918.35 MiB", true); !ok || p != hardware.PathMetal {
		t.Fatalf("Metal's mapped buffer: %q %v", p, ok)
	}
}

// The benchmark's requests: raw text, no truncation or shifting, the cache
// count read back, thinking counted as output, and an error line mid-stream
// is an error — not a quiet end of the stream.
func TestGenerateRawNoTruncateAndCachedCount(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		enc := json.NewEncoder(w)
		_ = enc.Encode(map[string]any{"thinking": "hm"})
		_ = enc.Encode(map[string]any{"response": "The"})
		_ = enc.Encode(map[string]any{"done": true, "done_reason": "length", "prompt_eval_count": 480,
			"prompt_eval_cached_count": 2, "prompt_eval_duration": 400_000_000, "eval_count": 256, "eval_duration": 5_000_000_000})
	}))
	defer srv.Close()
	b := newTestBackend(t, srv)
	var events []backend.GenerateEvent
	err := b.Generate(context.Background(), backend.GenerateRequest{Model: "m", Prompt: "1.\n\nText", Raw: true, NoTruncate: true, KeepAlive: "5m",
		Options: map[string]any{"num_ctx": 4096}}, func(e backend.GenerateEvent) error { events = append(events, e); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got["raw"] != true || got["truncate"] != false || got["shift"] != false || got["keep_alive"] != "5m" {
		t.Fatalf("request body %v", got)
	}
	if len(events) != 3 || events[0].Thinking != "hm" || events[1].Response != "The" {
		t.Fatalf("events %+v", events)
	}
	last := events[2]
	if !last.Done || !last.PromptEvalCachedKnown || last.PromptEvalCached != 2 || last.EvalCount != 256 {
		t.Fatalf("final event %+v", last)
	}

	// An older server that does not report the cached count.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"done": true, "prompt_eval_count": 10, "eval_count": 5})
	}))
	defer srv2.Close()
	_ = newTestBackend(t, srv2).Generate(context.Background(), backend.GenerateRequest{Model: "m", Prompt: "x"},
		func(e backend.GenerateEvent) error { last = e; return nil })
	if last.PromptEvalCachedKnown {
		t.Fatalf("not stated must stay unknown: %+v", last)
	}

	// Ollama's refusal of a prompt longer than the context, and an error
	// reported mid-stream after the 200.
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "mid" {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "model runner has unexpectedly stopped"})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"the prompt is longer than the context length currently available to the model"}`))
	}))
	defer srv3.Close()
	err = newTestBackend(t, srv3).Generate(context.Background(), backend.GenerateRequest{Model: "m", Prompt: "x"}, nil)
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusBadRequest || !strings.Contains(se.Message, "longer than the context") {
		t.Fatalf("refusal: %v", err)
	}
	err = newTestBackend(t, srv3).Generate(context.Background(), backend.GenerateRequest{Model: "mid", Prompt: "x"}, nil)
	if !errors.As(err, &se) || !strings.Contains(se.Message, "unexpectedly stopped") {
		t.Fatalf("mid-stream error: %v", err)
	}
}

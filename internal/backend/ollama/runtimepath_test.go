package ollama

import (
	"testing"

	"advisor/internal/hardware"
)

func TestPathFromLogPicksLastMatchingLine(t *testing.T) {
	// A server can log a failed discovery for one backend and then succeed
	// with another; the later line must win.
	log := `time=2026-09-18T10:00:00 level=INFO msg="looking for compatible GPUs"
time=2026-09-18T10:00:01 level=WARN msg="failed rocm init"
time=2026-09-18T10:00:02 level=INFO source=types.go:130 msg="inference compute" id=GPU-0 library=cuda variant=v12 compute=8.9 name="NVIDIA GeForce RTX 4080" total="16.0 GiB" available="15.0 GiB"`
	path, evidence, ok := pathFromLog(log)
	if !ok {
		t.Fatal("expected a match")
	}
	if path != hardware.PathCUDA {
		t.Fatalf("path = %q, want cuda", path)
	}
	if evidence == "" {
		t.Fatal("evidence should quote the matching line")
	}
}

func TestPathFromLogCoversEveryBackend(t *testing.T) {
	cases := map[string]hardware.RuntimePath{
		`msg="inference compute" library=cuda`:     hardware.PathCUDA,
		`msg="inference compute" library=rocm`:     hardware.PathROCm,
		`msg="inference compute" library=metal`:    hardware.PathMetal,
		`msg="inference compute" library=vulkan`:   hardware.PathVulkan,
		`msg="no compatible GPUs were discovered"`: hardware.PathCPU,
		`ggml_vulkan: Found 1 Vulkan devices`:      hardware.PathVulkan,
		`ggml_cuda_init: found 1 CUDA devices`:     hardware.PathCUDA,
	}
	for line, want := range cases {
		got, _, ok := pathFromLog(line)
		if !ok || got != want {
			t.Errorf("pathFromLog(%q) = %q, %v, want %q, true", line, got, ok, want)
		}
	}
}

func TestPathFromLogEmpty(t *testing.T) {
	if _, _, ok := pathFromLog(""); ok {
		t.Fatal("empty log should not match")
	}
	if _, _, ok := pathFromLog("   \n  "); ok {
		t.Fatal("whitespace-only log should not match")
	}
}

func TestRuntimePathsFromPS_CPU(t *testing.T) {
	paths, detail := runtimePathsFromPS(0, "library=cuda") // even with a confusing log, 0 VRAM means CPU
	if paths[0] != hardware.PathCPU {
		t.Fatalf("paths[0] = %q, want cpu", paths[0])
	}
	if detail == "" {
		t.Fatal("expected a detail sentence")
	}
}

func TestRuntimePathsFromPS_ConfirmedFromLog(t *testing.T) {
	paths, detail := runtimePathsFromPS(4_000_000_000, `msg="inference compute" library=cuda`)
	if paths[0] != hardware.PathCUDA {
		t.Fatalf("paths[0] = %q, want cuda", paths[0])
	}
	if detail == "" {
		t.Fatal("expected a detail sentence naming the evidence")
	}
}

func TestRuntimePathsFromPS_UnconfirmedIsOmittedNotGuessed(t *testing.T) {
	paths, detail := runtimePathsFromPS(4_000_000_000, "")
	if _, ok := paths[0]; ok {
		t.Fatalf("paths[0] = %v, want no entry when the log cannot confirm a backend (D-21: unknown is unknown)", paths[0])
	}
	if detail == "" {
		t.Fatal("expected a detail sentence explaining why nothing was recorded")
	}
}

func TestCaptureEnvOnlyIncludesSetRelevantVars(t *testing.T) {
	b := backendWithEnv(fakeEnv{vars: map[string]string{
		"HSA_OVERRIDE_GFX_VERSION": "11.0.0",
		"PATH":                     "/usr/bin",
	}})
	env := b.captureEnv()
	if env["HSA_OVERRIDE_GFX_VERSION"] != "11.0.0" {
		t.Fatalf("captureEnv() = %v", env)
	}
	if _, ok := env["PATH"]; ok {
		t.Fatalf("captureEnv() included an irrelevant variable: %v", env)
	}
	if b2 := backendWithEnv(fakeEnv{}); b2.captureEnv() != nil {
		t.Fatalf("captureEnv() with nothing set should be nil, got %v", b2.captureEnv())
	}
}

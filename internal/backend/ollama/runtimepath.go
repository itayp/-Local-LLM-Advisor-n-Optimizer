package ollama

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"advisor/internal/hardware"
)

// logCandidates returns, in the order to try them, the log files Ollama may
// have written to (docs.ollama.com/troubleshooting). A run this process
// itself started (Start, in install.go) is tried first: it is known to be
// this exact process's own output, not possibly a stale run's.
func (b *Backend) logCandidates() []string {
	var out []string
	if p := b.getSupervisedLog(); p != "" {
		out = append(out, p)
	}
	home, _ := b.env.userHomeDir()
	switch runtime.GOOS {
	case "darwin", "linux":
		if home != "" {
			out = append(out, filepath.Join(home, ".ollama", "logs", "server.log"))
		}
		out = append(out, "/var/log/ollama/server.log")
	case "windows":
		if lad := b.env.getenv("LOCALAPPDATA"); lad != "" {
			out = append(out,
				filepath.Join(lad, "Ollama", "server.log"),
				filepath.Join(lad, "Ollama", "logs", "server.log"))
		}
	}
	return out
}

// readRecentLog returns up to the last 2 MiB of the first readable
// candidate log, journalctl's recent output as a last resort on Linux, or
// "" when none is available — always best-effort, never an error: the load
// already happened by the time this is asked, and a missing log only means
// the path below is left unconfirmed rather than that anything failed.
func (b *Backend) readRecentLog() string {
	for _, p := range b.logCandidates() {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		const max = 2 << 20
		if len(data) > max {
			data = data[len(data)-max:]
		}
		return string(data)
	}
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("journalctl"); err == nil {
			if out, err := exec.Command("journalctl", "-u", "ollama", "-n", "2000", "--no-pager").CombinedOutput(); err == nil {
				return string(out)
			}
		}
	}
	return ""
}

// pathFromLog reads Ollama's own device-discovery lines and reports the
// most recently named compute backend. Later lines win: a server can log a
// failed discovery for one backend and then succeed with another. The
// needles are Ollama/llama.cpp's own log vocabulary — step 0's probe0
// experiment (scripts/probe0/main.go, runtimePathFrom) validated the
// library=<name> form against real runs on CUDA, Metal and Vulkan across
// three machines; the loaded/ggml_* forms are kept as a fallback for older
// or differently-built binaries.
//
// gpuOnly skips the processor's lines. It is what a caller that already
// knows the model is on a graphics device (size_vram > 0) wants: llama.cpp
// builds that load their backends as libraries log the processor's backend
// last ("load_backend: loaded CPU backend from …" after the CUDA one), so
// without it the last line would name the processor for a model that sits
// wholly in graphics memory.
func pathFromLog(logText string, gpuOnly bool) (path hardware.RuntimePath, evidence string, ok bool) {
	if strings.TrimSpace(logText) == "" {
		return "", "", false
	}
	type rule struct {
		path   hardware.RuntimePath
		needle string
	}
	rules := []rule{
		{hardware.PathCUDA, "library=cuda"},
		{hardware.PathCUDA, "loaded cuda backend"},
		{hardware.PathCUDA, "ggml_cuda_init"},
		{hardware.PathROCm, "library=rocm"},
		{hardware.PathROCm, "loaded rocm backend"},
		{hardware.PathROCm, "ggml_rocm"},
		{hardware.PathMetal, "library=metal"},
		{hardware.PathMetal, "loaded metal backend"},
		{hardware.PathMetal, "ggml_metal_init"},
		{hardware.PathVulkan, "library=vulkan"},
		{hardware.PathVulkan, "loaded vulkan backend"},
		{hardware.PathVulkan, "ggml_vulkan: found"},
		{hardware.PathCPU, "library=cpu"},
		{hardware.PathCPU, "no compatible gpus were discovered"},
		{hardware.PathCPU, "loaded cpu backend"},
	}
	for _, line := range strings.Split(logText, "\n") {
		// llama.cpp's own line for each load names the device every part of
		// the weights went to — the most direct evidence there is.
		if p, ok2 := bufferPath(line); ok2 && (!gpuOnly || p.UsesGPU()) {
			path, evidence, ok = p, strings.TrimSpace(line), true
			continue
		}
		l := strings.ToLower(line)
		for _, r := range rules {
			if gpuOnly && !r.path.UsesGPU() {
				continue
			}
			if strings.Contains(l, r.needle) {
				path, evidence, ok = r.path, strings.TrimSpace(line), true
			}
		}
	}
	return path, evidence, ok
}

// modelBufferRE matches llama.cpp's per-load line
// "load_tensors:        CUDA0 model buffer size =  4403.49 MiB" (llama.cpp
// src/llama-model.cpp): the buffer's name is the device's ("CUDA0",
// "ROCm1", "MTL0_Mapped", "Vulkan0", "CPU_Mapped", "CPU_REPACK").
var modelBufferRE = regexp.MustCompile(`(?i)\b([A-Za-z]+)(\d*)(?:_[A-Za-z]+)? model buffer size\s*=\s*([\d.]+)\s*MiB`)

// devicePaths maps a llama.cpp backend device name (without its index) to
// the runtime path: ggml's GGML_CUDA_NAME ("CUDA", "ROCm" when built for
// HIP), GGML_METAL_NAME ("MTL"), GGML_VK_NAME ("Vulkan"), and the CPU.
var devicePaths = map[string]hardware.RuntimePath{
	"cuda":   hardware.PathCUDA,
	"rocm":   hardware.PathROCm,
	"mtl":    hardware.PathMetal,
	"metal":  hardware.PathMetal,
	"vulkan": hardware.PathVulkan,
	"cpu":    hardware.PathCPU,
}

// bufferPath reads one model-buffer line.
func bufferPath(line string) (hardware.RuntimePath, bool) {
	m := modelBufferRE.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	p, ok := devicePaths[strings.ToLower(m[1])]
	return p, ok
}

// runtimePathsFromPS derives Status.RuntimePaths from one /api/ps entry:
// which path Ollama actually took, per GPU index in the hardware profile.
// Ollama runs a model on one device at a time (step 0's finding: two 6 GB
// cards were not pooled into 12 GB — ARCHITECTURE.md D-20, finding 8), so
// this assigns at most one path, to index 0 — the device hardware.Profile's
// own ordering already puts first (orderGPUs: the runtime's likely answer,
// discrete before integrated, most memory first). Detect has no hardware
// profile to consult (its signature takes only a context), so index 0 is
// used as the best available stand-in for "the device Ollama used"; a
// caller that also holds the hardware profile (internal/server, step 3
// item 4) is what actually compares this against GPUs[0].ExpectedBackend.
//
// size_vram alone distinguishes CPU from GPU with certainty; which named
// GPU backend it was (cuda vs rocm vs vulkan) needs the server's own log,
// because /api/ps does not say. When the log cannot be read, the entry for
// index 0 is left out rather than guessed (D-21) — Detail says why.
func runtimePathsFromPS(sizeVRAM uint64, logText string) (paths map[int]hardware.RuntimePath, detail string) {
	paths = map[int]hardware.RuntimePath{}
	if sizeVRAM == 0 {
		paths[0] = hardware.PathCPU
		return paths, "ran on the CPU (/api/ps size_vram is 0)"
	}
	if path, evidence, ok := pathFromLog(logText, true); ok {
		paths[0] = path
		return paths, "confirmed from the server log: " + evidence
	}
	return paths, "a model is using GPU memory (/api/ps size_vram > 0), but the server log could not be read to confirm which backend"
}

// relevantEnvVars are the environment variables that steer which GPU path
// Ollama takes. Captured for Status.Env exactly as this process's own
// environment holds them — never set, never written to (CLAUDE.md's
// Network convention and product rule 7's spirit: nothing changes on the
// machine the app was not explicitly asked to change).
var relevantEnvVars = []string{
	"OLLAMA_HOST",
	"OLLAMA_VULKAN",
	"GGML_VK_VISIBLE_DEVICES",
	"HSA_OVERRIDE_GFX_VERSION",
	"CUDA_VISIBLE_DEVICES",
	"ROCR_VISIBLE_DEVICES",
}

func (b *Backend) captureEnv() map[string]string {
	out := map[string]string{}
	for _, k := range relevantEnvVars {
		if v := b.env.getenv(k); v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

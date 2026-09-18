// Command probe0 is step 0 of the Local LLM Advisor & Optimizer build plan:
// the experiment that decides whether the product's central claim — "I can tell
// you what fits your machine before you download it" — survives contact with
// real hardware.
//
// It does two things and prints a report:
//
//  1. Labels the machine: OS + version, CPU model + cores, RAM, and per GPU the
//     vendor, name and VRAM. Values that cannot be read are printed as
//     "unknown". Nothing is guessed.
//
//  2. Runs the estimator experiment against a local Ollama: for every installed
//     model and each of num_ctx = 4096 and 32768, predicts total memory, loads
//     the model, and compares the prediction with what Ollama reports and with
//     the actual VRAM delta measured from the vendor tool.
//
// It is deliberately one throwaway file: no UI, no database, no other packages,
// no dependencies outside the standard library. It moves to scripts/probe0/ in
// step 1.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	schemaVersion = 1
	toolName      = "probe0"

	// buildStamp is bumped whenever this file changes in a way a test machine
	// needs to pick up. `probe0 -version` prints it, the report header carries
	// it, and every JSON report records it — so a run made with a stale binary
	// identifies itself instead of quietly producing last week's numbers.
	buildStamp = "2026-09-18.5 (key_length head_dim, context clamp, per-backend overhead, -recompute)"
	mib        = 1 << 20
	gib        = 1 << 30
)

// ---------------------------------------------------------------------------
// Flags
// ---------------------------------------------------------------------------

var (
	flagHost         = flag.String("host", defaultOllamaHost(), "Ollama base URL")
	flagCtx          = flag.String("ctx", "4096,32768", "comma-separated num_ctx values to test")
	flagModels       = flag.String("models", "", "only test models whose name contains one of these comma-separated substrings")
	flagHardwareOnly = flag.Bool("hardware-only", false, "print the hardware report and exit")
	flagOut          = flag.String("out", "", "write the full report as JSON to this path (default probe0-<host>-<timestamp>.json)")
	flagNoOut        = flag.Bool("no-json", false, "do not write a JSON report")
	flagTimeout      = flag.Duration("timeout", 20*time.Minute, "HTTP timeout for a single model load")
	flagSettle       = flag.Duration("settle", 4*time.Second, "how long to wait after a load or unload before sampling VRAM")
	flagKeepAlive    = flag.String("keep-alive", "5m", "keep_alive sent with the load request")
	flagFixedMiB     = flag.Float64("overhead-fixed-mib", 208, "overhead term: fixed part, in MiB")
	flagGraphFactor  = flag.Float64("overhead-graph-factor", 12, "overhead term: number of full-width fp32 activation tensors assumed live in the compute graph")
	flagBatch        = flag.Int("batch", 512, "n_batch assumed by the overhead term (Ollama's default is 512)")
	flagRefit        = flag.String("refit", "", "comma-separated probe0 JSON reports: print the combined summary and overhead fit, then exit")
	flagLabel        = flag.String("label", "", "human label for this machine (default: hostname)")

	flagPull         = flag.Bool("pull", false, "download any missing models from the step-0 test set, then run")
	flagPullOnly     = flag.Bool("pull-only", false, "download any missing models from the step-0 test set, then exit")
	flagPullModels   = flag.String("pull-models", "", "comma-separated model list to ensure, replacing the built-in test set")
	flagBigDeviceGiB = flag.Float64("big-device-gib", 12, "a device this large or larger also gets the wide (5120) models")

	flagVersion = flag.Bool("version", false, "print the build stamp and exit")

	// Round 3 changed three things about the formula. Each is a flag so the
	// original can be reproduced: -head-dim spec -clamp-ctx=false
	// -overhead-model fixed gives exactly the prediction the build plan wrote.
	flagHeadDim       = flag.String("head-dim", "keylen", "head_dim source: keylen (attention.key_length when present) or spec (embedding_length/head_count)")
	flagClampCtx      = flag.Bool("clamp-ctx", true, "clamp num_ctx to the model's trained context_length, as Ollama itself does")
	flagOverheadModel = flag.String("overhead-model", "path", "overhead term: path (per runtime backend) or fixed (-overhead-fixed-mib + -overhead-graph-factor)")
	flagOverheadPath  = flag.String("overhead-by-path", "cuda=250,metal=0,vulkan=50,rocm=50,cpu=0", "per-backend overhead in MiB, used when -overhead-model=path")
	flagRecompute     = flag.Bool("recompute", false, "with -refit: recompute every prediction from the stored row data using the current formula flags, instead of using what each machine recorded")
)

func defaultOllamaHost() string {
	h := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if h == "" {
		return "http://127.0.0.1:11434"
	}
	if !strings.HasPrefix(h, "http://") && !strings.HasPrefix(h, "https://") {
		h = "http://" + h
	}
	return strings.TrimRight(h, "/")
}

// ---------------------------------------------------------------------------
// Report types (also the JSON schema)
// ---------------------------------------------------------------------------

type GPU struct {
	Vendor     string `json:"vendor"`
	Name       string `json:"name"`
	VRAMBytes  uint64 `json:"vram_bytes"` // 0 means unknown
	VRAMKnown  bool   `json:"vram_known"`
	VRAMSource string `json:"vram_source"`
	Note       string `json:"note,omitempty"`
}

type Hardware struct {
	OS               string `json:"os"`
	OSVersion        string `json:"os_version"`
	Kernel           string `json:"kernel,omitempty"`
	Arch             string `json:"arch"`
	Hostname         string `json:"hostname"`
	CPUModel         string `json:"cpu_model"`
	CPUCoresPhysical int    `json:"cpu_cores_physical"` // 0 means unknown
	CPUCoresLogical  int    `json:"cpu_cores_logical"`
	RAMBytes         uint64 `json:"ram_bytes"` // 0 means unknown
	RAMKnown         bool   `json:"ram_known"`
	GPUs             []GPU  `json:"gpus"`
	UnifiedMemory    bool   `json:"unified_memory"`

	// A model runs on ONE device unless the runtime explicitly splits it, so the
	// budget that decides "will it fit" is the largest single device, never the
	// sum. The sum is kept separately and clearly named.
	GPUBudgetBytes    uint64 `json:"gpu_budget_bytes"` // largest single device; 0 means unknown
	GPUBudgetKnown    bool   `json:"gpu_budget_known"`
	GPUBudgetSource   string `json:"gpu_budget_source"`
	GPUVRAMTotalBytes uint64 `json:"gpu_vram_total_bytes"` // sum across devices, for reference only
	GPUVRAMTotalKnown bool   `json:"gpu_vram_total_known"`
	GPUDeviceCount    int    `json:"gpu_device_count"`

	FilteredAdapters []string `json:"filtered_adapters,omitempty"` // virtual/remote display adapters, not real GPUs
	Problems         []string `json:"problems,omitempty"`
}

type OllamaInfo struct {
	Host            string `json:"host"`
	Version         string `json:"version"`
	Reachable       bool   `json:"reachable"`
	ModelCount      int    `json:"model_count"`
	RuntimePath     string `json:"runtime_path"`
	RuntimeEvidence string `json:"runtime_evidence,omitempty"`
	LogSource       string `json:"log_source,omitempty"`
	KVCacheTypeEnv  string `json:"kv_cache_type_env,omitempty"`
	FlashAttnEnv    string `json:"flash_attention_env,omitempty"`
}

type OverheadModel struct {
	Mode        string             `json:"mode"` // "path" or "fixed"
	Description string             `json:"description"`
	FixedMiB    float64            `json:"fixed_mib"`
	GraphFactor float64            `json:"graph_factor"`
	Batch       int                `json:"batch"`
	ByPathMiB   map[string]float64 `json:"by_path_mib,omitempty"`
	HeadDim     string             `json:"head_dim_source"`
	ClampCtx    bool               `json:"clamp_ctx"`
	Formula     string             `json:"formula"`
}

// normalizePath reduces "cuda", "gpu (inferred: size_vram > 0…)", "cpu (inferred…)"
// to the bare backend name the overhead table is keyed on.
func normalizePath(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	for _, name := range []string{"cuda", "metal", "vulkan", "rocm", "cpu"} {
		if strings.HasPrefix(p, name) {
			return name
		}
	}
	return p
}

func (o OverheadModel) bytes(embeddingLength int, runtimePath string) uint64 {
	if o.Mode == "path" {
		if v, ok := o.ByPathMiB[normalizePath(runtimePath)]; ok {
			return uint64(v * mib)
		}
		return 0
	}
	fixed := o.FixedMiB * mib
	graph := o.GraphFactor * float64(o.Batch) * float64(embeddingLength) * 4
	return uint64(fixed + graph)
}

func parseByPath(s string) map[string]float64 {
	out := map[string]float64{}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := splitKV(part, "=")
		if !ok {
			continue
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			out[normalizePath(k)] = f
		}
	}
	return out
}

type Row struct {
	Model         string `json:"model"`
	Digest        string `json:"digest,omitempty"`
	Arch          string `json:"arch"`
	Class         string `json:"class"` // dense | moe | vision | embedding | unknown
	ClassReason   string `json:"class_reason,omitempty"`
	Excluded      bool   `json:"excluded"` // excluded from the averages and the gate
	Quantization  string `json:"quantization"`
	ParameterSize string `json:"parameter_size,omitempty"`
	NumCtx        int    `json:"num_ctx"`

	BlockCount         int `json:"block_count"`
	HeadCount          int `json:"head_count"`
	HeadCountKV        int `json:"head_count_kv"`
	EmbeddingLength    int `json:"embedding_length"`
	HeadDim            int `json:"head_dim"`   // embedding_length / head_count, as the build plan specified
	KeyLength          int `json:"key_length"` // attention.key_length, 0 when the model does not state one
	EffectiveHeadDim   int `json:"effective_head_dim"`
	EffectiveCtx       int `json:"effective_ctx"`
	ModelContextLength int `json:"model_context_length"`

	WeightsBytes   uint64 `json:"weights_bytes"`
	KVBytes        uint64 `json:"kv_bytes"`
	OverheadBytes  uint64 `json:"overhead_bytes"`
	PredictedBytes uint64 `json:"predicted_bytes"`

	OllamaSizeBytes     uint64 `json:"ollama_size_bytes"`
	OllamaSizeVRAMBytes uint64 `json:"ollama_size_vram_bytes"`
	OllamaReported      bool   `json:"ollama_reported"`

	ActualVRAMDeltaBytes uint64 `json:"actual_vram_delta_bytes"`
	ActualKnown          bool   `json:"actual_known"`
	ActualSource         string `json:"actual_source,omitempty"`
	VRAMBaseBytes        uint64 `json:"vram_base_bytes,omitempty"`
	VRAMAfterBytes       uint64 `json:"vram_after_bytes,omitempty"`

	// Every reading this machine could give, not just the one that scores. A
	// source that does not track num_ctx is visible here instead of silently
	// deciding the gate.
	Measurements []MemDelta `json:"measurements,omitempty"`

	MeasuredBytes  uint64  `json:"measured_bytes"` // actual if known, else Ollama's size
	MeasuredSource string  `json:"measured_source,omitempty"`
	ErrPct         float64 `json:"err_pct"`
	ErrKnown       bool    `json:"err_known"`

	// Both errors, always, so the two can be compared after the fact.
	ErrPctVsActual   float64 `json:"err_pct_vs_actual"`
	ErrVsActualKnown bool    `json:"err_vs_actual_known"`
	ErrPctVsOllama   float64 `json:"err_pct_vs_ollama"`
	ErrVsOllamaKnown bool    `json:"err_vs_ollama_known"`

	SplitToCPU   bool    `json:"split_to_cpu"`
	VRAMFraction float64 `json:"vram_fraction"` // size_vram / size

	RuntimePath     string `json:"runtime_path,omitempty"`
	RuntimeEvidence string `json:"runtime_evidence,omitempty"`

	LoadSeconds float64  `json:"load_seconds,omitempty"`
	Skipped     bool     `json:"skipped"`
	SkipReason  string   `json:"skip_reason,omitempty"`
	Notes       []string `json:"notes,omitempty"`
	Error       string   `json:"error,omitempty"`
}

func (r *Row) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

type MemDelta struct {
	Source     string `json:"source"`
	BaseBytes  uint64 `json:"base_bytes"`
	AfterBytes uint64 `json:"after_bytes"`
	DeltaBytes uint64 `json:"delta_bytes"`
	DeltaKnown bool   `json:"delta_known"`
	Preferred  bool   `json:"preferred"`
}

type Report struct {
	SchemaVersion int           `json:"schema_version"`
	Tool          string        `json:"tool"`
	Build         string        `json:"build"`
	GeneratedAt   time.Time     `json:"generated_at"`
	MachineLabel  string        `json:"machine_label"`
	Hardware      Hardware      `json:"hardware"`
	Ollama        OllamaInfo    `json:"ollama"`
	Overhead      OverheadModel `json:"overhead"`
	CtxValues     []int         `json:"ctx_values"`
	Rows          []Row         `json:"rows"`
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Printf("%s build %s\n", toolName, buildStamp)
		return
	}

	if *flagRefit != "" {
		if err := refit(strings.Split(*flagRefit, ",")); err != nil {
			fmt.Fprintf(os.Stderr, "probe0: refit: %v\n", err)
			os.Exit(1)
		}
		return
	}

	hw := detectHardware()
	label := *flagLabel
	if label == "" {
		label = hw.Hostname
	}

	overhead := newOverheadModel()

	rep := Report{
		SchemaVersion: schemaVersion,
		Tool:          toolName,
		Build:         buildStamp,
		GeneratedAt:   time.Now(),
		MachineLabel:  label,
		Hardware:      hw,
		Overhead:      overhead,
	}

	printHardware(os.Stdout, rep)

	if *flagHardwareOnly {
		writeJSON(&rep)
		return
	}

	if *flagPull || *flagPullOnly {
		pc := newClient(*flagHost, 60*time.Second)
		if err := ensureModels(pc, &rep.Hardware); err != nil {
			fmt.Fprintf(os.Stderr, "probe0: %v\n", err)
			if *flagPullOnly {
				os.Exit(3)
			}
		}
		if *flagPullOnly {
			return
		}
	}

	ctxValues, err := parseCtxList(*flagCtx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe0: %v\n", err)
		os.Exit(2)
	}
	rep.CtxValues = ctxValues

	oi, rows := runExperiment(&rep, ctxValues)
	rep.Ollama = oi
	rep.Rows = rows

	if !oi.Reachable {
		fmt.Printf("\nOllama is not reachable at %s — hardware section only.\n", oi.Host)
		fmt.Println("Start Ollama (or set -host) and run again to produce the estimator rows.")
		writeJSON(&rep)
		os.Exit(3)
	}

	printExperiment(os.Stdout, &rep)
	printSummary(os.Stdout, []Report{rep})
	writeJSON(&rep)
}

func newOverheadModel() OverheadModel {
	o := OverheadModel{
		Mode:        *flagOverheadModel,
		FixedMiB:    *flagFixedMiB,
		GraphFactor: *flagGraphFactor,
		Batch:       *flagBatch,
		HeadDim:     *flagHeadDim,
		ClampCtx:    *flagClampCtx,
	}
	if o.Mode == "path" {
		o.ByPathMiB = parseByPath(*flagOverheadPath)
		o.Description = "a flat term per runtime backend: round 3 found the residual is a property of the " +
			"runtime (CUDA's context costs ~250 MiB, Metal's is near zero), not of the model's width"
		var parts []string
		for _, k := range []string{"cuda", "metal", "vulkan", "rocm", "cpu"} {
			if v, ok := o.ByPathMiB[k]; ok {
				parts = append(parts, fmt.Sprintf("%s %.0f MiB", k, v))
			}
		}
		o.Formula = "overhead = " + strings.Join(parts, ", ")
	} else {
		o.Description = "fixed runtime/allocator term plus a compute-graph term proportional to the width of the model"
		o.Formula = fmt.Sprintf("overhead = %.0f MiB + %.0f * n_batch(%d) * embedding_length * 4 B",
			o.FixedMiB, o.GraphFactor, o.Batch)
	}
	return o
}

func parseCtxList(s string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("bad -ctx value %q", part)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("-ctx is empty")
	}
	return out, nil
}

func writeJSON(rep *Report) {
	if *flagNoOut {
		return
	}
	path := *flagOut
	if path == "" {
		safe := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				return r
			}
			return '-'
		}, rep.MachineLabel)
		path = fmt.Sprintf("probe0-%s-%s.json", safe, rep.GeneratedAt.Format("20060102-150405"))
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe0: encoding report: %v\n", err)
		return
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "probe0: writing %s: %v\n", path, err)
		return
	}
	fmt.Printf("\nJSON report: %s\n", path)
}

// ---------------------------------------------------------------------------
// Shelling out
// ---------------------------------------------------------------------------

func run(timeout time.Duration, name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", fmt.Errorf("%s not found", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func powershell(script string) (string, error) {
	prelude := "$ErrorActionPreference='SilentlyContinue'; [Console]::OutputEncoding=[System.Text.Encoding]::UTF8; "
	var lastErr error
	for _, shell := range []string{"powershell", "pwsh"} {
		if !have(shell) {
			continue
		}
		out, err := run(90*time.Second, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", prelude+script)
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("neither powershell nor pwsh found")
	}
	return "", lastErr
}

func readFileTrim(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// ---------------------------------------------------------------------------
// Hardware detection
// ---------------------------------------------------------------------------

func detectHardware() Hardware {
	hw := Hardware{
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		CPUCoresLogical: runtime.NumCPU(),
		CPUModel:        "unknown",
		OSVersion:       "unknown",
	}
	if h, err := os.Hostname(); err == nil {
		hw.Hostname = h
	} else {
		hw.Hostname = "unknown"
	}

	switch runtime.GOOS {
	case "linux":
		detectLinux(&hw)
	case "darwin":
		detectDarwin(&hw)
	case "windows":
		detectWindows(&hw)
	default:
		hw.Problems = append(hw.Problems, "unsupported OS for detection: "+runtime.GOOS)
	}

	var total, largest uint64
	known := false
	for _, g := range hw.GPUs {
		if g.VRAMKnown {
			total += g.VRAMBytes
			if g.VRAMBytes > largest {
				largest = g.VRAMBytes
			}
			known = true
			hw.GPUDeviceCount++
		}
	}
	if known {
		hw.GPUVRAMTotalBytes = total
		hw.GPUVRAMTotalKnown = true
	}
	// macOS has already set a budget from iogpu.wired_limit_mb; don't overwrite it.
	if !hw.GPUBudgetKnown && known {
		hw.GPUBudgetBytes = largest
		hw.GPUBudgetKnown = true
		hw.GPUBudgetSource = "largest single device"
		if hw.GPUDeviceCount > 1 {
			hw.GPUBudgetSource += fmt.Sprintf(" of %d (a model runs on one device unless the runtime splits it)", hw.GPUDeviceCount)
		}
	}
	return hw
}

// --- Linux ---

func detectLinux(hw *Hardware) {
	if s, ok := readFileTrim("/etc/os-release"); ok {
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				hw.OSVersion = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
			}
		}
	}
	if s, ok := readFileTrim("/proc/sys/kernel/osrelease"); ok {
		hw.Kernel = s
	}

	if s, ok := readFileTrim("/proc/cpuinfo"); ok {
		physIDs := map[string]bool{}
		coreKeys := map[string]bool{}
		var curPhys, curCore string
		for _, line := range strings.Split(s, "\n") {
			k, v, ok := splitKV(line, ":")
			if !ok {
				if strings.TrimSpace(line) == "" {
					curPhys, curCore = "", ""
				}
				continue
			}
			switch k {
			case "model name", "Model", "cpu model", "Processor":
				if hw.CPUModel == "unknown" && v != "" {
					hw.CPUModel = v
				}
			case "Hardware":
				if hw.CPUModel == "unknown" && v != "" {
					hw.CPUModel = v
				}
			case "physical id":
				curPhys = v
				physIDs[v] = true
			case "core id":
				curCore = v
				if curPhys != "" {
					coreKeys[curPhys+"/"+curCore] = true
				}
			}
		}
		if len(coreKeys) > 0 {
			hw.CPUCoresPhysical = len(coreKeys)
		}
	}
	if hw.CPUModel == "unknown" {
		if out, err := run(10*time.Second, "lscpu"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if k, v, ok := splitKV(line, ":"); ok && k == "Model name" {
					hw.CPUModel = v
				}
			}
		}
	}

	if s, ok := readFileTrim("/proc/meminfo"); ok {
		for _, line := range strings.Split(s, "\n") {
			if k, v, ok := splitKV(line, ":"); ok && k == "MemTotal" {
				fields := strings.Fields(v)
				if len(fields) >= 1 {
					if kb, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
						hw.RAMBytes = kb * 1024
						hw.RAMKnown = true
					}
				}
			}
		}
	}

	nvidia := nvidiaGPUs()
	hw.GPUs = append(hw.GPUs, nvidia...)
	hw.GPUs = append(hw.GPUs, linuxDRMGPUs(len(nvidia) > 0)...)

	if len(hw.GPUs) == 0 {
		hw.GPUs = append(hw.GPUs, GPU{Vendor: "none detected", Name: "unknown", VRAMSource: "no nvidia-smi and no /sys/class/drm device with VRAM"})
	}
}

var drmCardRE = regexp.MustCompile(`^card[0-9]+$`)

func linuxDRMGPUs(skipNVIDIA bool) []GPU {
	var out []GPU
	entries, err := os.ReadDir("/sys/class/drm")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if drmCardRE.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, card := range names {
		dev := filepath.Join("/sys/class/drm", card, "device")
		vendorID, _ := readFileTrim(filepath.Join(dev, "vendor"))
		deviceID, _ := readFileTrim(filepath.Join(dev, "device"))
		vendor := pciVendorName(vendorID)
		if skipNVIDIA && strings.EqualFold(vendorID, "0x10de") {
			continue // already reported by nvidia-smi, with a real VRAM figure
		}
		g := GPU{Vendor: vendor, Name: "unknown", VRAMSource: "unknown"}

		slot, _ := readFileTrim(filepath.Join(dev, "uevent"))
		pciSlot := ""
		driver := ""
		for _, line := range strings.Split(slot, "\n") {
			if k, v, ok := splitKV(line, "="); ok {
				switch k {
				case "PCI_SLOT_NAME":
					pciSlot = v
				case "DRIVER":
					driver = v
				}
			}
		}
		if pciSlot != "" {
			if name := lspciName(pciSlot); name != "" {
				g.Name = name
			}
		}
		if g.Name == "unknown" && vendorID != "" && deviceID != "" {
			g.Name = fmt.Sprintf("PCI %s:%s", strings.TrimPrefix(vendorID, "0x"), strings.TrimPrefix(deviceID, "0x"))
		}
		if driver != "" {
			g.Note = "driver " + driver
		}

		for _, f := range []string{"mem_info_vram_total", "lmem_total_bytes"} {
			if s, ok := readFileTrim(filepath.Join(dev, f)); ok {
				if n, err := strconv.ParseUint(s, 10, 64); err == nil && n > 0 {
					g.VRAMBytes = n
					g.VRAMKnown = true
					g.VRAMSource = "/sys/class/drm/" + card + "/device/" + f
					break
				}
			}
		}
		if !g.VRAMKnown {
			g.VRAMSource = "unknown (no mem_info_vram_total; integrated GPUs share system RAM)"
		}
		out = append(out, g)
	}
	return out
}

func pciVendorName(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "0x10de":
		return "NVIDIA"
	case "0x1002", "0x1022":
		return "AMD"
	case "0x8086":
		return "Intel"
	case "0x106b":
		return "Apple"
	case "":
		return "unknown"
	}
	return "PCI " + id
}

func lspciName(slot string) string {
	out, err := run(10*time.Second, "lspci", "-mm", "-s", slot)
	if err != nil {
		return ""
	}
	// lspci -mm prints: slot "class" "vendor" "device" -rNN "subvendor" "subdevice"
	fields := splitQuoted(out)
	if len(fields) >= 4 {
		return strings.TrimSpace(fields[2] + " " + fields[3])
	}
	return ""
}

func splitQuoted(s string) []string {
	var out []string
	for _, part := range strings.Split(s, `"`) {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "-r") || strings.HasPrefix(part, "-p") {
			continue
		}
		out = append(out, part)
	}
	return out
}

// --- macOS ---

func detectDarwin(hw *Hardware) {
	hw.UnifiedMemory = runtime.GOARCH == "arm64"

	name, _ := run(10*time.Second, "sw_vers", "-productName")
	ver, _ := run(10*time.Second, "sw_vers", "-productVersion")
	build, _ := run(10*time.Second, "sw_vers", "-buildVersion")
	v := strings.TrimSpace(strings.TrimSpace(name) + " " + strings.TrimSpace(ver))
	if b := strings.TrimSpace(build); b != "" {
		v += " (" + b + ")"
	}
	if v != "" {
		hw.OSVersion = v
	}
	if k, err := run(10*time.Second, "uname", "-r"); err == nil {
		hw.Kernel = strings.TrimSpace(k)
	}

	if s, err := run(10*time.Second, "sysctl", "-n", "machdep.cpu.brand_string"); err == nil {
		if t := strings.TrimSpace(s); t != "" {
			hw.CPUModel = t
		}
	}
	if hw.CPUModel == "unknown" {
		if s, err := run(10*time.Second, "sysctl", "-n", "hw.model"); err == nil {
			if t := strings.TrimSpace(s); t != "" {
				hw.CPUModel = t
			}
		}
	}
	if n, ok := sysctlUint("hw.physicalcpu"); ok {
		hw.CPUCoresPhysical = int(n)
	}
	if n, ok := sysctlUint("hw.logicalcpu"); ok {
		hw.CPUCoresLogical = int(n)
	}
	if n, ok := sysctlUint("hw.memsize"); ok {
		hw.RAMBytes = n
		hw.RAMKnown = true
	}

	hw.GPUs = append(hw.GPUs, darwinGPUs(hw)...)
	if len(hw.GPUs) == 0 {
		hw.GPUs = append(hw.GPUs, GPU{Vendor: "unknown", Name: "unknown", VRAMSource: "system_profiler returned nothing"})
	}

	// The number that matters for "will it fit" on Apple Silicon is not VRAM —
	// there is none — but how much of unified memory the GPU is allowed to wire.
	if limit, ok := sysctlUint("iogpu.wired_limit_mb"); ok {
		if limit > 0 {
			hw.GPUBudgetBytes = limit * mib
			hw.GPUBudgetKnown = true
			hw.GPUBudgetSource = "sysctl iogpu.wired_limit_mb"
		} else {
			hw.GPUBudgetSource = "iogpu.wired_limit_mb = 0 (macOS default, unset — effective limit not readable)"
			hw.Problems = append(hw.Problems, "iogpu.wired_limit_mb is 0 (macOS default); the effective GPU memory budget is chosen by macOS and is not read here")
		}
	} else {
		hw.GPUBudgetSource = "sysctl iogpu.wired_limit_mb not present (Intel Mac or older macOS)"
	}
}

func sysctlUint(key string) (uint64, bool) {
	s, err := run(10*time.Second, "sysctl", "-n", key)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func darwinGPUs(hw *Hardware) []GPU {
	out, err := run(60*time.Second, "system_profiler", "SPDisplaysDataType", "-json")
	if err != nil {
		hw.Problems = append(hw.Problems, "system_profiler SPDisplaysDataType failed: "+err.Error())
		return nil
	}
	var parsed struct {
		Displays []map[string]any `json:"SPDisplaysDataType"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		hw.Problems = append(hw.Problems, "system_profiler JSON not understood: "+err.Error())
		return nil
	}
	var gpus []GPU
	for _, d := range parsed.Displays {
		g := GPU{Vendor: "unknown", Name: "unknown", VRAMSource: "unknown"}
		if s, ok := d["sppci_model"].(string); ok && s != "" {
			g.Name = s
		} else if s, ok := d["_name"].(string); ok && s != "" {
			g.Name = s
		}
		if s, ok := d["spdisplays_vendor"].(string); ok && s != "" {
			g.Vendor = strings.TrimPrefix(s, "sppci_vendor_")
		}
		for _, key := range []string{"sppci_vram", "spdisplays_vram", "spdisplays_vram_shared"} {
			if s, ok := d[key].(string); ok && s != "" {
				if n, ok := parseSizeString(s); ok {
					g.VRAMBytes = n
					g.VRAMKnown = true
					g.VRAMSource = "system_profiler " + key
					break
				}
			}
		}
		if !g.VRAMKnown {
			if runtime.GOARCH == "arm64" {
				g.Note = "Apple Silicon: unified memory, no dedicated VRAM"
				g.VRAMSource = "not applicable (unified memory)"
			} else {
				g.VRAMSource = "unknown (no vram field in system_profiler output)"
			}
		}
		gpus = append(gpus, g)
	}
	return gpus
}

var sizeRE = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*(kb|mb|gb|tb|b)?\s*$`)

func parseSizeString(s string) (uint64, bool) {
	m := sizeRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(m[2]) {
	case "kb":
		f *= 1 << 10
	case "mb":
		f *= 1 << 20
	case "gb":
		f *= 1 << 30
	case "tb":
		f *= 1 << 40
	}
	return uint64(f), true
}

// --- Windows ---

const winSysScript = `
$os = Get-CimInstance Win32_OperatingSystem
$cs = Get-CimInstance Win32_ComputerSystem
$cpu = @(Get-CimInstance Win32_Processor)[0]
[PSCustomObject]@{
  OSCaption = $os.Caption
  OSVersion = $os.Version
  OSBuild   = $os.BuildNumber
  TotalRAM  = [uint64]$cs.TotalPhysicalMemory
  CPUName   = $cpu.Name
  Cores     = $cpu.NumberOfCores
  Logical   = $cpu.NumberOfLogicalProcessors
} | ConvertTo-Json -Compress
`

// The display class key. HardwareInformation.qwMemorySize is a REG_QWORD and is
// the only place Windows records more than 4 GB of adapter memory;
// Win32_VideoController.AdapterRAM is a 32-bit field and wraps at 4 GB, so it is
// deliberately not used here.
const winGPUScript = `
$cls = 'HKLM:\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}'
$res = @()
Get-ChildItem $cls -ErrorAction SilentlyContinue | ForEach-Object {
  $p = Get-ItemProperty $_.PSPath -ErrorAction SilentlyContinue
  if ($p -and $p.DriverDesc) {
    $adapter = $p.'HardwareInformation.AdapterString'
    if ($adapter -isnot [string]) {
      if ($adapter -is [byte[]]) { $adapter = [System.Text.Encoding]::Unicode.GetString($adapter).Trim([char]0) }
      else { $adapter = '' }
    }
    $qw = $p.'HardwareInformation.qwMemorySize'
    $qwStr = ''
    if ($qw -ne $null) { $qwStr = [string]([uint64]$qw) }
    $res += [PSCustomObject]@{
      Desc     = [string]$p.DriverDesc
      Adapter  = [string]$adapter
      Provider = [string]$p.ProviderName
      QwMemory = $qwStr
      Key      = $_.PSChildName
    }
  }
}
ConvertTo-Json -InputObject @($res) -Compress
`

func detectWindows(hw *Hardware) {
	out, err := powershell(winSysScript)
	if err != nil {
		hw.Problems = append(hw.Problems, "powershell system query failed: "+err.Error())
	} else {
		var sys struct {
			OSCaption string  `json:"OSCaption"`
			OSVersion string  `json:"OSVersion"`
			OSBuild   string  `json:"OSBuild"`
			TotalRAM  float64 `json:"TotalRAM"`
			CPUName   string  `json:"CPUName"`
			Cores     int     `json:"Cores"`
			Logical   int     `json:"Logical"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &sys); err != nil {
			hw.Problems = append(hw.Problems, "powershell system JSON not understood: "+err.Error())
		} else {
			if sys.OSCaption != "" {
				hw.OSVersion = strings.TrimSpace(sys.OSCaption + " " + sys.OSVersion)
			}
			hw.Kernel = sys.OSBuild
			if sys.CPUName != "" {
				hw.CPUModel = strings.TrimSpace(sys.CPUName)
			}
			if sys.Cores > 0 {
				hw.CPUCoresPhysical = sys.Cores
			}
			if sys.Logical > 0 {
				hw.CPUCoresLogical = sys.Logical
			}
			if sys.TotalRAM > 0 {
				hw.RAMBytes = uint64(sys.TotalRAM)
				hw.RAMKnown = true
			}
		}
	}

	nvidia := nvidiaGPUs()
	hw.GPUs = append(hw.GPUs, nvidia...)

	out, err = powershell(winGPUScript)
	if err != nil {
		hw.Problems = append(hw.Problems, "powershell display-class registry query failed: "+err.Error())
	} else {
		var regs []struct {
			Desc     string `json:"Desc"`
			Adapter  string `json:"Adapter"`
			Provider string `json:"Provider"`
			QwMemory string `json:"QwMemory"`
			Key      string `json:"Key"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &regs); err != nil {
			hw.Problems = append(hw.Problems, "registry JSON not understood: "+err.Error())
		} else {
			for _, r := range regs {
				name := r.Desc
				if name == "" {
					name = r.Adapter
				}
				if isNVIDIAName(name) && len(nvidia) > 0 {
					continue // nvidia-smi already reported it, with a live figure
				}
				if virtualAdapterRE.MatchString(name) || virtualAdapterRE.MatchString(r.Adapter) {
					hw.FilteredAdapters = append(hw.FilteredAdapters, name)
					continue // a remote/virtual display head, not a GPU that can run a model
				}
				g := GPU{Vendor: vendorFromName(name), Name: name, VRAMSource: "unknown"}
				if r.QwMemory != "" {
					if n, err := strconv.ParseUint(r.QwMemory, 10, 64); err == nil && n > 0 {
						g.VRAMBytes = n
						g.VRAMKnown = true
						g.VRAMSource = `registry HardwareInformation.qwMemorySize (display class \` + r.Key + `)`
					}
				}
				if !g.VRAMKnown {
					g.VRAMSource = "unknown (no HardwareInformation.qwMemorySize under the display-class key)"
				}
				hw.GPUs = append(hw.GPUs, g)
			}
		}
	}

	if len(hw.GPUs) == 0 {
		hw.GPUs = append(hw.GPUs, GPU{Vendor: "none detected", Name: "unknown", VRAMSource: "no nvidia-smi and nothing under the display-class key"})
	}
}

// Display heads that are not GPUs: remote-desktop sinks, indirect display
// drivers, virtual monitors, and the software fallback adapter. None of them can
// run a model, and leaving them in the list makes a machine look like it has six
// graphics cards.
var virtualAdapterRE = regexp.MustCompile(`(?i)(remote display|remote desktop|basic display|basic render|virtual display|virtual monitor|virtual adapter|indirect display|\bidd\b|iddcx|sudomaker|parsec|splashtop|spacedesk|duet display|citrix|vmware svga|virtualbox|hyper-v video|teamviewer|usb display|displaylink)`)

func isNVIDIAName(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "nvidia") || strings.Contains(l, "geforce") || strings.Contains(l, "quadro") || strings.Contains(l, "rtx")
}

func vendorFromName(s string) string {
	l := strings.ToLower(s)
	switch {
	case isNVIDIAName(s):
		return "NVIDIA"
	case strings.Contains(l, "amd") || strings.Contains(l, "radeon") || strings.Contains(l, "ati"):
		return "AMD"
	case strings.Contains(l, "intel") || strings.Contains(l, "arc") || strings.Contains(l, "iris") || strings.Contains(l, "uhd"):
		return "Intel"
	case strings.Contains(l, "apple"):
		return "Apple"
	case strings.Contains(l, "microsoft") || strings.Contains(l, "basic display"):
		return "Microsoft (software adapter)"
	}
	return "unknown"
}

// --- nvidia-smi ---

func nvidiaGPUs() []GPU {
	out, err := run(30*time.Second, "nvidia-smi", "--query-gpu=name,memory.total,driver_version", "--format=csv,noheader,nounits")
	if err != nil {
		return nil
	}
	var gpus []GPU
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		g := GPU{Vendor: "NVIDIA", Name: "unknown", VRAMSource: "nvidia-smi memory.total"}
		if len(parts) > 0 {
			g.Name = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			if n, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil && n > 0 {
				g.VRAMBytes = uint64(n) * mib
				g.VRAMKnown = true
			}
		}
		if len(parts) > 2 {
			g.Note = "driver " + strings.TrimSpace(parts[2])
		}
		gpus = append(gpus, g)
	}
	return gpus
}

// ---------------------------------------------------------------------------
// VRAM sampling (the "actual" column)
// ---------------------------------------------------------------------------

type vramSample struct {
	Bytes  uint64
	Source string
	OK     bool
}

// sampleAllVRAM returns every device-memory reading this machine can give, most
// trustworthy first. Taking all of them costs one extra command per sample and
// means a reading that turns out not to track reality can be spotted in the data
// instead of quietly deciding the gate.
func sampleAllVRAM(hw *Hardware) []vramSample {
	var out []vramSample
	if hasVendor(hw, "NVIDIA") || have("nvidia-smi") {
		if s, ok := nvidiaUsed(); ok {
			out = append(out, s)
		}
	}
	if runtime.GOOS == "linux" {
		if s, ok := amdLinuxUsed(); ok {
			out = append(out, s)
		}
	}
	if runtime.GOOS == "darwin" {
		// Preferred: wired memory. On Apple Silicon a Metal buffer is wired, and
		// iogpu.wired_limit_mb is precisely the cap on it, so the wired total is
		// the closest thing the OS has to "VRAM in use".
		if s, ok := darwinWiredUsed(); ok {
			out = append(out, s)
		}
		// Kept for comparison only. In the 2026-09-18 round this reading did not
		// move with num_ctx — it fell as context grew eightfold — so it is
		// recorded but never scored against.
		if s, ok := darwinIoregUsed(); ok {
			out = append(out, s)
		}
	}
	return out
}

func preferredSample(samples []vramSample) vramSample {
	if len(samples) == 0 {
		return vramSample{}
	}
	return samples[0]
}

func hasVendor(hw *Hardware, vendor string) bool {
	for _, g := range hw.GPUs {
		if strings.EqualFold(g.Vendor, vendor) {
			return true
		}
	}
	return false
}

func nvidiaUsed() (vramSample, bool) {
	out, err := run(30*time.Second, "nvidia-smi", "--query-gpu=memory.used", "--format=csv,noheader,nounits")
	if err != nil {
		return vramSample{}, false
	}
	var total uint64
	found := false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if n, err := strconv.ParseFloat(line, 64); err == nil {
			total += uint64(n) * mib
			found = true
		}
	}
	if !found {
		return vramSample{}, false
	}
	return vramSample{Bytes: total, Source: "nvidia-smi memory.used", OK: true}, true
}

func amdLinuxUsed() (vramSample, bool) {
	// sysfs first: no tool to install, no output format to guess.
	entries, err := os.ReadDir("/sys/class/drm")
	if err == nil {
		var total uint64
		found := false
		var names []string
		for _, e := range entries {
			if !drmCardRE.MatchString(e.Name()) {
				continue
			}
			p := filepath.Join("/sys/class/drm", e.Name(), "device", "mem_info_vram_used")
			if s, ok := readFileTrim(p); ok {
				if n, err := strconv.ParseUint(s, 10, 64); err == nil {
					total += n
					found = true
					names = append(names, e.Name())
				}
			}
		}
		if found {
			return vramSample{Bytes: total, Source: "sysfs mem_info_vram_used (" + strings.Join(names, ",") + ")", OK: true}, true
		}
	}
	if out, err := run(30*time.Second, "rocm-smi", "--showmeminfo", "vram", "--csv"); err == nil {
		if n, ok := parseFirstBigNumberInColumn(out, "used"); ok {
			return vramSample{Bytes: n, Source: "rocm-smi --showmeminfo vram", OK: true}, true
		}
	}
	if out, err := run(30*time.Second, "amd-smi", "metric", "--mem-usage", "--csv"); err == nil {
		if n, ok := parseFirstBigNumberInColumn(out, "used"); ok {
			return vramSample{Bytes: n, Source: "amd-smi metric --mem-usage", OK: true}, true
		}
	}
	return vramSample{}, false
}

// parseFirstBigNumberInColumn sums the values of every CSV column whose header
// contains want (case-insensitive). rocm-smi and amd-smi report VRAM in bytes.
func parseFirstBigNumberInColumn(csv, want string) (uint64, bool) {
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if len(lines) < 2 {
		return 0, false
	}
	header := strings.Split(lines[0], ",")
	idx := -1
	for i, h := range header {
		if strings.Contains(strings.ToLower(h), strings.ToLower(want)) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, false
	}
	var total uint64
	found := false
	for _, line := range lines[1:] {
		cols := strings.Split(line, ",")
		if idx >= len(cols) {
			continue
		}
		v := strings.TrimSpace(cols[idx])
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			total += n
			found = true
		}
	}
	return total, found
}

var (
	vmStatPageSizeRE = regexp.MustCompile(`page size of (\d+) bytes`)
	vmStatWiredRE    = regexp.MustCompile(`Pages wired down:\s+(\d+)`)
)

// darwinWiredUsed reads system-wide wired memory. Metal allocations on Apple
// Silicon are wired, and iogpu.wired_limit_mb caps exactly this, so the delta
// across a model load is the closest available measurement of what the GPU took.
// It is system-wide, so close other GPU work before running.
func darwinWiredUsed() (vramSample, bool) {
	out, err := run(30*time.Second, "vm_stat")
	if err != nil {
		return vramSample{}, false
	}
	m := vmStatWiredRE.FindStringSubmatch(out)
	if m == nil {
		return vramSample{}, false
	}
	pages, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return vramSample{}, false
	}
	pageSize := uint64(4096)
	if p := vmStatPageSizeRE.FindStringSubmatch(out); p != nil {
		if n, err := strconv.ParseUint(p[1], 10, 64); err == nil && n > 0 {
			pageSize = n
		}
	} else if n, ok := sysctlUint("hw.pagesize"); ok && n > 0 {
		pageSize = n
	}
	return vramSample{
		Bytes:  pages * pageSize,
		Source: `vm_stat "Pages wired down" (Metal buffers are wired on Apple Silicon)`,
		OK:     true,
	}, true
}

var ioregInUseRE = regexp.MustCompile(`"In use system memory"=([0-9]+)`)

func darwinIoregUsed() (vramSample, bool) {
	out, err := run(60*time.Second, "ioreg", "-r", "-d", "1", "-w", "0", "-c", "IOAccelerator")
	if err != nil {
		return vramSample{}, false
	}
	matches := ioregInUseRE.FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		return vramSample{}, false
	}
	var total uint64
	for _, m := range matches {
		if n, err := strconv.ParseUint(m[1], 10, 64); err == nil {
			total += n
		}
	}
	return vramSample{Bytes: total, Source: `ioreg IOAccelerator "In use system memory"`, OK: true}, true
}

// ---------------------------------------------------------------------------
// Ollama client
// ---------------------------------------------------------------------------

type tagModel struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	Size    uint64 `json:"size"`
	Digest  string `json:"digest"`
	Details struct {
		Family            string   `json:"family"`
		Families          []string `json:"families"`
		ParameterSize     string   `json:"parameter_size"`
		QuantizationLevel string   `json:"quantization_level"`
	} `json:"details"`
}

type tagsResp struct {
	Models []tagModel `json:"models"`
}

type showResp struct {
	Details struct {
		Family            string   `json:"family"`
		Families          []string `json:"families"`
		ParameterSize     string   `json:"parameter_size"`
		QuantizationLevel string   `json:"quantization_level"`
	} `json:"details"`
	ModelInfo     map[string]any `json:"model_info"`
	ProjectorInfo map[string]any `json:"projector_info"`
	Capabilities  []string       `json:"capabilities"`
}

type psModel struct {
	Name          string `json:"name"`
	Model         string `json:"model"`
	Size          uint64 `json:"size"`
	SizeVRAM      uint64 `json:"size_vram"`
	ExpiresAt     string `json:"expires_at"`
	ContextLength int    `json:"context_length"`
}

type psResp struct {
	Models []psModel `json:"models"`
}

type client struct {
	base string
	http *http.Client
}

func newClient(base string, timeout time.Duration) *client {
	return &client{base: base, http: &http.Client{Timeout: timeout}}
}

func (c *client) do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(truncate(string(data), 300)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *client) version() string {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.do(http.MethodGet, "/api/version", nil, &v); err != nil {
		return "unknown"
	}
	return v.Version
}

func (c *client) tags() (tagsResp, error) {
	var t tagsResp
	err := c.do(http.MethodGet, "/api/tags", nil, &t)
	return t, err
}

func (c *client) show(model string) (showResp, error) {
	var s showResp
	// Ollama implements /api/show as POST; older builds also accept GET with a
	// query string. Try POST, then fall back.
	err := c.do(http.MethodPost, "/api/show", map[string]any{"model": model, "verbose": false}, &s)
	if err == nil {
		return s, nil
	}
	if err2 := c.do(http.MethodGet, "/api/show?model="+model, nil, &s); err2 == nil {
		return s, nil
	}
	return s, err
}

func (c *client) ps() (psResp, error) {
	var p psResp
	err := c.do(http.MethodGet, "/api/ps", nil, &p)
	return p, err
}

func (c *client) load(model string, numCtx int, keepAlive string, embedding bool) error {
	if embedding {
		body := map[string]any{
			"model":      model,
			"input":      "probe0",
			"keep_alive": keepAlive,
			"options":    map[string]any{"num_ctx": numCtx},
		}
		if err := c.do(http.MethodPost, "/api/embed", body, nil); err == nil {
			return nil
		}
		body2 := map[string]any{"model": model, "prompt": "probe0", "keep_alive": keepAlive}
		return c.do(http.MethodPost, "/api/embeddings", body2, nil)
	}
	body := map[string]any{
		"model":      model,
		"prompt":     "hi",
		"stream":     false,
		"keep_alive": keepAlive,
		"options": map[string]any{
			"num_ctx":     numCtx,
			"num_predict": 1,
			"temperature": 0,
		},
	}
	return c.do(http.MethodPost, "/api/generate", body, nil)
}

func (c *client) unload(model string) error {
	body := map[string]any{"model": model, "keep_alive": 0, "stream": false}
	if err := c.do(http.MethodPost, "/api/generate", body, nil); err == nil {
		return nil
	}
	body2 := map[string]any{"model": model, "input": "", "keep_alive": 0}
	return c.do(http.MethodPost, "/api/embed", body2, nil)
}

// pull streams /api/pull. It deliberately uses its own client with no overall
// timeout: a 9 GB download on a slow line legitimately takes longer than any
// per-request deadline worth setting for the rest of the API.
func (c *client) pull(name string, progress func(status string, completed, total int64)) error {
	body, err := json.Marshal(map[string]any{"model": name, "stream": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.base+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(truncate(string(data), 300)))
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
		if progress != nil {
			progress(msg.Status, msg.Completed, msg.Total)
		}
	}
}

func (c *client) unloadAll(settle time.Duration) {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		p, err := c.ps()
		if err != nil || len(p.Models) == 0 {
			break
		}
		for _, m := range p.Models {
			name := m.Model
			if name == "" {
				name = m.Name
			}
			_ = c.unload(name)
		}
		time.Sleep(1500 * time.Millisecond)
	}
	time.Sleep(settle)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------------------------------------------------------------------------
// Ollama server log: which path did it actually take?
// ---------------------------------------------------------------------------

type logSource struct {
	kind   string // "file" | "journalctl" | "none"
	path   string
	offset int64
	since  time.Time
}

func newLogSource() *logSource {
	ls := &logSource{kind: "none", since: time.Now().Add(-2 * time.Second)}
	var candidates []string
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin", "linux":
		if home != "" {
			candidates = append(candidates, filepath.Join(home, ".ollama", "logs", "server.log"))
		}
		candidates = append(candidates, "/var/log/ollama/server.log")
	case "windows":
		if lad := os.Getenv("LOCALAPPDATA"); lad != "" {
			candidates = append(candidates,
				filepath.Join(lad, "Ollama", "server.log"),
				filepath.Join(lad, "Ollama", "app.log"))
		}
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			ls.kind = "file"
			ls.path = p
			ls.offset = fi.Size()
			return ls
		}
	}
	if runtime.GOOS == "linux" && have("journalctl") {
		if _, err := run(15*time.Second, "journalctl", "-u", "ollama", "-n", "1", "--no-pager"); err == nil {
			ls.kind = "journalctl"
			ls.path = "journalctl -u ollama"
			return ls
		}
	}
	return ls
}

// all returns the whole log (or a generous tail of it).
func (ls *logSource) all() string {
	switch ls.kind {
	case "file":
		b, err := os.ReadFile(ls.path)
		if err != nil {
			return ""
		}
		const max = 2 << 20
		if len(b) > max {
			b = b[len(b)-max:]
		}
		return string(b)
	case "journalctl":
		out, err := run(30*time.Second, "journalctl", "-u", "ollama", "-n", "5000", "--no-pager")
		if err != nil {
			return ""
		}
		return out
	}
	return ""
}

// since returns whatever the server has logged since the previous call.
func (ls *logSource) sinceLast() string {
	switch ls.kind {
	case "file":
		f, err := os.Open(ls.path)
		if err != nil {
			return ""
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			return ""
		}
		if fi.Size() < ls.offset { // rotated
			ls.offset = 0
		}
		if _, err := f.Seek(ls.offset, io.SeekStart); err != nil {
			return ""
		}
		b, err := io.ReadAll(f)
		if err != nil {
			return ""
		}
		ls.offset += int64(len(b))
		return string(b)
	case "journalctl":
		stamp := ls.since.Format("2006-01-02 15:04:05")
		ls.since = time.Now()
		out, err := run(30*time.Second, "journalctl", "-u", "ollama", "--since", stamp, "--no-pager")
		if err != nil {
			return ""
		}
		return out
	}
	return ""
}

type pathMatch struct {
	Path     string
	Evidence string
}

// runtimePathFrom reads Ollama's device-discovery lines. The order matters: the
// last line that names a backend wins, because a server can log a discovery
// failure for one backend and then succeed with another.
func runtimePathFrom(logText string) pathMatch {
	if strings.TrimSpace(logText) == "" {
		return pathMatch{}
	}
	type rule struct {
		path   string
		needle string
	}
	rules := []rule{
		{"cuda", "library=cuda"},
		{"cuda", "loaded CUDA backend"},
		{"cuda", "ggml_cuda_init"},
		{"rocm", "library=rocm"},
		{"rocm", "loaded ROCm backend"},
		{"rocm", "ggml_rocm"},
		{"metal", "library=metal"},
		{"metal", "loaded Metal backend"},
		{"metal", "ggml_metal_init"},
		{"vulkan", "library=vulkan"},
		{"vulkan", "loaded Vulkan backend"},
		{"vulkan", "ggml_vulkan: Found"},
		{"cpu", "library=cpu"},
		{"cpu", "no compatible GPUs were discovered"},
		{"cpu", "loaded CPU backend"},
	}
	best := pathMatch{}
	for _, line := range strings.Split(logText, "\n") {
		l := strings.ToLower(line)
		for _, r := range rules {
			if strings.Contains(l, strings.ToLower(r.needle)) {
				best = pathMatch{Path: r.path, Evidence: strings.TrimSpace(truncate(strings.TrimSpace(line), 180))}
			}
		}
	}
	return best
}

// ---------------------------------------------------------------------------
// model_info helpers
// ---------------------------------------------------------------------------

func miValue(mi map[string]any, arch, key string) (any, bool) {
	if mi == nil {
		return nil, false
	}
	if arch != "" {
		if v, ok := mi[arch+"."+key]; ok {
			return v, true
		}
	}
	if v, ok := mi[key]; ok {
		return v, true
	}
	// Some builds use "general." or a differently spelled architecture prefix.
	suffix := "." + key
	for k, v := range mi {
		if strings.HasSuffix(k, suffix) {
			return v, true
		}
	}
	return nil, false
}

func miNum(mi map[string]any, arch, key string) (float64, bool) {
	v, ok := miValue(mi, arch, key)
	if !ok {
		return 0, false
	}
	return toNum(v)
}

// toNum accepts a number, a numeric string, or a per-layer array (in which case
// it takes the maximum — a few architectures give head counts per block).
func toNum(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	case []any:
		max := math.Inf(-1)
		found := false
		for _, e := range t {
			if f, ok := toNum(e); ok {
				found = true
				if f > max {
					max = f
				}
			}
		}
		if !found {
			return 0, false
		}
		return max, true
	}
	return 0, false
}

func miIsArray(mi map[string]any, arch, key string) bool {
	v, ok := miValue(mi, arch, key)
	if !ok {
		return false
	}
	_, isArr := v.([]any)
	return isArr
}

func archOf(s showResp) string {
	if s.ModelInfo == nil {
		return ""
	}
	if v, ok := s.ModelInfo["general.architecture"].(string); ok {
		return v
	}
	return ""
}

var moeArchHints = []string{"moe", "mixtral", "deepseek2", "deepseek3", "gptoss", "gpt-oss", "qwen3next", "granitemoe", "olmoe", "phimoe", "jamba"}

func classify(t tagModel, s showResp, arch string) (class, reason string) {
	caps := map[string]bool{}
	for _, c := range s.Capabilities {
		caps[strings.ToLower(c)] = true
	}
	families := strings.ToLower(strings.Join(append(append([]string{}, s.Details.Families...), t.Details.Families...), ","))

	if n, ok := miNum(s.ModelInfo, arch, "expert_count"); ok && n > 0 {
		return "moe", fmt.Sprintf("%s.expert_count = %d", arch, int(n))
	}
	la := strings.ToLower(arch)
	for _, h := range moeArchHints {
		if strings.Contains(la, h) {
			return "moe", "architecture " + arch
		}
	}
	if caps["vision"] {
		return "vision", "capability: vision"
	}
	if len(s.ProjectorInfo) > 0 {
		return "vision", "projector_info present"
	}
	for _, h := range []string{"clip", "mllama", "qwen2vl", "qwen2.5vl", "siglip", "vision"} {
		if strings.Contains(families, h) {
			return "vision", "families include " + h
		}
	}
	if caps["embedding"] || caps["embed"] {
		return "embedding", "capability: embedding"
	}
	if len(s.Capabilities) > 0 && !caps["completion"] {
		return "embedding", "no completion capability"
	}
	if strings.Contains(la, "bert") || strings.Contains(la, "xlm-roberta") {
		return "embedding", "architecture " + arch
	}
	if arch == "" {
		return "unknown", "no general.architecture in model_info"
	}
	return "dense", "dense transformer: " + arch
}

// ---------------------------------------------------------------------------
// The step-0 test set
// ---------------------------------------------------------------------------

type testModel struct {
	Name     string
	Width    int     // embedding_length, the thing the graph term scales with
	ApproxGB float64 // download size, for the "this will cost you" line
}

// Dense and text-only, on purpose: step 0 excludes MoE and vision from the gate,
// so a machine stocked with those produces a report with nothing in it. The
// widths are spread so the fixed and graph parts of the overhead term can be
// told apart — four models all 5120 wide would fit any straight line.
var baseTestModels = []testModel{
	{"llama3.2:1b", 2048, 1.3},
	{"qwen3:4b", 2560, 2.6},
	{"llama3.2:3b", 3072, 2.0},
	{"llama3.1:8b", 4096, 4.9},
}

// Only worth pulling onto a machine with a big enough device to hold them at
// 32k context; elsewhere they would only ever produce CPU-split rows.
var wideTestModels = []testModel{
	{"qwen3:14b", 5120, 9.3},
	{"phi4:14b", 5120, 9.1},
}

// practicalBudget is what one model can actually use. On unified memory the
// whole of RAM is not available to the GPU — macOS keeps a working set for
// everything else — so a fraction is used rather than the full figure.
func practicalBudget(hw *Hardware) (uint64, string) {
	if hw.GPUBudgetKnown && hw.GPUBudgetBytes > 0 {
		return hw.GPUBudgetBytes, hw.GPUBudgetSource
	}
	if hw.UnifiedMemory && hw.RAMKnown {
		return uint64(float64(hw.RAMBytes) * 0.6), "60% of unified memory (iogpu.wired_limit_mb is unset)"
	}
	if hw.RAMKnown {
		return hw.RAMBytes, "system RAM (no GPU memory figure)"
	}
	return 0, "unknown"
}

func testSetFor(hw *Hardware) ([]testModel, uint64, string) {
	if s := strings.TrimSpace(*flagPullModels); s != "" {
		var out []testModel
		for _, n := range strings.Split(s, ",") {
			if n = strings.TrimSpace(n); n != "" {
				out = append(out, testModel{Name: n})
			}
		}
		return out, 0, "given on the command line"
	}
	budget, source := practicalBudget(hw)
	set := append([]testModel(nil), baseTestModels...)
	if float64(budget) >= *flagBigDeviceGiB*gib {
		set = append(set, wideTestModels...)
	}
	return set, budget, source
}

// ensureModels downloads whatever the test set is missing. It is opt-in behind
// -pull because it writes several gigabytes to the machine; it prints the whole
// list and the total first, and never removes anything.
func ensureModels(c *client, hw *Hardware) error {
	set, budget, source := testSetFor(hw)
	if len(set) == 0 {
		return errors.New("empty model set")
	}

	tags, err := c.tags()
	if err != nil {
		return fmt.Errorf("GET /api/tags: %w", err)
	}
	have := map[string]bool{}
	for _, m := range tags.Models {
		have[m.Name] = true
		have[m.Model] = true
		have[strings.TrimSuffix(m.Name, ":latest")] = true
	}

	fmt.Printf("\nTEST SET\n")
	if budget > 0 {
		fmt.Printf("  sizing against %s (%s)\n", human(budget), source)
	}
	var missing []testModel
	var toDownload float64
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, m := range set {
		state := "MISSING"
		if have[m.Name] {
			state = "present"
		} else {
			missing = append(missing, m)
			toDownload += m.ApproxGB
		}
		width := "—"
		if m.Width > 0 {
			width = fmt.Sprintf("%d", m.Width)
		}
		size := ""
		if m.ApproxGB > 0 {
			size = fmt.Sprintf("~%.1f GB", m.ApproxGB)
		}
		fmt.Fprintf(tw, "  %s\twidth %s\t%s\t%s\n", m.Name, width, size, state)
	}
	// Say plainly what was left out and why, rather than silently shipping a
	// narrower set than the report's fit needs.
	if strings.TrimSpace(*flagPullModels) == "" && float64(budget) < *flagBigDeviceGiB*gib {
		for _, m := range wideTestModels {
			fmt.Fprintf(tw, "  %s\twidth %d\t~%.1f GB\tskipped (needs a %.0f GB device; this machine has %s)\n",
				m.Name, m.Width, m.ApproxGB, *flagBigDeviceGiB, human(budget))
		}
	}
	tw.Flush()

	if len(missing) == 0 {
		fmt.Printf("  nothing to download.\n")
		return nil
	}
	fmt.Printf("  downloading %d model(s), roughly %.1f GB\n\n", len(missing), toDownload)

	var failed []string
	for i, m := range missing {
		fmt.Printf("  [%d/%d] %s\n", i+1, len(missing), m.Name)
		last := time.Now().Add(-time.Hour)
		lastStatus := ""
		err := c.pull(m.Name, func(status string, completed, total int64) {
			if status != lastStatus {
				lastStatus = status
				last = time.Time{}
			}
			if time.Since(last) < 2*time.Second {
				return
			}
			last = time.Now()
			if total > 0 {
				fmt.Fprintf(os.Stderr, "\r        %-28s %5.1f%% of %s   ",
					truncate(status, 28), float64(completed)/float64(total)*100, human(uint64(total)))
			} else {
				fmt.Fprintf(os.Stderr, "\r        %-28s        ", truncate(status, 28))
			}
		})
		fmt.Fprintf(os.Stderr, "\r%-60s\r", "")
		if err != nil {
			fmt.Printf("        failed: %v\n", err)
			failed = append(failed, m.Name)
			continue
		}
		fmt.Printf("        done\n")
	}
	if len(failed) > 0 {
		fmt.Printf("\n  could not download: %s\n", strings.Join(failed, ", "))
		fmt.Printf("  A tag that no longer exists is the usual cause — check `ollama search`\n")
		fmt.Printf("  or pass -pull-models with the current equivalents.\n")
	}
	return nil
}

// ---------------------------------------------------------------------------
// The experiment
// ---------------------------------------------------------------------------

func runExperiment(rep *Report, ctxValues []int) (OllamaInfo, []Row) {
	hw := &rep.Hardware
	oi := OllamaInfo{
		Host:           *flagHost,
		KVCacheTypeEnv: os.Getenv("OLLAMA_KV_CACHE_TYPE"),
		FlashAttnEnv:   os.Getenv("OLLAMA_FLASH_ATTENTION"),
	}

	c := newClient(*flagHost, *flagTimeout)
	quick := newClient(*flagHost, 15*time.Second)

	tags, err := quick.tags()
	if err != nil {
		fmt.Printf("\nOLLAMA\n  %s — not reachable: %v\n", *flagHost, err)
		return oi, nil
	}
	oi.Reachable = true
	oi.Version = quick.version()
	oi.ModelCount = len(tags.Models)

	logs := newLogSource()
	oi.LogSource = logs.path
	if oi.LogSource == "" {
		oi.LogSource = "none found"
	}
	global := runtimePathFrom(logs.all())
	oi.RuntimePath = global.Path
	oi.RuntimeEvidence = global.Evidence
	if oi.RuntimePath == "" {
		oi.RuntimePath = "unknown"
	}
	logs.sinceLast() // reset the cursor to "now"

	fmt.Printf("\nOLLAMA\n")
	fmt.Printf("  host             %s\n", oi.Host)
	fmt.Printf("  version          %s\n", oi.Version)
	fmt.Printf("  models installed %d\n", oi.ModelCount)
	fmt.Printf("  server log       %s\n", oi.LogSource)
	fmt.Printf("  runtime path     %s\n", oi.RuntimePath)
	if oi.RuntimeEvidence != "" {
		fmt.Printf("  evidence         %s\n", oi.RuntimeEvidence)
	}
	if oi.KVCacheTypeEnv != "" {
		fmt.Printf("  OLLAMA_KV_CACHE_TYPE=%s — the KV term below assumes f16; this value makes it wrong\n", oi.KVCacheTypeEnv)
	}

	// Machine capacity, used only to skip models that cannot be run at all.
	capacity, capacityKnown, capacityNote := machineCapacity(hw)

	models := selectModels(tags.Models, *flagModels)
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })

	var rows []Row
	total := len(models) * len(ctxValues)
	done := 0

	for _, m := range models {
		name := m.Model
		if name == "" {
			name = m.Name
		}
		s, showErr := c.show(name)
		arch := archOf(s)
		class, reason := classify(m, s, arch)

		blockCount, okBC := miNum(s.ModelInfo, arch, "block_count")
		headCount, okHC := miNum(s.ModelInfo, arch, "attention.head_count")
		headCountKV, okKV := miNum(s.ModelInfo, arch, "attention.head_count_kv")
		embeddingLength, okEL := miNum(s.ModelInfo, arch, "embedding_length")
		modelCtx, _ := miNum(s.ModelInfo, arch, "context_length")
		keyLength, okKL := miNum(s.ModelInfo, arch, "attention.key_length")
		_, hasSWA := miValue(s.ModelInfo, arch, "attention.sliding_window")

		quant := s.Details.QuantizationLevel
		if quant == "" {
			quant = m.Details.QuantizationLevel
		}
		if quant == "" {
			quant = "unknown"
		}

		for _, numCtx := range ctxValues {
			done++
			r := Row{
				Model:              m.Name,
				Digest:             shortDigest(m.Digest),
				Arch:               arch,
				Class:              class,
				ClassReason:        reason,
				Excluded:           class != "dense",
				Quantization:       quant,
				ParameterSize:      firstNonEmpty(s.Details.ParameterSize, m.Details.ParameterSize),
				NumCtx:             numCtx,
				BlockCount:         int(blockCount),
				HeadCount:          int(headCount),
				HeadCountKV:        int(headCountKV),
				EmbeddingLength:    int(embeddingLength),
				ModelContextLength: int(modelCtx),
				WeightsBytes:       m.Size,
			}
			if arch == "" {
				r.Arch = "unknown"
			}
			if showErr != nil {
				r.Error = "GET /api/show failed: " + showErr.Error()
			}

			fmt.Fprintf(os.Stderr, "[%d/%d] %s @ num_ctx=%d ... ", done, total, m.Name, numCtx)

			if class != "dense" {
				r.note("EXCLUDED from the averages (%s: %s)", class, reason)
			}
			if !okBC || !okHC || !okKV || !okEL || headCount == 0 {
				r.Skipped = true
				r.SkipReason = "model_info is missing block_count / head_count / head_count_kv / embedding_length"
				rows = append(rows, r)
				fmt.Fprintln(os.Stderr, "skipped (no model_info)")
				continue
			}
			if miIsArray(s.ModelInfo, arch, "attention.head_count_kv") || miIsArray(s.ModelInfo, arch, "attention.head_count") {
				r.note("head counts are per-layer arrays; the maximum was used")
			}

			headDim := int(embeddingLength) / int(headCount)
			r.HeadDim = headDim
			if okKL {
				r.KeyLength = int(keyLength)
			}

			// Round 3's finding: where a model states attention.key_length, that is
			// the real head dimension and embedding_length/head_count is not.
			// qwen3:4b states 128 against a computed 80, and predicting with 80
			// under-shot by 18–30% at 32k on all three machines.
			effHeadDim := headDim
			if *flagHeadDim == "keylen" && r.KeyLength > 0 {
				effHeadDim = r.KeyLength
			}
			if okKL && int(keyLength) != headDim {
				r.note("%s.attention.key_length = %d but embedding_length/head_count = %d; using %d (-head-dim=%s)",
					arch, int(keyLength), headDim, effHeadDim, *flagHeadDim)
			}

			// Ollama clamps a request to the model's trained context; predicting
			// against the number we asked for rather than the one it used cost
			// phi4:14b a 29% error that was never the formula's fault.
			effCtx := numCtx
			if *flagClampCtx && modelCtx > 0 && float64(numCtx) > modelCtx {
				effCtx = int(modelCtx)
			}
			r.EffectiveHeadDim = effHeadDim
			r.EffectiveCtx = effCtx
			if hasSWA {
				r.note("this architecture declares a sliding window; its real KV cache is smaller than the full-context formula")
			}
			if modelCtx > 0 && float64(numCtx) > modelCtx {
				r.note("num_ctx %d exceeds the model's trained context_length %d", numCtx, int(modelCtx))
			}

			r.KVBytes = uint64(2 * blockCount * headCountKV * float64(effHeadDim) * float64(effCtx) * 2)
			r.OverheadBytes = rep.Overhead.bytes(int(embeddingLength), oi.RuntimePath)
			r.PredictedBytes = r.WeightsBytes + r.KVBytes + r.OverheadBytes

			if capacityKnown && m.Size > capacity {
				r.Skipped = true
				r.SkipReason = fmt.Sprintf("weights alone (%s) exceed this machine's capacity (%s, %s) — not loaded",
					human(m.Size), human(capacity), capacityNote)
				rows = append(rows, r)
				fmt.Fprintln(os.Stderr, "skipped (too big for this machine)")
				continue
			}

			measure(c, logs, hw, &r, name, numCtx, class == "embedding")
			rows = append(rows, r)

			if r.Error != "" {
				fmt.Fprintf(os.Stderr, "error: %s\n", r.Error)
			} else if r.ErrKnown {
				fmt.Fprintf(os.Stderr, "predicted %s vs measured %s (%+.1f%%)\n", human(r.PredictedBytes), human(r.MeasuredBytes), r.ErrPct)
			} else {
				fmt.Fprintln(os.Stderr, "no measurement")
			}
		}
	}

	c.unloadAll(500 * time.Millisecond)
	return oi, rows
}

func measure(c *client, logs *logSource, hw *Hardware, r *Row, name string, numCtx int, embedding bool) {
	c.unloadAll(*flagSettle)

	base := sampleAllVRAM(hw)
	start := time.Now()
	if err := c.load(name, numCtx, *flagKeepAlive, embedding); err != nil {
		r.Error = "load failed: " + err.Error()
		return
	}
	r.LoadSeconds = time.Since(start).Seconds()
	time.Sleep(*flagSettle)

	after := sampleAllVRAM(hw)
	p, err := c.ps()
	if err != nil {
		r.Error = "GET /api/ps failed: " + err.Error()
	} else {
		for _, pm := range p.Models {
			if pm.Model == name || pm.Name == name || pm.Name == r.Model || pm.Model == r.Model {
				r.OllamaSizeBytes = pm.Size
				r.OllamaSizeVRAMBytes = pm.SizeVRAM
				r.OllamaReported = true
				if pm.Size > 0 {
					r.VRAMFraction = float64(pm.SizeVRAM) / float64(pm.Size)
				}
				r.SplitToCPU = pm.SizeVRAM < pm.Size
				if pm.ContextLength > 0 && pm.ContextLength != numCtx {
					r.note("Ollama reports context_length %d for a request of num_ctx %d", pm.ContextLength, numCtx)
				}
				break
			}
		}
		if !r.OllamaReported {
			if len(p.Models) == 1 {
				pm := p.Models[0]
				r.OllamaSizeBytes = pm.Size
				r.OllamaSizeVRAMBytes = pm.SizeVRAM
				r.OllamaReported = true
				r.SplitToCPU = pm.SizeVRAM < pm.Size
				if pm.Size > 0 {
					r.VRAMFraction = float64(pm.SizeVRAM) / float64(pm.Size)
				}
				r.note("matched the only entry in /api/ps (%q) by position, not by name", pm.Name)
			} else {
				r.note("model not found in /api/ps after the load")
			}
		}
	}

	baseBySource := map[string]vramSample{}
	for _, s := range base {
		baseBySource[s.Source] = s
	}
	preferred := preferredSample(after).Source
	for _, a := range after {
		b, ok := baseBySource[a.Source]
		if !ok {
			continue
		}
		d := MemDelta{Source: a.Source, BaseBytes: b.Bytes, AfterBytes: a.Bytes, Preferred: a.Source == preferred}
		if a.Bytes > b.Bytes {
			d.DeltaBytes = a.Bytes - b.Bytes
			d.DeltaKnown = true
		}
		r.Measurements = append(r.Measurements, d)
		if d.Preferred {
			r.VRAMBaseBytes = d.BaseBytes
			r.VRAMAfterBytes = d.AfterBytes
			r.ActualSource = d.Source
			r.ActualVRAMDeltaBytes = d.DeltaBytes
			r.ActualKnown = d.DeltaKnown
			if !d.DeltaKnown {
				r.note("device-memory delta not positive (%s before, %s after) — nothing was placed on the GPU, or another process freed memory during the load",
					human(d.BaseBytes), human(d.AfterBytes))
			}
		}
	}
	// Two readings that disagree by more than 2x mean one of them is not
	// measuring the model. Say so on the row rather than averaging them.
	if len(r.Measurements) > 1 {
		var lo, hi uint64
		for _, m := range r.Measurements {
			if !m.DeltaKnown {
				continue
			}
			if lo == 0 || m.DeltaBytes < lo {
				lo = m.DeltaBytes
			}
			if m.DeltaBytes > hi {
				hi = m.DeltaBytes
			}
		}
		if lo > 0 && float64(hi)/float64(lo) > 2 {
			var parts []string
			for _, m := range r.Measurements {
				parts = append(parts, fmt.Sprintf("%s = %s", m.Source, humanOr(m.DeltaBytes, m.DeltaKnown)))
			}
			r.note("device-memory readings disagree by more than 2x: %s — at most one of these is measuring the model", strings.Join(parts, "; "))
		}
	}

	if newLines := logs.sinceLast(); newLines != "" {
		if pm := runtimePathFrom(newLines); pm.Path != "" {
			r.RuntimePath = pm.Path
			r.RuntimeEvidence = pm.Evidence
		}
	}
	if r.RuntimePath == "" {
		// No fresh discovery line: infer from where the weights ended up.
		switch {
		case r.OllamaReported && r.OllamaSizeVRAMBytes == 0:
			r.RuntimePath = "cpu (inferred: size_vram = 0)"
		case r.OllamaReported && r.OllamaSizeVRAMBytes > 0:
			r.RuntimePath = "gpu (inferred: size_vram > 0; backend not named in the log)"
		default:
			r.RuntimePath = "unknown"
		}
	}

	// The prediction is a whole-model figure, so it must be compared with a
	// whole-model measurement. The VRAM delta is the best one available — but
	// only while everything is on the GPU. The moment Ollama splits layers to
	// the CPU the delta covers the GPU part alone, and /api/ps size is the only
	// number that still describes the whole model.
	switch {
	case r.ActualKnown && !(r.OllamaReported && r.SplitToCPU):
		r.MeasuredBytes = r.ActualVRAMDeltaBytes
		r.MeasuredSource = r.ActualSource
	case r.OllamaReported:
		r.MeasuredBytes = r.OllamaSizeBytes
		r.MeasuredSource = "/api/ps size"
		if r.ActualKnown {
			r.note("layers were split to the CPU, so the VRAM delta (%s) measures only the GPU part; the error above is against /api/ps size (%s)",
				human(r.ActualVRAMDeltaBytes), human(r.OllamaSizeBytes))
		}
	case r.ActualKnown:
		r.MeasuredBytes = r.ActualVRAMDeltaBytes
		r.MeasuredSource = r.ActualSource
	}
	if r.MeasuredBytes > 0 {
		r.ErrPct = errPct(r.PredictedBytes, r.MeasuredBytes)
		r.ErrKnown = true
	}
	if r.ActualKnown && r.ActualVRAMDeltaBytes > 0 {
		r.ErrPctVsActual = errPct(r.PredictedBytes, r.ActualVRAMDeltaBytes)
		r.ErrVsActualKnown = true
	}
	if r.OllamaReported && r.OllamaSizeBytes > 0 {
		r.ErrPctVsOllama = errPct(r.PredictedBytes, r.OllamaSizeBytes)
		r.ErrVsOllamaKnown = true
	}

	_ = c.unload(name)
}

func errPct(predicted, measured uint64) float64 {
	if measured == 0 {
		return 0
	}
	return (float64(predicted) - float64(measured)) / float64(measured) * 100
}

func machineCapacity(hw *Hardware) (uint64, bool, string) {
	if !hw.RAMKnown {
		return 0, false, "RAM unknown"
	}
	if hw.UnifiedMemory {
		return hw.RAMBytes, true, "unified memory"
	}
	total := hw.RAMBytes
	note := "RAM"
	var vram uint64
	for _, g := range hw.GPUs {
		if g.VRAMKnown {
			vram += g.VRAMBytes
		}
	}
	if vram > 0 {
		total += vram
		note = "RAM + VRAM"
	}
	return total, true, note
}

func selectModels(models []tagModel, filter string) []tagModel {
	if strings.TrimSpace(filter) == "" {
		return models
	}
	var wanted []string
	for _, f := range strings.Split(filter, ",") {
		if f = strings.TrimSpace(f); f != "" {
			wanted = append(wanted, strings.ToLower(f))
		}
	}
	var out []tagModel
	for _, m := range models {
		l := strings.ToLower(m.Name)
		for _, w := range wanted {
			if strings.Contains(l, w) {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Printing
// ---------------------------------------------------------------------------

func human(b uint64) string {
	switch {
	case b == 0:
		return "0"
	case b >= gib:
		return fmt.Sprintf("%.2fG", float64(b)/gib)
	case b >= mib:
		return fmt.Sprintf("%.0fM", float64(b)/mib)
	default:
		return fmt.Sprintf("%dB", b)
	}
}

func humanOr(b uint64, known bool) string {
	if !known {
		return "unknown"
	}
	return human(b)
}

func printHardware(w io.Writer, rep Report) {
	hw := rep.Hardware
	fmt.Fprintf(w, "probe0 — Local LLM Advisor & Optimizer, step 0\n")
	fmt.Fprintf(w, "build %s\n", rep.Build)
	fmt.Fprintf(w, "%s\n", rep.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "\nMACHINE  %s\n", rep.MachineLabel)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  OS\t%s (%s/%s)\n", hw.OSVersion, hw.OS, hw.Arch)
	if hw.Kernel != "" {
		fmt.Fprintf(tw, "  kernel\t%s\n", hw.Kernel)
	}
	cores := "unknown"
	if hw.CPUCoresPhysical > 0 {
		cores = fmt.Sprintf("%d physical / %d logical", hw.CPUCoresPhysical, hw.CPUCoresLogical)
	} else if hw.CPUCoresLogical > 0 {
		cores = fmt.Sprintf("%d logical (physical unknown)", hw.CPUCoresLogical)
	}
	fmt.Fprintf(tw, "  CPU\t%s\n", hw.CPUModel)
	fmt.Fprintf(tw, "  cores\t%s\n", cores)
	fmt.Fprintf(tw, "  RAM\t%s\n", humanOr(hw.RAMBytes, hw.RAMKnown))
	tw.Flush()

	for i, g := range hw.GPUs {
		fmt.Fprintf(w, "  GPU %d\n", i)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintf(tw, "    vendor\t%s\n", g.Vendor)
		fmt.Fprintf(tw, "    name\t%s\n", g.Name)
		fmt.Fprintf(tw, "    VRAM\t%s\t(%s)\n", humanOr(g.VRAMBytes, g.VRAMKnown), g.VRAMSource)
		if g.Note != "" {
			fmt.Fprintf(tw, "    note\t%s\n", g.Note)
		}
		tw.Flush()
	}
	if hw.GPUDeviceCount > 1 {
		fmt.Fprintf(w, "  VRAM across %d devices  %s total — NOT a single pool\n",
			hw.GPUDeviceCount, humanOr(hw.GPUVRAMTotalBytes, hw.GPUVRAMTotalKnown))
	}
	if hw.UnifiedMemory || hw.GPUBudgetSource != "" {
		fmt.Fprintf(w, "  GPU memory budget  %s  (%s)\n", humanOr(hw.GPUBudgetBytes, hw.GPUBudgetKnown), hw.GPUBudgetSource)
	}
	if len(hw.FilteredAdapters) > 0 {
		fmt.Fprintf(w, "  ignored display adapters (not GPUs): %s\n", strings.Join(hw.FilteredAdapters, ", "))
	}
	for _, p := range hw.Problems {
		fmt.Fprintf(w, "  ! %s\n", p)
	}
}

func printExperiment(w io.Writer, rep *Report) {
	fmt.Fprintf(w, "\nOVERHEAD TERM (stated, not fitted)\n  %s\n  %s\n",
		rep.Overhead.Formula, rep.Overhead.Description)
	fmt.Fprintf(w, "  predicted = weights + KV(f16) + overhead\n")
	fmt.Fprintf(w, "  KV = 2 * block_count * head_count_kv * head_dim * ctx * 2 B\n")
	fmt.Fprintf(w, "     head_dim from %s; ctx clamped to the model's trained context_length: %v\n",
		rep.Overhead.HeadDim, rep.Overhead.ClampCtx)

	fmt.Fprintf(w, "\nROWS\n")
	fmt.Fprintf(w, "  ERR%%   = against ACTUAL (the device-memory delta) when the whole model sat on the GPU,\n")
	fmt.Fprintf(w, "           otherwise against OLLAMA — the only whole-model number left once layers split to CPU.\n")
	fmt.Fprintf(w, "  ERRps%% = always against OLLAMA (/api/ps size), for comparison. Note that /api/ps size is\n")
	fmt.Fprintf(w, "           Ollama's own ESTIMATE, not a measurement: the same model reports near-identical\n")
	fmt.Fprintf(w, "           figures on Metal and Vulkan. Only ACTUAL is evidence.\n")
	fmt.Fprintf(w, "  CLASS in capitals = excluded from the averages and from the gate.\n\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tCTX\tCLASS\tQUANT\tWEIGHTS\tKV\tOVHD\tPREDICT\tOLLAMA\tVRAM\tACTUAL\tERR%\tERRps%\tSPLIT\tPATH")
	for _, r := range rep.Rows {
		class := r.Class
		if r.Excluded {
			class = strings.ToUpper(r.Class)
		}
		if r.Skipped {
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t-\t-\t-\t-\t-\t-\t-\t-\t-\tSKIPPED\n",
				truncate(r.Model, 34), r.NumCtx, class, r.Quantization, human(r.WeightsBytes))
			continue
		}
		errs := "-"
		if r.ErrKnown {
			errs = fmt.Sprintf("%+.1f", r.ErrPct)
		}
		errPS := "-"
		if r.ErrVsOllamaKnown {
			errPS = fmt.Sprintf("%+.1f", r.ErrPctVsOllama)
		}
		split := "-"
		if r.OllamaReported {
			if r.SplitToCPU {
				split = fmt.Sprintf("yes %.0f%%gpu", r.VRAMFraction*100)
			} else {
				split = "no"
			}
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			truncate(r.Model, 34), r.NumCtx, class, r.Quantization,
			human(r.WeightsBytes), human(r.KVBytes), human(r.OverheadBytes), human(r.PredictedBytes),
			humanOr(r.OllamaSizeBytes, r.OllamaReported), humanOr(r.OllamaSizeVRAMBytes, r.OllamaReported),
			humanOr(r.ActualVRAMDeltaBytes, r.ActualKnown), errs, errPS, split, truncate(r.RuntimePath, 40))
	}
	tw.Flush()

	notes := false
	for _, r := range rep.Rows {
		if len(r.Notes) == 0 && r.SkipReason == "" && r.Error == "" {
			continue
		}
		if !notes {
			fmt.Fprintf(w, "\nNOTES\n")
			notes = true
		}
		fmt.Fprintf(w, "  %s @ %d\n", r.Model, r.NumCtx)
		if r.SkipReason != "" {
			fmt.Fprintf(w, "    skipped: %s\n", r.SkipReason)
		}
		if r.Error != "" {
			fmt.Fprintf(w, "    error: %s\n", r.Error)
		}
		for _, n := range r.Notes {
			fmt.Fprintf(w, "    - %s\n", n)
		}
		if r.RuntimeEvidence != "" {
			fmt.Fprintf(w, "    log: %s\n", r.RuntimeEvidence)
		}
	}
}

// ---------------------------------------------------------------------------
// Summary and the overhead fit
// ---------------------------------------------------------------------------

func printSummary(w io.Writer, reps []Report) {
	type point struct {
		rep     *Report
		row     *Row
		implied float64
		x       float64 // n_batch * embedding_length * 4 bytes
		absErr  float64
	}
	var dense []point
	var excluded, skipped, errored int

	for i := range reps {
		rep := &reps[i]
		for j := range rep.Rows {
			r := &rep.Rows[j]
			switch {
			case r.Skipped:
				skipped++
				continue
			case r.Error != "":
				errored++
				continue
			case r.Excluded:
				excluded++
				continue
			case !r.ErrKnown:
				continue
			}
			batch := rep.Overhead.Batch
			if batch <= 0 {
				batch = 512
			}
			implied := float64(r.MeasuredBytes) - float64(r.WeightsBytes) - float64(r.KVBytes)
			dense = append(dense, point{
				rep:     rep,
				row:     r,
				implied: implied,
				x:       float64(batch) * float64(r.EmbeddingLength) * 4,
				absErr:  math.Abs(r.ErrPct),
			})
		}
	}

	fmt.Fprintf(w, "\nSUMMARY\n")
	if len(reps) > 1 {
		var labels []string
		for i := range reps {
			labels = append(labels, fmt.Sprintf("%s [%s]", reps[i].MachineLabel, reps[i].Ollama.RuntimePath))
		}
		fmt.Fprintf(w, "  machines: %s\n", strings.Join(labels, ", "))
	}
	fmt.Fprintf(w, "  dense rows measured: %d   (excluded MoE/vision/embedding: %d, skipped: %d, errored: %d)\n",
		len(dense), excluded, skipped, errored)

	if len(dense) == 0 {
		classes := map[string]int{}
		for i := range reps {
			for _, r := range reps[i].Rows {
				classes[r.Class]++
			}
		}
		var seen []string
		for k, v := range classes {
			seen = append(seen, fmt.Sprintf("%s (%d)", k, v))
		}
		sort.Strings(seen)
		fmt.Fprintf(w, "\n  ****************************************************************************\n")
		fmt.Fprintf(w, "  *** GATE UNANSWERABLE — not one dense row was measured.                  ***\n")
		fmt.Fprintf(w, "  ****************************************************************************\n")
		fmt.Fprintf(w, "  Everything installed was MoE, vision, an embedding model, or lacked the\n")
		fmt.Fprintf(w, "  metadata the formula needs, so nothing here tests the estimator at all.\n")
		fmt.Fprintf(w, "  Rows by class: %s\n", strings.Join(seen, ", "))
		fmt.Fprintf(w, "  Pull dense, text-only models and run again — vary the parameter count so\n")
		fmt.Fprintf(w, "  embedding_length varies, or the overhead term cannot be fitted either.\n")
		return
	}

	within := 0
	worst := dense[0]
	var sum float64
	errs := make([]float64, 0, len(dense))
	widths := map[int]bool{}
	for _, p := range dense {
		if p.absErr <= 15 {
			within++
		}
		if p.absErr > worst.absErr {
			worst = p
		}
		sum += p.absErr
		errs = append(errs, p.absErr)
		widths[p.row.EmbeddingLength] = true
	}
	fmt.Fprintf(w, "  within 15%%: %d/%d (%.0f%%)   gate needs >= 80%%   -> %s\n",
		within, len(dense), float64(within)/float64(len(dense))*100, verdictFor(within, len(dense)))

	// The gate is per machine, not pooled. A machine that happens to have more
	// models must not carry one that has none.
	if len(reps) > 1 {
		fmt.Fprintf(w, "\n  PER MACHINE (the gate is per machine, not pooled):\n")
		for i := range reps {
			n, ok := 0, 0
			for _, p := range dense {
				if p.rep == &reps[i] {
					n++
					if p.absErr <= 15 {
						ok++
					}
				}
			}
			fmt.Fprintf(w, "    %-24s %d dense rows, %d within 15%%  -> %s\n",
				reps[i].MachineLabel, n, ok, verdictFor(ok, n))
		}
	}

	if len(widths) < 2 {
		fmt.Fprintf(w, "\n  ! every dense row has the same embedding_length. The fixed and graph parts of\n")
		fmt.Fprintf(w, "    the overhead term cannot be told apart from this data — test models of\n")
		fmt.Fprintf(w, "    different widths before trusting any fit.\n")
	}
	fmt.Fprintf(w, "  mean |err|: %.1f%%   median |err|: %.1f%%\n", sum/float64(len(dense)), median(errs))
	fmt.Fprintf(w, "  worst row: %s @ %d on %s — predicted %s vs measured %s (%+.1f%%, %s)\n",
		worst.row.Model, worst.row.NumCtx, worst.rep.MachineLabel,
		human(worst.row.PredictedBytes), human(worst.row.MeasuredBytes), worst.row.ErrPct, worst.row.MeasuredSource)

	implied := make([]float64, 0, len(dense))
	negatives := 0
	for _, p := range dense {
		implied = append(implied, p.implied)
		if p.implied < 0 {
			negatives++
		}
	}
	sort.Float64s(implied)
	fmt.Fprintf(w, "\n  IMPLIED OVERHEAD (measured - weights - KV), the number step 5 should be calibrated on:\n")
	fmt.Fprintf(w, "    min %s   median %s   max %s\n",
		signedHuman(implied[0]), signedHuman(median(implied)), signedHuman(implied[len(implied)-1]))
	if negatives > 0 {
		fmt.Fprintf(w, "    %d row(s) negative: the machine used less than weights + KV. Sliding-window attention, a\n", negatives)
		fmt.Fprintf(w, "    quantized KV cache, or a partial CPU split will each do that — check the notes before fitting.\n")
	}

	// Fit only on rows where the implied overhead is positive. A negative one
	// means the formula's KV term is wrong for that architecture (a sliding
	// window, a quantized cache), not that the overhead is negative, and one
	// such row drags a least-squares line anywhere.
	xy := make([][2]float64, 0, len(dense))
	var fitImplied []float64
	for _, p := range dense {
		if p.implied <= 0 {
			continue
		}
		xy = append(xy, [2]float64{p.x, p.implied})
		fitImplied = append(fitImplied, p.implied)
	}
	fmt.Fprintf(w, "\n  OVERHEAD FIT (over the %d of %d dense rows with a positive implied overhead):\n", len(xy), len(dense))
	if a, b, r2, ok := ols(xy); ok && b > 0 {
		fmt.Fprintf(w, "    overhead ≈ %.0f MiB + %.2f * n_batch * embedding_length * 4 B   (R² = %.2f)\n", a/mib, b, r2)
		fmt.Fprintf(w, "    reproduce with: -overhead-fixed-mib %.0f -overhead-graph-factor %.2f\n", a/mib, b)
	} else if ok {
		fmt.Fprintf(w, "    the graph term is not supported by this data (slope %.2f ≤ 0) — use the flat term below\n", b)
	} else {
		fmt.Fprintf(w, "    too few usable rows to fit\n")
	}
	if len(fitImplied) > 0 {
		fmt.Fprintf(w, "    flat alternative: -overhead-fixed-mib %.0f -overhead-graph-factor 0   (median implied overhead)\n", median(fitImplied)/mib)
	}

	byPath := map[string]int{}
	for _, p := range dense {
		byPath[p.row.RuntimePath]++
	}
	var paths []string
	for k, v := range byPath {
		paths = append(paths, fmt.Sprintf("%s (%d)", k, v))
	}
	sort.Strings(paths)
	fmt.Fprintf(w, "\n  runtime path taken: %s\n", strings.Join(paths, ", "))
}

func ols(xy [][2]float64) (a, b, r2 float64, ok bool) {
	if len(xy) < 3 {
		return 0, 0, 0, false
	}
	var sx, sy, sxx, sxy float64
	n := float64(len(xy))
	for _, p := range xy {
		sx += p[0]
		sy += p[1]
		sxx += p[0] * p[0]
		sxy += p[0] * p[1]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, 0, 0, false
	}
	b = (n*sxy - sx*sy) / den
	a = (sy - b*sx) / n
	meanY := sy / n
	var ssTot, ssRes float64
	for _, p := range xy {
		pred := a + b*p[0]
		ssTot += (p[1] - meanY) * (p[1] - meanY)
		ssRes += (p[1] - pred) * (p[1] - pred)
	}
	if ssTot > 0 {
		r2 = 1 - ssRes/ssTot
	}
	return a, b, r2, true
}

// minGateRows is the fewest dense rows that can support a verdict. The gate is
// "four of every five", which two rows cannot express: one bad row out of two is
// 50%, and two good rows out of two is not evidence of anything. Below this,
// probe0 says INCONCLUSIVE rather than printing a PASS that would be quoted
// later as if it meant something.
const minGateRows = 5

func verdictFor(within, total int) string {
	if total < minGateRows {
		return fmt.Sprintf("INCONCLUSIVE (%d dense rows; need at least %d)", total, minGateRows)
	}
	if float64(within)/float64(total)*100 >= 80 {
		return "PASS"
	}
	return "FAIL"
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}

func signedHuman(f float64) string {
	if f < 0 {
		return "-" + human(uint64(-f))
	}
	return human(uint64(f))
}

// ---------------------------------------------------------------------------
// Refit: combine reports from several machines
// ---------------------------------------------------------------------------

var keyLenNoteRE = regexp.MustCompile(`\.attention\.key_length = (\d+)`)

// recomputeRows re-derives every prediction from the data each row already
// carries, using the current formula flags. It is what makes a formula change
// checkable against reports that are already in hand, without going back to the
// machines for another two hours of model loading.
func recomputeRows(rep *Report, o OverheadModel) {
	rep.Overhead = o
	for i := range rep.Rows {
		r := &rep.Rows[i]
		if r.Skipped || r.BlockCount == 0 || r.HeadDim == 0 {
			continue
		}
		keyLen := r.KeyLength
		if keyLen == 0 { // reports from before key_length was a field
			for _, n := range r.Notes {
				if m := keyLenNoteRE.FindStringSubmatch(n); m != nil {
					if v, err := strconv.Atoi(m[1]); err == nil {
						keyLen = v
					}
				}
			}
		}
		headDim := r.HeadDim
		if *flagHeadDim == "keylen" && keyLen > 0 {
			headDim = keyLen
		}
		ctx := r.NumCtx
		if *flagClampCtx && r.ModelContextLength > 0 && r.NumCtx > r.ModelContextLength {
			ctx = r.ModelContextLength
		}
		r.KeyLength, r.EffectiveHeadDim, r.EffectiveCtx = keyLen, headDim, ctx
		r.KVBytes = 2 * uint64(r.BlockCount) * uint64(r.HeadCountKV) * uint64(headDim) * uint64(ctx) * 2
		r.OverheadBytes = o.bytes(r.EmbeddingLength, r.RuntimePath)
		r.PredictedBytes = r.WeightsBytes + r.KVBytes + r.OverheadBytes
		if r.MeasuredBytes > 0 {
			r.ErrPct = errPct(r.PredictedBytes, r.MeasuredBytes)
			r.ErrKnown = true
		}
		if r.ActualKnown && r.ActualVRAMDeltaBytes > 0 {
			r.ErrPctVsActual = errPct(r.PredictedBytes, r.ActualVRAMDeltaBytes)
		}
		if r.OllamaReported && r.OllamaSizeBytes > 0 {
			r.ErrPctVsOllama = errPct(r.PredictedBytes, r.OllamaSizeBytes)
		}
	}
}

func refit(paths []string) error {
	var reps []Report
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var r Report
		if err := json.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		reps = append(reps, r)
	}
	if len(reps) == 0 {
		return errors.New("no reports given")
	}
	for i := range reps {
		fmt.Printf("%s — %s, %s, Ollama %s, path %s, %d rows, build %s\n",
			reps[i].MachineLabel, reps[i].Hardware.OSVersion, reps[i].Hardware.CPUModel,
			reps[i].Ollama.Version, reps[i].Ollama.RuntimePath, len(reps[i].Rows),
			firstNonEmpty(reps[i].Build, "(pre-stamp)"))
	}
	if *flagRecompute {
		o := newOverheadModel()
		for i := range reps {
			recomputeRows(&reps[i], o)
		}
		fmt.Printf("\nRECOMPUTED from the stored row data, not as each machine recorded it:\n")
		fmt.Printf("  head_dim = %s   clamp_ctx = %v\n  %s\n", *flagHeadDim, *flagClampCtx, o.Formula)
	}
	printSummary(os.Stdout, reps)
	return nil
}

func splitKV(line, sep string) (string, string, bool) {
	i := strings.Index(line, sep)
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+len(sep):]), true
}

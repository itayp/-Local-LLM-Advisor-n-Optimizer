// calibrate turns llama-bench results from a machine in the test fleet into
// the speed model's constants (internal/estimate/config.go): the share of a
// part's memory bandwidth that generation achieves on a runtime path, and
// the ratio of prompt-processing speed to generation speed.
//
// It is the dev-side instrument of build-plan step 5 — never something the
// customer installs or the daemon runs. README.md says how to produce its
// input on each machine.
//
//	go run ./scripts/calibrate -label windows-5070ti bench.json
//	go run ./scripts/calibrate -label macpro-cpu -bandwidth 59.7 cpu.json
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"advisor/internal/estimate"
	"advisor/internal/hardware"
)

// benchRow is one object of `llama-bench -o json`; only the fields used here.
type benchRow struct {
	BuildCommit  string  `json:"build_commit"`
	CPUInfo      string  `json:"cpu_info"`
	GPUInfo      string  `json:"gpu_info"`
	Backends     string  `json:"backends"`
	ModelFile    string  `json:"model_filename"`
	ModelType    string  `json:"model_type"`
	ModelSize    float64 `json:"model_size"`     // bytes
	ModelParams  float64 `json:"model_n_params"` // parameters
	NGPULayers   int     `json:"n_gpu_layers"`
	NPrompt      int     `json:"n_prompt"`
	NGen         int     `json:"n_gen"`
	NDepth       int     `json:"n_depth"`
	TypeK        string  `json:"type_k"`
	FlashAttn    any     `json:"flash_attn"`
	TestTime     string  `json:"test_time"`
	AvgTS        float64 `json:"avg_ts"`
	StddevTS     float64 `json:"stddev_ts"`
	sourceFile   string
	runtimePath  hardware.RuntimePath
	bandwidthGBs [2]float64 // low, high
	bandwidthWhy string
}

// Result is one calibrated model on one machine: what goes into
// scripts/calibrate/results/ and, from there, into config.go's Basis lines.
type Result struct {
	Label        string               `json:"label"`
	CalibratedAt string               `json:"calibrated_at"`
	Path         hardware.RuntimePath `json:"runtime_path"`
	Device       string               `json:"device"`
	Bandwidth    [2]float64           `json:"bandwidth_gbs"` // low, high (equal for a graphics part)
	BandwidthWhy string               `json:"bandwidth_source"`
	Model        string               `json:"model"`
	ModelBytes   float64              `json:"model_bytes"`
	ModelParams  float64              `json:"model_params"`
	ActiveShare  float64              `json:"active_share"` // 1 for a dense model
	GenerationTS float64              `json:"generation_tok_s"`
	PromptTS     float64              `json:"prompt_tok_s,omitempty"`
	// Efficiency is generation tok/s × bytes read per token ÷ bandwidth. For
	// a processor the bandwidth is a range, so this is one too: [against
	// high, against low].
	Efficiency [2]float64 `json:"efficiency"`
	// PromptRatio is prompt tok/s ÷ the generation tok/s the same part would
	// reach on the reference model scaled to this model's parameters
	// (estimate.Config.PromptReferenceBytesPerParam).
	PromptRatio float64  `json:"prompt_ratio,omitempty"`
	Configured  string   `json:"configured_range"`
	Verdict     string   `json:"verdict"`
	LlamaCpp    string   `json:"llama_cpp_build"`
	Inputs      []string `json:"inputs"`
}

func main() {
	label := flag.String("label", "", "a name for this machine, used in the results file (required)")
	pathFlag := flag.String("path", "", "runtime path, when llama-bench's \"backends\" field does not settle it: cuda | metal | rocm | vulkan | cpu")
	bandwidth := flag.Float64("bandwidth", 0, "memory bandwidth in GB/s, when the part is not in data/hardware/gpus.yaml or — for a processor — to state what is actually installed")
	active := flag.Float64("active-share", 1, "share of the parameters used per token, for a mixture-of-experts model (active ÷ total); 1 for a dense model")
	out := flag.String("out", filepath.Join("scripts", "calibrate", "results"), "folder for the results file; \"\" to print only")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: go run ./scripts/calibrate -label NAME [flags] llama-bench.json [more.json]\n\nSee scripts/calibrate/README.md.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if err := run(os.Stdout, *label, *pathFlag, *bandwidth, *active, *out, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "calibrate:", err)
		os.Exit(1)
	}
}

func run(w io.Writer, label, pathFlag string, bandwidth, active float64, outDir string, files []string) error {
	if strings.TrimSpace(label) == "" {
		return errors.New("-label is required (for example -label windows-5070ti)")
	}
	if len(files) == 0 {
		return errors.New("give at least one llama-bench JSON file (llama-bench -o json > bench.json)")
	}
	if active <= 0 || active > 1 {
		return errors.New("-active-share must be in (0, 1]")
	}
	est, err := estimate.New()
	if err != nil {
		return err
	}

	var rows []benchRow
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var batch []benchRow
		if err := json.Unmarshal(b, &batch); err != nil {
			return fmt.Errorf("%s: not llama-bench's JSON output (run it with -o json): %w", f, err)
		}
		for i := range batch {
			batch[i].sourceFile = filepath.Base(f)
		}
		rows = append(rows, batch...)
	}
	for i := range rows {
		if err := settle(est, &rows[i], pathFlag, bandwidth); err != nil {
			return err
		}
	}

	results := derive(est, label, rows, active)
	if len(results) == 0 {
		return errors.New("no generation test (n_gen > 0, n_prompt = 0) found in the input; run llama-bench with -n 128")
	}
	report(w, results)

	if outDir == "" {
		return nil
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	name := filepath.Join(outDir, fmt.Sprintf("%s-%s.json", safe(label), time.Now().UTC().Format("20060102")))
	b, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(name, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nwrote %s — commit it, and quote it in the Basis of the range it supports or moves (internal/estimate/config.go).\n", name)
	return nil
}

// settle decides a row's runtime path and the bandwidth to score it against.
func settle(est *estimate.Estimator, r *benchRow, pathFlag string, bandwidth float64) error {
	switch {
	case pathFlag != "":
		r.runtimePath = hardware.RuntimePath(strings.ToLower(pathFlag))
	case r.NGPULayers == 0:
		r.runtimePath = hardware.PathCPU
	default:
		b := strings.ToLower(r.Backends)
		switch {
		case strings.Contains(b, "cuda"):
			r.runtimePath = hardware.PathCUDA
		case strings.Contains(b, "metal"), strings.Contains(b, "mtl"):
			r.runtimePath = hardware.PathMetal
		case strings.Contains(b, "rocm"), strings.Contains(b, "hip"):
			r.runtimePath = hardware.PathROCm
		case strings.Contains(b, "vulkan"):
			r.runtimePath = hardware.PathVulkan
		default:
			return fmt.Errorf("%s: cannot tell the runtime path from backends=%q; pass -path", r.sourceFile, r.Backends)
		}
	}
	if _, ok := est.Config.Paths[r.runtimePath]; !ok {
		return fmt.Errorf("runtime path %q is not one the estimator knows", r.runtimePath)
	}
	if bandwidth > 0 {
		r.bandwidthGBs, r.bandwidthWhy = [2]float64{bandwidth, bandwidth}, "stated with -bandwidth"
		return nil
	}
	if r.runtimePath == hardware.PathCPU {
		spec, ok := est.Devices.SystemMemory(hardware.CPU{Model: r.CPUInfo})
		if !ok {
			return fmt.Errorf("processor %q is not in data/hardware/gpus.yaml; add a system_memory row, or pass -bandwidth with what is installed (channels × MT/s × 8 ÷ 1000)", r.CPUInfo)
		}
		r.bandwidthGBs = [2]float64{spec.LowGBs, spec.HighGBs}
		r.bandwidthWhy = "gpus.yaml " + spec.RowID + " (" + spec.Memory + "); pass -bandwidth for what is actually installed"
		return nil
	}
	g := hardware.GPU{Vendor: vendorOf(r.GPUInfo), Name: r.GPUInfo}
	spec, ok := est.Devices.GPU(g)
	if !ok {
		return fmt.Errorf("graphics part %q is not in data/hardware/gpus.yaml (or its row needs the card's memory size to choose a variant); add the row, or pass -bandwidth", r.GPUInfo)
	}
	r.bandwidthGBs, r.bandwidthWhy = [2]float64{spec.BandwidthGBs, spec.BandwidthGBs}, "gpus.yaml "+spec.RowID
	return nil
}

var appleChip = regexp.MustCompile(`(?i)\bApple\b|\bM[1-9]\b`)

func vendorOf(name string) hardware.Vendor {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "nvidia"), strings.Contains(n, "geforce"), strings.Contains(n, "rtx"), strings.Contains(n, "gtx"):
		return hardware.VendorNVIDIA
	case strings.Contains(n, "radeon"), strings.Contains(n, "amd"), strings.Contains(n, "firepro"):
		return hardware.VendorAMD
	case strings.Contains(n, "intel"), strings.Contains(n, "arc"):
		return hardware.VendorIntel
	case appleChip.MatchString(name):
		return hardware.VendorApple
	}
	return hardware.VendorUnknown
}

// derive pairs each model's generation test with its prompt test (same
// file, same path, empty context) and computes the two factors.
func derive(est *estimate.Estimator, label string, rows []benchRow, active float64) []Result {
	type key struct {
		model string
		path  hardware.RuntimePath
	}
	gen, prompt := map[key]benchRow{}, map[key]benchRow{}
	for _, r := range rows {
		if r.NDepth != 0 || r.ModelSize <= 0 || r.AvgTS <= 0 {
			continue
		}
		k := key{r.ModelFile, r.runtimePath}
		switch {
		case r.NGen > 0 && r.NPrompt == 0:
			gen[k] = r
		case r.NPrompt > 0 && r.NGen == 0:
			prompt[k] = r
		}
	}
	var out []Result
	for k, g := range gen {
		bytesGB := g.ModelSize * active / 1e9
		res := Result{
			Label: label, CalibratedAt: time.Now().UTC().Format(time.RFC3339), Path: k.path,
			Device: strings.TrimSpace(g.GPUInfo), Bandwidth: g.bandwidthGBs, BandwidthWhy: g.bandwidthWhy,
			Model: filepath.Base(g.ModelFile) + " (" + g.ModelType + ")", ModelBytes: g.ModelSize, ModelParams: g.ModelParams,
			ActiveShare: active, GenerationTS: g.AvgTS, LlamaCpp: g.BuildCommit, Inputs: []string{g.sourceFile},
		}
		if k.path == hardware.PathCPU {
			res.Device = strings.TrimSpace(g.CPUInfo)
		}
		res.Efficiency = [2]float64{g.AvgTS * bytesGB / g.bandwidthGBs[1], g.AvgTS * bytesGB / g.bandwidthGBs[0]}

		ps := est.Config.Paths[k.path]
		if k.path == hardware.PathVulkan {
			if v, ok := est.Config.VulkanByVendor[vendorOf(g.GPUInfo)]; ok {
				ps = v
			}
		}
		if spec, ok := est.Devices.GPU(hardware.GPU{Vendor: vendorOf(g.GPUInfo), Name: g.GPUInfo}); ok && k.path != hardware.PathCPU && spec.Efficiency != nil {
			ps.Efficiency = *spec.Efficiency
		}
		res.Configured = fmt.Sprintf("efficiency %.2f–%.2f, prompt ratio %.0f–%.0f", ps.Efficiency.Low, ps.Efficiency.High, ps.PromptRatio.Low, ps.PromptRatio.High)
		lo, hi := res.Efficiency[0], res.Efficiency[1]
		switch {
		case active < 1:
			moe := est.Config.MoEGeneration
			res.Verdict = fmt.Sprintf("mixture-of-experts: compare %.2f–%.2f with the path's range × MoEGeneration (%.2f–%.2f)", lo, hi, ps.Efficiency.Low*moe.Low, ps.Efficiency.High*moe.High)
		case hi < ps.Efficiency.Low:
			res.Verdict = "BELOW the configured range: the advisor over-promises on this part — lower Efficiency.Low, or give the part an efficiency override in gpus.yaml"
		case lo > ps.Efficiency.High:
			res.Verdict = "ABOVE the configured range: the advisor under-promises on this part — raise Efficiency.High, or give the part an override"
		default:
			res.Verdict = "inside the configured range"
		}

		if p, ok := prompt[k]; ok && g.ModelParams > 0 {
			res.PromptTS = p.AvgTS
			refGB := g.ModelParams * active * est.Config.PromptReferenceBytesPerParam / 1e9
			// prompt = ratio × (bandwidth × efficiency ÷ reference bytes), and
			// bandwidth × efficiency = generation × bytes read per token.
			res.PromptRatio = p.AvgTS * refGB / (g.AvgTS * bytesGB)
			if active == 1 && (res.PromptRatio < ps.PromptRatio.Low || res.PromptRatio > ps.PromptRatio.High) {
				res.Verdict += fmt.Sprintf("; prompt ratio %.1f is OUTSIDE %.0f–%.0f", res.PromptRatio, ps.PromptRatio.Low, ps.PromptRatio.High)
			}
			if p.sourceFile != g.sourceFile {
				res.Inputs = append(res.Inputs, p.sourceFile)
			}
		}
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Model < out[j].Model
	})
	return out
}

func report(w io.Writer, results []Result) {
	for _, r := range results {
		fmt.Fprintf(w, "\n%s — %s on %s\n", r.Label, r.Path, r.Device)
		fmt.Fprintf(w, "  model        %s, %.2f GB, %.2f B parameters", r.Model, r.ModelBytes/1e9, r.ModelParams/1e9)
		if r.ActiveShare < 1 {
			fmt.Fprintf(w, " (%.0f%% used per token)", 100*r.ActiveShare)
		}
		fmt.Fprintf(w, "\n  bandwidth    %s GB/s — %s\n", span(r.Bandwidth[0], r.Bandwidth[1], "%.1f"), r.BandwidthWhy)
		fmt.Fprintf(w, "  generation   %.2f tok/s  →  efficiency %s\n", r.GenerationTS, span(r.Efficiency[0], r.Efficiency[1], "%.2f"))
		if r.PromptTS > 0 {
			fmt.Fprintf(w, "  prompt       %.1f tok/s  →  prompt ratio %.1f\n", r.PromptTS, r.PromptRatio)
		}
		fmt.Fprintf(w, "  configured   %s\n  verdict      %s\n", r.Configured, r.Verdict)
	}
}

func span(a, b float64, format string) string {
	if a == b {
		return fmt.Sprintf(format, a)
	}
	return fmt.Sprintf(format+"–"+format, a, b)
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safe(s string) string { return strings.Trim(unsafeChars.ReplaceAllString(s, "-"), "-") }

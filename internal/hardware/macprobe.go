package hardware

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The macOS path: sysctl and sw_vers for the processor, memory and OS;
// system_profiler for the model, form factor and graphics; Metal (asked
// through osascript) for how much memory the GPU may use.

// darwinSysctlKeys are read in one sysctl call. On an Intel Mac the
// iogpu key does not exist; sysctl says so on stderr and prints the rest.
var darwinSysctlKeys = []string{
	"hw.memsize",
	"hw.physicalcpu",
	"hw.logicalcpu",
	"machdep.cpu.brand_string",
	"hw.optional.arm64",      // 1 on Apple Silicon, also inside Rosetta
	"sysctl.proc_translated", // 1 when this process runs under Rosetta
	"iogpu.wired_limit_mb",   // Apple Silicon: the GPU wired-memory cap, 0 = macOS default
}

// parseSysctl reads `sysctl name…` output: "name: value" per line.
func parseSysctl(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return m
}

// swVers is `sw_vers` output.
type swVers struct{ name, version, build string }

func parseSwVers(out string) swVers {
	var s swVers
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "ProductName":
			s.name = v
		case "ProductVersion":
			s.version = v
		case "BuildVersion":
			s.build = v
		}
	}
	return s
}

// systemProfiler is the part of `system_profiler -json SPHardwareDataType
// SPDisplaysDataType` detection reads. Only these fields are kept; the rest
// (serial number, hardware UUID) is never stored anywhere.
type systemProfiler struct {
	Hardware []struct {
		MachineName    string `json:"machine_name"`    // "MacBook Pro", "Mac mini", "iMac", "Mac Studio"
		MachineModel   string `json:"machine_model"`   // "MacBookPro18,3", "Mac14,2"
		ChipType       string `json:"chip_type"`       // "Apple M1 Pro" (Apple Silicon)
		CPUType        string `json:"cpu_type"`        // "8-Core Intel Core i9" (Intel)
		PhysicalMemory string `json:"physical_memory"` // "16 GB"
	} `json:"SPHardwareDataType"`
	Displays []map[string]any `json:"SPDisplaysDataType"`
}

func parseSystemProfiler(out string) (systemProfiler, error) {
	var sp systemProfiler
	if err := json.Unmarshal([]byte(out), &sp); err != nil {
		return sp, fmt.Errorf("system_profiler output is not the expected JSON: %w", err)
	}
	return sp, nil
}

// darwinGPUs turns SPDisplaysDataType entries into GPUs.
func darwinGPUs(sp systemProfiler, osVersion string) []GPU {
	var gpus []GPU
	str := func(d map[string]any, k string) string {
		s, _ := d[k].(string)
		return strings.TrimSpace(s)
	}
	for _, d := range sp.Displays {
		name := str(d, "sppci_model")
		if name == "" {
			name = str(d, "_name")
		}
		if name == "" {
			name = Unknown
		}
		g := GPU{Name: name, DriverVersion: Unknown, ExpectedBackend: PathUnknown}
		if osVersion != Unknown && osVersion != "" {
			g.DriverVersion = "part of " + osVersion // Apple ships every Mac GPU driver with the OS
		}

		vendorRaw := strings.ToLower(str(d, "spdisplays_vendor"))
		switch {
		case strings.Contains(vendorRaw, "apple"):
			g.Vendor = VendorApple
		case strings.Contains(vendorRaw, "amd"), strings.Contains(vendorRaw, "ati"):
			g.Vendor = VendorAMD
		case strings.Contains(vendorRaw, "intel"):
			g.Vendor = VendorIntel
		case strings.Contains(vendorRaw, "nvidia"):
			g.Vendor = VendorNVIDIA
		default:
			g.Vendor = vendorFromName(name)
		}
		vid := hexID(str(d, "spdisplays_vendor-id"))
		if v := vendorFromPCI(vid); v != VendorUnknown {
			g.Vendor = v
		} else {
			vid = pciVendorID(g.Vendor)
		}
		if did := hexID(str(d, "spdisplays_device-id")); did != "" && vid != "" {
			g.PCIID = vid + ":" + did
		}

		bus := str(d, "sppci_bus")
		switch {
		case g.Vendor == VendorApple:
			g.IsIntegrated, g.IntegratedKnown = true, true
		case bus == "spdisplays_builtin":
			g.IsIntegrated, g.IntegratedKnown = true, true
		case bus != "":
			g.IsIntegrated, g.IntegratedKnown = false, true // PCIe or Thunderbolt
		}

		switch {
		case g.Vendor == VendorApple:
			g.VRAMSource = "not applicable: Apple Silicon graphics use the Mac's unified memory"
		default:
			g.VRAMSource = "system_profiler reported no graphics memory"
			for _, key := range []string{"spdisplays_vram", "sppci_vram", "spdisplays_vram_shared"} {
				if n, ok := parseSizeString(str(d, key)); ok && n > 0 {
					g.VRAMBytes, g.VRAMKnown = n, true
					g.VRAMSource = "system_profiler " + key
					if key == "spdisplays_vram_shared" {
						g.IsIntegrated, g.IntegratedKnown = true, true
					}
					break
				}
			}
		}
		if cores := str(d, "sppci_cores"); cores != "" && g.Vendor == VendorApple {
			g.Note = cores + "-core GPU"
		}
		gpus = append(gpus, g)
	}
	return gpus
}

func hexID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "0x")
	if len(s) == 0 || len(s) > 4 {
		return ""
	}
	if _, err := strconv.ParseUint(s, 16, 16); err != nil {
		return ""
	}
	return strings.Repeat("0", 4-len(s)) + s
}

var sizeRE = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*(kb|mb|gb|tb|b)?\s*$`)

// parseSizeString reads system_profiler sizes ("8 GB", "1536 MB"): binary
// units, as macOS means them for memory.
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

// metalProbeScript asks Metal, from JavaScript for Automation, for the
// system default GPU's recommendedMaxWorkingSetSize — the same call Ollama
// makes (discover/gpu_info_darwin.m) to decide how much a model may use.
// osascript ships with every Mac, so this needs no compiler and no cgo.
const metalProbeScript = `ObjC.import('Metal'); ` +
	`if (typeof $.MTLCreateSystemDefaultDevice !== 'function') { ObjC.bindFunction('MTLCreateSystemDefaultDevice', ['id', []]); } ` +
	`var d = $.MTLCreateSystemDefaultDevice(); var o = {}; ` +
	`if (d && !d.isNil()) { o.name = ObjC.unwrap(d.name); o.recommended_max_working_set_size = Number(d.recommendedMaxWorkingSetSize); o.has_unified_memory = d.hasUnifiedMemory ? true : false; } ` +
	`JSON.stringify(o);`

type metalProbe struct {
	Name                         string  `json:"name"`
	RecommendedMaxWorkingSetSize float64 `json:"recommended_max_working_set_size"`
	HasUnifiedMemory             bool    `json:"has_unified_memory"`
}

func parseMetalProbe(out string) (metalProbe, error) {
	var m metalProbe
	s := strings.TrimSpace(out)
	i, j := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}')
	if i < 0 || j < i {
		return m, fmt.Errorf("Metal query printed no JSON: %q", truncate(s, 120))
	}
	if err := json.Unmarshal([]byte(s[i:j+1]), &m); err != nil {
		return m, fmt.Errorf("Metal query output not understood: %w", err)
	}
	return m, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func detectDarwin(ctx context.Context, e env, p *Profile) {
	// The two slow calls run side by side.
	type spResult struct {
		sp  systemProfiler
		err error
	}
	spCh := make(chan spResult, 1)
	go func() {
		out, err := e.run(ctx, timeoutProfiler, "system_profiler", "-json", "SPHardwareDataType", "SPDisplaysDataType")
		if err != nil && strings.TrimSpace(out) == "" {
			spCh <- spResult{err: err}
			return
		}
		sp, perr := parseSystemProfiler(out)
		spCh <- spResult{sp, perr}
	}()

	sysctlOut, sysctlErr := e.run(ctx, timeoutQuick, "sysctl", darwinSysctlKeys...)
	sc := parseSysctl(sysctlOut)
	if len(sc) == 0 {
		p.problem("sysctl did not answer: %v", sysctlErr)
	}
	appleSilicon := sc["hw.optional.arm64"] == "1" || p.Arch == "arm64"

	type metalResult struct {
		m   metalProbe
		err error
		ran bool
	}
	metalCh := make(chan metalResult, 1)
	go func() {
		if !appleSilicon {
			metalCh <- metalResult{}
			return
		}
		out, err := e.run(ctx, timeoutQuick, "osascript", "-l", "JavaScript", "-e", metalProbeScript)
		if err != nil && strings.TrimSpace(out) == "" {
			metalCh <- metalResult{err: err, ran: true}
			return
		}
		m, perr := parseMetalProbe(out)
		metalCh <- metalResult{m, perr, true}
	}()

	if sw, err := e.run(ctx, timeoutQuick, "sw_vers"); err == nil {
		v := parseSwVers(sw)
		if v.name != "" && v.version != "" {
			p.OSVersion = v.name + " " + v.version
			if v.build != "" {
				p.OSVersion += " (" + v.build + ")"
			}
			p.macOSVersion = v.version
		}
	} else {
		p.problem("sw_vers did not answer: %v", err)
	}

	if s := cleanName(sc["machdep.cpu.brand_string"]); s != "" {
		p.CPU.Model = s
	}
	if n, err := strconv.Atoi(sc["hw.physicalcpu"]); err == nil && n > 0 {
		p.CPU.CoresPhysical = n
	}
	if n, err := strconv.Atoi(sc["hw.logicalcpu"]); err == nil && n > 0 {
		p.CPU.CoresLogical = n
	}
	if n, err := strconv.ParseUint(sc["hw.memsize"], 10, 64); err == nil && n > 0 {
		p.RAMBytes, p.RAMKnown = n, true
	}
	if appleSilicon {
		p.UnifiedMemory = true
		if p.Arch != "arm64" { // the Intel build under Rosetta
			p.Arch = "arm64"
			p.note("The Intel version of the advisor is running on this Apple Silicon Mac through Rosetta. The Apple Silicon version reads this Mac more directly.")
		}
		p.CPU.HasAVX2, p.CPU.HasAVX512, p.CPU.VectorKnown = false, false, true
	}

	spr := <-spCh
	if spr.err != nil {
		p.problem("system_profiler did not answer: %v", spr.err)
	} else {
		if len(spr.sp.Hardware) > 0 {
			hw := spr.sp.Hardware[0]
			if p.CPU.Model == Unknown {
				if c := cleanName(firstNonEmpty(hw.ChipType, hw.CPUType)); c != "" {
					p.CPU.Model = c
				}
			}
			switch mn := strings.ToLower(hw.MachineName); {
			case strings.Contains(mn, "macbook"):
				p.IsLaptop, p.LaptopKnown = true, true
			case strings.Contains(mn, "imac"), strings.Contains(mn, "mac mini"), strings.Contains(mn, "mac studio"), strings.Contains(mn, "mac pro"):
				p.IsLaptop, p.LaptopKnown = false, true
			}
		}
		p.GPUs = darwinGPUs(spr.sp, p.OSVersion)
		// Every Mac lists its graphics, with or without a display attached;
		// an empty list is a failed query, not "no GPU".
		p.gpuListRead = len(p.GPUs) > 0
	}

	// How much of unified memory the GPU may use. macOS caps the GPU's
	// working set below total RAM, and the cap is the OS's to choose:
	//
	//  1. iogpu.wired_limit_mb > 0 — someone set the cap on this Mac
	//     (`sudo sysctl iogpu.wired_limit_mb=N`); the OS enforces exactly it.
	//  2. Otherwise (0, the default) the platform default for this RAM size
	//     applies, and the one place the OS states it is Metal's
	//     recommendedMaxWorkingSetSize — the value Ollama itself schedules
	//     against. It is read, not computed: the often-quoted rule (two-thirds
	//     of RAM up to 36 GB, three-quarters above) does not hold on the test
	//     fleet's M1 Pro under macOS 26.6.2, where Ollama v0.34.2 logs Metal
	//     total="11.8 GiB" for 16 GiB of RAM (0.74), 2026-09-18. A table
	//     would be wrong on the next macOS release; the OS will not be.
	//  3. Neither readable: unknown, with the reason in Problems.
	if appleSilicon {
		wired, wiredErr := strconv.ParseUint(sc["iogpu.wired_limit_mb"], 10, 64)
		mr := <-metalCh
		switch {
		case wiredErr == nil && wired > 0:
			p.GPUUsableBytes, p.GPUUsableKnown = wired*mib, true
			p.GPUUsableSource = fmt.Sprintf("iogpu.wired_limit_mb = %d, set on this Mac", wired)
		case mr.err == nil && mr.ran && mr.m.RecommendedMaxWorkingSetSize > 0:
			p.GPUUsableBytes, p.GPUUsableKnown = uint64(mr.m.RecommendedMaxWorkingSetSize), true
			p.GPUUsableSource = "macOS's default for this Mac, as Metal reports it (recommendedMaxWorkingSetSize); iogpu.wired_limit_mb is not set"
		default:
			p.GPUUsableSource = "unknown: iogpu.wired_limit_mb is not set and Metal could not be asked"
			if mr.err != nil {
				p.problem("could not ask Metal how much memory the graphics may use: %v", mr.err)
			}
		}
	} else {
		<-metalCh
	}

	p.Storage.ModelsDir, p.Storage.ModelsDirSource = darwinModelsDir(ctx, e)
}

// darwinModelsDir: OLLAMA_MODELS from this process, then from launchd (where
// Ollama's FAQ tells Mac users to set it: `launchctl setenv`), then the
// documented default.
func darwinModelsDir(ctx context.Context, e env) (dir, source string) {
	if v := strings.TrimSpace(e.getenv("OLLAMA_MODELS")); v != "" {
		return v, "OLLAMA_MODELS"
	}
	if out, err := e.run(ctx, timeoutQuick, "launchctl", "getenv", "OLLAMA_MODELS"); err == nil {
		if v := strings.TrimSpace(out); v != "" {
			return v, "OLLAMA_MODELS (set with launchctl)"
		}
	}
	home, err := e.homeDir()
	if err != nil || home == "" {
		return Unknown, "the home folder could not be found"
	}
	return strings.TrimRight(home, "/") + "/.ollama/models", "Ollama's default on macOS"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

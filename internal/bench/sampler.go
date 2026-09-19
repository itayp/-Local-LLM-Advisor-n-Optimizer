package bench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"advisor/internal/backend"
	"advisor/internal/hardware"
)

// The resource sampler (build-plan step 6, item 3; ARCHITECTURE.md D-47):
// once a second while a run lasts, every probe this machine has reads its
// tool. What it reads, and why that and not something else:
//
//	NVIDIA (Windows, Linux)   nvidia-smi --query-gpu: utilisation, memory used,
//	                          temperature, power — per GPU.
//	AMD on Linux              the amdgpu driver's sysfs counters, which rocm-smi
//	                          and amd-smi themselves read: mem_info_vram_used
//	                          and mem_info_gtt_used (step 0 found part of a
//	                          Vulkan model in GTT, finding 6), gpu_busy_percent,
//	                          hwmon temperature and power. No tool to install,
//	                          no output format to guess, no root.
//	Apple Silicon             vm_stat: wired memory — Metal's buffers are
//	                          wired, and step 0 found the wired delta tracks a
//	                          model where ioreg's figure does not (finding 6) —
//	                          and memory in use; ioreg's IOAccelerator
//	                          "Device Utilization %" for the GPU. Temperature
//	                          and power need powermetrics, which needs root:
//	                          not read.
//	system memory             /proc/meminfo on Linux, vm_stat on macOS,
//	                          GlobalMemoryStatusEx on Windows.
//	everywhere                the runtime's own list of loaded models (Ollama's
//	                          /api/ps size_vram): its estimate, kept as context
//	                          (D-20, finding 4), and the way to notice another
//	                          model loading or leaving during the run.
//
// Where no tool exists — AMD and Intel cards on Windows, Intel on Linux, an
// Intel Mac's graphics — nothing is sampled for the graphics device, and the
// run says so in words (SamplerNote) rather than showing zero.
//
// Everything a probe runs goes through sysEnv, and the tests answer from
// fixture files in the tools' real formats (testdata/sampler/).

// sysEnv is everything the sampler reads from the machine.
type sysEnv interface {
	goos() string
	goarch() string
	// run executes a program and returns its standard output.
	run(ctx context.Context, name string, args ...string) (string, error)
	lookPath(name string) (string, error)
	readFile(path string) ([]byte, error)
	glob(pattern string) ([]string, error)
	// memory is the system memory's total and available bytes where the OS
	// answers with a call rather than a file or a tool (Windows).
	memory() (total, available uint64, ok bool)
}

type realSysEnv struct{}

func (realSysEnv) goos() string                      { return runtime.GOOS }
func (realSysEnv) goarch() string                    { return runtime.GOARCH }
func (realSysEnv) lookPath(n string) (string, error) { return exec.LookPath(n) }
func (realSysEnv) readFile(p string) ([]byte, error) { return os.ReadFile(p) }
func (realSysEnv) glob(p string) ([]string, error)   { return filepath.Glob(p) }
func (realSysEnv) memory() (uint64, uint64, bool)    { return systemMemory() }
func (realSysEnv) run(ctx context.Context, name string, args ...string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	hideWindow(cmd)
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s: %w", name, err)
	}
	return out.String(), nil
}

// reading is one probe's reading of one device.
type reading struct {
	device   string
	gpuUtil  *float64
	vramUsed *uint64
	ramUsed  *uint64
	tempC    *float64
	powerW   *float64
	models   []string // api/ps: the models loaded, by name
}

// probe reads one tool.
type probe interface {
	tool() string
	read(ctx context.Context) ([]reading, error)
}

// ---- NVIDIA -------------------------------------------------------------------

type nvidiaProbe struct {
	env  sysEnv
	path string
}

func (nvidiaProbe) tool() string { return "nvidia-smi" }

var nvidiaSampleArgs = []string{"--query-gpu=index,utilization.gpu,memory.used,temperature.gpu,power.draw", "--format=csv,noheader,nounits"}

func (p nvidiaProbe) read(ctx context.Context) ([]reading, error) {
	out, err := p.env.run(ctx, p.path, nvidiaSampleArgs...)
	if err != nil {
		return nil, err
	}
	return parseNvidiaSample(out)
}

// parseNvidiaSample reads one line per GPU: "0, 35, 5432, 52, 120.45" —
// memory in MiB, power in W; "[N/A]" or "[Not Supported]" where the driver
// has no value (power on many laptops).
func parseNvidiaSample(out string) ([]reading, error) {
	var rs []reading
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, ",")
		if len(f) != 5 {
			return nil, fmt.Errorf("nvidia-smi: expected 5 fields, got %d in %q", len(f), line)
		}
		r := reading{device: strings.TrimSpace(f[0]), gpuUtil: num(f[1]), tempC: num(f[3]), powerW: num(f[4])}
		if mib := num(f[2]); mib != nil {
			b := uint64(*mib * (1 << 20))
			r.vramUsed = &b
		}
		rs = append(rs, r)
	}
	if len(rs) == 0 {
		return nil, errors.New("nvidia-smi: no GPU in the answer")
	}
	return rs, nil
}

// num parses a tool's number, or nil for "[N/A]", "[Not Supported]" and
// anything else that is not one.
func num(s string) *float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// ---- AMD on Linux (amdgpu sysfs) ------------------------------------------------

type amdSysfsProbe struct {
	env   sysEnv
	cards []string // /sys/class/drm/cardN
}

func (amdSysfsProbe) tool() string { return "sysfs" }

var drmCardRE = regexp.MustCompile(`^card\d+$`)

// amdCards lists the DRM cards whose driver exposes a VRAM counter. sysfs
// paths are slash-separated whatever OS the tests run on: package path, not
// filepath.
func amdCards(env sysEnv) []string {
	matches, _ := env.glob("/sys/class/drm/card*/device/mem_info_vram_used")
	var cards []string
	for _, m := range matches {
		card := path.Dir(path.Dir(m))
		if drmCardRE.MatchString(path.Base(card)) {
			cards = append(cards, card)
		}
	}
	sort.Strings(cards)
	return cards
}

func (p amdSysfsProbe) read(context.Context) ([]reading, error) {
	var rs []reading
	for _, card := range p.cards {
		name := path.Base(card)
		dev := card + "/device/"
		vram, ok := p.uint(dev + "mem_info_vram_used")
		if !ok {
			continue
		}
		r := reading{device: name, vramUsed: &vram}
		if busy, ok := p.uint(dev + "gpu_busy_percent"); ok {
			v := float64(busy)
			r.gpuUtil = &v
		}
		if hw, _ := p.env.glob(dev + "hwmon/hwmon*"); len(hw) > 0 {
			sort.Strings(hw)
			if mc, ok := p.uint(hw[0] + "/temp1_input"); ok {
				v := float64(mc) / 1000
				r.tempC = &v
			}
			for _, f := range []string{"power1_average", "power1_input"} {
				if uw, ok := p.uint(hw[0] + "/" + f); ok {
					v := float64(uw) / 1e6
					r.powerW = &v
					break
				}
			}
		}
		rs = append(rs, r)
		// Graphics memory the driver maps from system memory (GTT) is part of
		// what a model can take (D-20, finding 6): a device of its own, so the
		// rise over the baseline counts it.
		if gtt, ok := p.uint(dev + "mem_info_gtt_used"); ok {
			rs = append(rs, reading{device: name + " gtt", vramUsed: &gtt})
		}
	}
	if len(rs) == 0 {
		return nil, errors.New("sysfs: no card answered")
	}
	return rs, nil
}

func (p amdSysfsProbe) uint(path string) (uint64, bool) {
	b, err := p.env.readFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return v, err == nil
}

// ---- macOS ------------------------------------------------------------------------

type vmStatProbe struct {
	env sysEnv
	// wired: report wired memory as the graphics memory (Apple Silicon).
	wired bool
}

func (vmStatProbe) tool() string { return "vm_stat" }

var (
	vmStatPageRE = regexp.MustCompile(`page size of (\d+) bytes`)
	vmStatLineRE = regexp.MustCompile(`(?m)^"?([A-Za-z -]+?)"?:\s+(\d+)\.?\s*$`)
)

func (p vmStatProbe) read(ctx context.Context) ([]reading, error) {
	out, err := p.env.run(ctx, "vm_stat")
	if err != nil {
		return nil, err
	}
	return parseVMStat(out, p.wired)
}

// parseVMStat reads vm_stat's pages: wired ("Pages wired down"), and memory
// in use as Activity Monitor adds it up — active, wired and what the
// compressor occupies.
func parseVMStat(out string, wired bool) ([]reading, error) {
	m := vmStatPageRE.FindStringSubmatch(out)
	if m == nil {
		return nil, errors.New("vm_stat: no page size")
	}
	page, _ := strconv.ParseUint(m[1], 10, 64)
	pages := map[string]uint64{}
	for _, l := range vmStatLineRE.FindAllStringSubmatch(out, -1) {
		v, _ := strconv.ParseUint(l[2], 10, 64)
		pages[strings.TrimSpace(l[1])] = v
	}
	w, ok1 := pages["Pages wired down"]
	a, ok2 := pages["Pages active"]
	c := pages["Pages occupied by compressor"]
	if !ok1 || !ok2 {
		return nil, errors.New("vm_stat: no wired or active pages")
	}
	used := (a + w + c) * page
	r := reading{ramUsed: &used}
	if wired {
		wb := w * page
		r.vramUsed = &wb
	}
	return []reading{r}, nil
}

type ioregProbe struct{ env sysEnv }

func (ioregProbe) tool() string { return "ioreg" }

var ioregUtilRE = regexp.MustCompile(`"Device Utilization %"\s*=\s*(\d+)`)

func (p ioregProbe) read(ctx context.Context) ([]reading, error) {
	out, err := p.env.run(ctx, "ioreg", "-r", "-d", "1", "-w", "0", "-c", "IOAccelerator")
	if err != nil {
		return nil, err
	}
	return parseIoreg(out)
}

// parseIoreg reads the GPU's utilisation from its IOAccelerator
// PerformanceStatistics (the figure Activity Monitor's GPU history shows).
func parseIoreg(out string) ([]reading, error) {
	best := -1.0
	for _, m := range ioregUtilRE.FindAllStringSubmatch(out, -1) {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil && v > best {
			best = v
		}
	}
	if best < 0 {
		return nil, errors.New("ioreg: no Device Utilization %")
	}
	return []reading{{device: "gpu", gpuUtil: &best}}, nil
}

// ---- system memory elsewhere ----------------------------------------------------

type meminfoProbe struct{ env sysEnv }

func (meminfoProbe) tool() string { return "meminfo" }

func (p meminfoProbe) read(context.Context) ([]reading, error) {
	b, err := p.env.readFile("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	return parseMeminfo(string(b))
}

// parseMeminfo: in use = MemTotal − MemAvailable (kB). A model the runtime
// maps from its file sits in the page cache, which Linux counts as
// available — why this is reported as the system's memory in use, never as
// what a model took.
func parseMeminfo(s string) ([]reading, error) {
	kv := map[string]uint64{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.ParseUint(f[0], 10, 64); err == nil {
			kv[strings.TrimSpace(k)] = n * 1024
		}
	}
	total, ok1 := kv["MemTotal"]
	avail, ok2 := kv["MemAvailable"]
	if !ok1 || !ok2 || avail > total {
		return nil, errors.New("meminfo: no MemTotal or MemAvailable")
	}
	used := total - avail
	return []reading{{ramUsed: &used}}, nil
}

type osMemoryProbe struct{ env sysEnv }

func (osMemoryProbe) tool() string { return "os" }

func (p osMemoryProbe) read(context.Context) ([]reading, error) {
	total, avail, ok := p.env.memory()
	if !ok || avail > total {
		return nil, errors.New("the operating system did not say how much memory is in use")
	}
	used := total - avail
	return []reading{{ramUsed: &used}}, nil
}

// ---- the runtime's own account --------------------------------------------------

type psProbe struct{ b backend.Backend }

func (psProbe) tool() string { return "api/ps" }

func (p psProbe) read(ctx context.Context) ([]reading, error) {
	loaded, err := p.b.Running(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(loaded))
	var rs []reading
	for _, l := range loaded {
		names = append(names, l.Name)
	}
	for _, l := range loaded {
		v := l.SizeVRAMBytes
		rs = append(rs, reading{device: l.Name, vramUsed: &v, models: names})
	}
	if len(rs) == 0 {
		rs = []reading{{models: []string{}}}
	}
	return rs, nil
}

// ---- choosing the probes ----------------------------------------------------------

// probeSet is what a machine can be sampled with: the probes, which of them
// is the graphics memory counter the model's footprint is read from, and
// the sentence for what cannot be sampled.
type probeSet struct {
	probes []probe
	// memoryTool is the tool whose vram readings, summed over devices, are
	// the graphics memory counter; "" when there is none.
	memoryTool   string
	memorySource string
	note         string
}

// chooseProbes picks the probes for this machine from its profile and what
// is installed. It starts nothing and reads nothing but the file system.
func chooseProbes(env sysEnv, p hardware.Profile, b backend.Backend) probeSet {
	var ps probeSet
	var notes []string
	hasVendor := func(v hardware.Vendor) bool {
		for _, g := range p.GPUs {
			if g.Vendor == v {
				return true
			}
		}
		return false
	}
	goos := env.goos()

	if hasVendor(hardware.VendorNVIDIA) {
		if path, err := env.lookPath("nvidia-smi"); err == nil {
			ps.probes = append(ps.probes, nvidiaProbe{env: env, path: path})
			ps.memoryTool, ps.memorySource = "nvidia-smi", "nvidia-smi memory.used, summed over the graphics cards"
		} else {
			notes = append(notes, "the NVIDIA card's memory is not read: nvidia-smi, which comes with NVIDIA's driver, was not found")
		}
	}
	if goos == "linux" && ps.memoryTool == "" {
		if cards := amdCards(env); len(cards) > 0 {
			ps.probes = append(ps.probes, amdSysfsProbe{env: env, cards: cards})
			ps.memoryTool, ps.memorySource = "sysfs", "the graphics driver's own counters (amdgpu mem_info_vram_used and mem_info_gtt_used)"
		}
	}
	appleSilicon := goos == "darwin" && p.UnifiedMemory
	switch goos {
	case "darwin":
		ps.probes = append(ps.probes, vmStatProbe{env: env, wired: appleSilicon}, ioregProbe{env: env})
		if appleSilicon && ps.memoryTool == "" {
			ps.memoryTool, ps.memorySource = "vm_stat", "wired memory (vm_stat): on Apple Silicon the graphics' buffers are wired"
			notes = append(notes, "temperature and power are not read on a Mac: the tool that reads them needs an administrator's password")
		}
	case "linux":
		ps.probes = append(ps.probes, meminfoProbe{env: env})
	case "windows":
		ps.probes = append(ps.probes, osMemoryProbe{env: env})
	}
	if b != nil {
		ps.probes = append(ps.probes, psProbe{b: b})
	}

	if ps.memoryTool == "" && len(p.GPUs) > 0 && !hasVendor(hardware.VendorNVIDIA) {
		g := p.GPUs[0]
		name := hardware.MatchName(g.Name)
		switch {
		case goos == "windows" && (g.Vendor == hardware.VendorAMD || g.Vendor == hardware.VendorIntel):
			notes = append(notes, "how much of the "+name+"'s memory a model takes is not measured: Windows has no reading of it for AMD and Intel cards that works without administrator rights")
		case g.IsIntegrated:
			notes = append(notes, "graphics built into the processor share the computer's memory, which has no separate reading for them")
		default:
			notes = append(notes, "how much of the "+name+"'s memory a model takes is not measured: there is no reading of it on this computer")
		}
	}
	ps.note = strings.Join(notes, "; ")
	return ps
}

// ---- the sampler -------------------------------------------------------------------

// Sampler reads every probe once per interval and keeps what the run needs:
// the samples themselves, the reading before the load, and the peaks.
type Sampler struct {
	set      probeSet
	interval time.Duration
	now      func() time.Time

	mu        sync.Mutex
	baseline  *uint64 // graphics memory counter before the load
	measuring bool
	peakVRAM  uint64
	vramSeen  bool
	peakRAM   uint64
	ramSeen   bool
	utilSum   float64
	utilN     int
	powerSum  float64
	powerN    int
	maxTemp   float64
	tempSeen  bool
	others    map[string]bool // models other than ours seen loaded
	changed   bool            // another model came or went during the run
	ours      string
	pending   []Sample
	last      *Sample
	failures  map[string]string
}

func newSampler(set probeSet, interval time.Duration, now func() time.Time, model string) *Sampler {
	return &Sampler{set: set, interval: interval, now: now, ours: model, failures: map[string]string{}}
}

// Baseline reads every probe once, before the load: the graphics memory
// counter's reading is what the model's footprint is measured from, and the
// models loaded now are the ones whose coming or going would spoil it.
func (s *Sampler) Baseline(ctx context.Context) {
	samples := s.tick(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := sumVRAM(samples, s.set.memoryTool); ok {
		s.baseline = &v
	}
	s.peakVRAM, s.vramSeen = 0, false
	s.changed = false
}

// MarkMeasuring starts the part of the run the means (utilisation, power)
// are taken over: the timed requests.
func (s *Sampler) MarkMeasuring() {
	s.mu.Lock()
	s.measuring = true
	s.mu.Unlock()
}

// Run samples until ctx ends, calling onSample with the reading that stands
// for the tick (the graphics device's, else the system's).
func (s *Sampler) Run(ctx context.Context, onSample func(Sample)) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			samples := s.tick(ctx)
			if ctx.Err() != nil {
				return
			}
			if onSample != nil && len(samples) > 0 {
				onSample(representative(samples, s.set.memoryTool))
			}
		}
	}
}

// tick reads every probe once and folds the readings into the peaks.
func (s *Sampler) tick(ctx context.Context) []Sample {
	at := s.now().UTC()
	var samples []Sample
	var models []string
	psRead := false
	for _, p := range s.set.probes {
		rs, err := p.read(ctx)
		if err != nil {
			s.mu.Lock()
			s.failures[p.tool()] = err.Error()
			s.mu.Unlock()
			continue
		}
		for _, r := range rs {
			if p.tool() == "api/ps" {
				psRead = true
				models = r.models
				if r.device == "" {
					continue // nothing loaded: no row
				}
			}
			samples = append(samples, Sample{At: at, Tool: p.tool(), Device: r.device, GPUUtil: r.gpuUtil,
				VRAMUsed: r.vramUsed, RAMUsed: r.ramUsed, TempC: r.tempC, PowerW: r.powerW})
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := sumVRAM(samples, s.set.memoryTool); ok && (!s.vramSeen || v > s.peakVRAM) {
		s.peakVRAM, s.vramSeen = v, true
	}
	var util, power float64
	utilOK, powerOK := false, false
	for _, x := range samples {
		if x.RAMUsed != nil && x.Tool != "api/ps" && (!s.ramSeen || *x.RAMUsed > s.peakRAM) {
			s.peakRAM, s.ramSeen = *x.RAMUsed, true
		}
		if x.GPUUtil != nil {
			util, utilOK = math.Max(util, *x.GPUUtil), true
		}
		if x.PowerW != nil {
			power, powerOK = power+*x.PowerW, true
		}
		if x.TempC != nil && (!s.tempSeen || *x.TempC > s.maxTemp) {
			s.maxTemp, s.tempSeen = *x.TempC, true
		}
	}
	if s.measuring {
		if utilOK {
			s.utilSum, s.utilN = s.utilSum+util, s.utilN+1
		}
		if powerOK {
			s.powerSum, s.powerN = s.powerSum+power, s.powerN+1
		}
	}
	if psRead {
		current := map[string]bool{}
		for _, m := range models {
			if !sameModel(m, s.ours) {
				current[m] = true
			}
		}
		if s.others == nil {
			s.others = current
		} else if !sameSet(s.others, current) {
			s.changed = true
			s.others = current
		}
	}
	s.pending = append(s.pending, samples...)
	if len(samples) > 0 {
		r := representative(samples, s.set.memoryTool)
		s.last = &r
	}
	return samples
}

// Drain returns the samples not yet taken and forgets them.
func (s *Sampler) Drain() []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.pending
	s.pending = nil
	return out
}

// Last is the latest representative reading.
func (s *Sampler) Last() *Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return nil
	}
	c := *s.last
	return &c
}

// samplerSummary is what the run keeps of the samples.
type samplerSummary struct {
	peakVRAMDelta *uint64
	memorySource  string
	peakRAM       *uint64
	meanUtil      *float64
	maxTemp       *float64
	meanPower     *float64
	othersChanged bool
	failures      map[string]string
}

func (s *Sampler) summary() samplerSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := samplerSummary{memorySource: s.set.memorySource, othersChanged: s.changed, failures: s.failures}
	if s.baseline != nil && s.vramSeen && s.peakVRAM > *s.baseline {
		d := s.peakVRAM - *s.baseline
		out.peakVRAMDelta = &d
	}
	if s.ramSeen {
		v := s.peakRAM
		out.peakRAM = &v
	}
	if s.utilN > 0 {
		v := s.utilSum / float64(s.utilN)
		out.meanUtil = &v
	}
	if s.powerN > 0 {
		v := s.powerSum / float64(s.powerN)
		out.meanPower = &v
	}
	if s.tempSeen {
		v := s.maxTemp
		out.maxTemp = &v
	}
	return out
}

// sumVRAM adds up the graphics memory counter's readings over its devices.
func sumVRAM(samples []Sample, tool string) (uint64, bool) {
	if tool == "" {
		return 0, false
	}
	var sum uint64
	found := false
	for _, x := range samples {
		if x.Tool == tool && x.VRAMUsed != nil {
			sum += *x.VRAMUsed
			found = true
		}
	}
	return sum, found
}

// representative is the reading that stands for a tick in the progress
// stream: the graphics memory counter's first device, else any reading with
// a GPU figure, else the system's memory.
func representative(samples []Sample, tool string) Sample {
	for _, x := range samples {
		if tool != "" && x.Tool == tool {
			return x
		}
	}
	for _, x := range samples {
		if x.GPUUtil != nil {
			return x
		}
	}
	for _, x := range samples {
		if x.RAMUsed != nil {
			return x
		}
	}
	return samples[0]
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// sameModel compares two runtime names for one model: "llama3.2" and
// "llama3.2:latest" are the same.
func sameModel(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		if !strings.Contains(s, ":") {
			s += ":latest"
		}
		return s
	}
	return norm(a) == norm(b)
}

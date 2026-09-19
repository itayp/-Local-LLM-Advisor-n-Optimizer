package bench

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"advisor/internal/backend"
	"advisor/internal/hardware"
)

// fakeSysEnv answers the sampler from fixtures: files by path, commands by
// name (a function, so a test can change the answer between readings).
type fakeSysEnv struct {
	os, arch string
	mu       sync.Mutex
	files    map[string]string
	cmds     map[string]func() (string, error)
	paths    map[string]string
	mem      [2]uint64 // total, available; zero = not answered
}

func (f *fakeSysEnv) goos() string   { return f.os }
func (f *fakeSysEnv) goarch() string { return f.arch }
func (f *fakeSysEnv) run(_ context.Context, name string, _ ...string) (string, error) {
	f.mu.Lock()
	fn, ok := f.cmds[filepath.Base(name)]
	f.mu.Unlock()
	if !ok {
		return "", errors.New(name + ": not found")
	}
	return fn()
}
func (f *fakeSysEnv) lookPath(name string) (string, error) {
	if p, ok := f.paths[name]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}
func (f *fakeSysEnv) readFile(p string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.files[p]; ok {
		return []byte(s), nil
	}
	return nil, os.ErrNotExist
}
func (f *fakeSysEnv) glob(pattern string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for p := range f.files {
		if ok, _ := path.Match(pattern, p); ok {
			out = append(out, p)
		}
	}
	// Directories: the parents of files, for hwmon globs.
	seen := map[string]bool{}
	for p := range f.files {
		for d := path.Dir(p); d != "/" && d != "."; d = path.Dir(d) {
			if ok, _ := path.Match(pattern, d); ok && !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out, nil
}
func (f *fakeSysEnv) memory() (uint64, uint64, bool) { return f.mem[0], f.mem[1], f.mem[0] > 0 }

func (f *fakeSysEnv) setFile(p, v string) {
	f.mu.Lock()
	f.files[p] = v
	f.mu.Unlock()
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "sampler", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseNvidiaSample(t *testing.T) {
	rs, err := parseNvidiaSample(fixture(t, "nvidia-smi.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].device != "0" || *rs[0].gpuUtil != 97 || *rs[0].vramUsed != 10543<<20 || *rs[0].tempC != 64 || *rs[0].powerW != 278.52 {
		t.Fatalf("first card: %+v", rs)
	}
	if rs[1].powerW != nil {
		t.Fatalf("[N/A] must be unknown, not zero: %v", *rs[1].powerW)
	}
	if _, err := parseNvidiaSample("0, 12\n"); err == nil {
		t.Fatal("a line with the wrong number of fields must fail")
	}
}

func TestParseVMStatIoregMeminfo(t *testing.T) {
	rs, err := parseVMStat(fixture(t, "vm_stat.txt"), true)
	if err != nil {
		t.Fatal(err)
	}
	if *rs[0].vramUsed != 372150*16384 || *rs[0].ramUsed != (301422+372150+98311)*16384 {
		t.Fatalf("vm_stat: wired %d, used %d", *rs[0].vramUsed, *rs[0].ramUsed)
	}
	if rs, _ := parseVMStat(fixture(t, "vm_stat.txt"), false); rs[0].vramUsed != nil {
		t.Fatal("an Intel Mac's wired memory is not its graphics memory")
	}
	if rs, err := parseIoreg(fixture(t, "ioreg.txt")); err != nil || *rs[0].gpuUtil != 92 {
		t.Fatalf("ioreg: %+v %v", rs, err)
	}
	rs, err = parseMeminfo(fixture(t, "meminfo.txt"))
	if err != nil || *rs[0].ramUsed != (65755152-48117720)*1024 {
		t.Fatalf("meminfo: %+v %v", rs, err)
	}
}

func profile(os string, gpus ...hardware.GPU) hardware.Profile {
	return hardware.Profile{OS: os, GPUs: gpus, UnifiedMemory: os == "darwin" && len(gpus) > 0 && gpus[0].Vendor == hardware.VendorApple}
}

// Which probes each kind of machine gets, which counter the model's memory
// is read from, and what is said where there is none.
func TestChooseProbes(t *testing.T) {
	nv := hardware.GPU{Vendor: hardware.VendorNVIDIA, Name: "NVIDIA GeForce RTX 5070 Ti"}
	amd := hardware.GPU{Vendor: hardware.VendorAMD, Name: "AMD Radeon RX 7800 XT", IntegratedKnown: true}
	apple := hardware.GPU{Vendor: hardware.VendorApple, Name: "Apple M1 Pro", IsIntegrated: true, IntegratedKnown: true}
	amdSys := map[string]string{
		"/sys/class/drm/card1/device/mem_info_vram_used":        "1000",
		"/sys/class/drm/card1-DP-1/device/mem_info_vram_used":   "1", // a connector: not a card
		"/sys/class/drm/card1/device/hwmon/hwmon3/temp1_input":  "51000",
		"/sys/class/drm/card1/device/hwmon/hwmon3/power1_input": "31000000",
	}
	cases := []struct {
		name      string
		env       *fakeSysEnv
		p         hardware.Profile
		tools     string
		memory    string
		noteHas   string
		noteEmpty bool
	}{
		{"windows nvidia", &fakeSysEnv{os: "windows", paths: map[string]string{"nvidia-smi": `C:\Windows\System32\nvidia-smi.exe`}},
			profile("windows", nv), "nvidia-smi,os,api/ps", "nvidia-smi", "", true},
		{"windows nvidia without nvidia-smi", &fakeSysEnv{os: "windows"}, profile("windows", nv), "os,api/ps", "", "nvidia-smi", false},
		{"windows amd", &fakeSysEnv{os: "windows"}, profile("windows", amd), "os,api/ps", "", "administrator", false},
		{"linux amd", &fakeSysEnv{os: "linux", files: amdSys}, profile("linux", amd), "sysfs,meminfo,api/ps", "sysfs", "", true},
		{"apple silicon", &fakeSysEnv{os: "darwin"}, profile("darwin", apple), "vm_stat,ioreg,api/ps", "vm_stat", "administrator", false},
		{"no gpu", &fakeSysEnv{os: "linux"}, profile("linux"), "meminfo,api/ps", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ps := chooseProbes(c.env, c.p, &fakeBackend{})
			var tools []string
			for _, p := range ps.probes {
				tools = append(tools, p.tool())
			}
			if strings.Join(tools, ",") != c.tools || ps.memoryTool != c.memory {
				t.Errorf("tools %v memory %q, want %s %q", tools, ps.memoryTool, c.tools, c.memory)
			}
			if c.noteEmpty != (ps.note == "") || !strings.Contains(ps.note, c.noteHas) {
				t.Errorf("note %q", ps.note)
			}
		})
	}
	if cards := amdCards(&fakeSysEnv{files: amdSys}); len(cards) != 1 || cards[0] != "/sys/class/drm/card1" {
		t.Errorf("cards %v", cards)
	}
}

// The model's footprint is the counter's peak over its reading before the
// load, summed over devices (AMD's GTT included); another model coming or
// going during the run is noticed.
func TestSamplerPeakOverBaseline(t *testing.T) {
	env := &fakeSysEnv{os: "linux", files: map[string]string{
		"/sys/class/drm/card1/device/mem_info_vram_used": "100",
		"/sys/class/drm/card1/device/mem_info_gtt_used":  "10",
		"/sys/class/drm/card1/device/gpu_busy_percent":   "0",
		"/proc/meminfo": "MemTotal: 1000 kB\nMemAvailable: 600 kB\n",
	}}
	b := &fakeBackend{}
	set := chooseProbes(env, profile("linux", hardware.GPU{Vendor: hardware.VendorAMD, Name: "AMD Radeon RX 580"}), b)
	s := newSampler(set, time.Millisecond, time.Now, "m:latest")
	ctx := context.Background()
	s.Baseline(ctx)

	env.setFile("/sys/class/drm/card1/device/mem_info_vram_used", "5100")
	env.setFile("/sys/class/drm/card1/device/mem_info_gtt_used", "510")
	env.setFile("/sys/class/drm/card1/device/gpu_busy_percent", "90")
	b.setLoaded(backend.Loaded{Name: "m:latest", SizeBytes: 5000, SizeVRAMBytes: 5000})
	s.MarkMeasuring()
	s.tick(ctx)
	env.setFile("/sys/class/drm/card1/device/mem_info_vram_used", "4000")
	env.setFile("/sys/class/drm/card1/device/gpu_busy_percent", "70")
	s.tick(ctx)

	sum := s.summary()
	if sum.peakVRAMDelta == nil || *sum.peakVRAMDelta != (5100+510)-(100+10) {
		t.Fatalf("peak rise %v", sum.peakVRAMDelta)
	}
	if sum.meanUtil == nil || *sum.meanUtil != 80 || sum.peakRAM == nil || *sum.peakRAM != 400*1024 {
		t.Fatalf("summary %+v", sum)
	}
	if sum.othersChanged {
		t.Fatal("only our model came and went")
	}
	b.setLoaded(backend.Loaded{Name: "m:latest"}, backend.Loaded{Name: "other:7b"})
	s.tick(ctx)
	if !s.summary().othersChanged {
		t.Fatal("another model loading during the run must be noticed")
	}
	if n := len(s.Drain()); n == 0 {
		t.Fatal("samples should be kept for the store")
	}
	if len(s.Drain()) != 0 {
		t.Fatal("Drain forgets what it returned")
	}
}

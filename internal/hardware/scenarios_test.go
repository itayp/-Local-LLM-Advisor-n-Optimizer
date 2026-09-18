package hardware

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*.json from the current detector")

// scenario is one machine from the real-world matrix (BUILD_PLAN step 2,
// in its priority order), assembled from one fixture file of tool outputs.
type scenario struct {
	fixture string // testdata/<fixture>.txtar
	goos    string
	goarch  string
	setup   func(f *fakeEnv)
	check   func(t *testing.T, p Profile)
}

func runScenario(t *testing.T, sc scenario) Profile {
	t.Helper()
	f := newFakeEnv(sc.goos, sc.goarch)
	f.home = map[string]string{"linux": "/home/itay", "darwin": "/Users/itay", "windows": `C:\Users\itay`}[sc.goos]
	loadFixture(t, f, sc.fixture)
	if sc.setup != nil {
		sc.setup(f)
	}
	p := detectFixture(t, f)
	checkGolden(t, p)
	if sc.check != nil {
		sc.check(t, p)
	}
	checkInvariants(t, p)
	return p
}

// checkGolden compares the whole profile with testdata/golden/<test>.json.
// The golden files are also the readable answer to "what does the advisor
// say about this machine": review them like code.
func checkGolden(t *testing.T, p Profile) {
	t.Helper()
	got, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "golden", strings.TrimPrefix(t.Name(), "TestScenario")+".json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/hardware -run Scenario -update` to create it)", err)
	}
	if string(want) != string(got) {
		t.Errorf("profile differs from %s (rerun with -update if the change is intended):\n%s", path, diffLines(string(want), string(got)))
	}
}

func diffLines(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out []string
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			out = append(out, "- "+x, "+ "+y)
		}
		if len(out) > 40 {
			out = append(out, "…")
			break
		}
	}
	return strings.Join(out, "\n")
}

// checkInvariants hold for every profile, whatever the machine.
func checkInvariants(t *testing.T, p Profile) {
	t.Helper()
	if p.GPUs == nil {
		t.Error("GPUs must be an empty list, not null")
	}
	if p.Summary == "" || p.Tier == "" {
		t.Error("every profile has a tier and a sentence")
	}
	if p.RAMKnown != (p.RAMBytes > 0) || p.GPUUsableKnown != (p.GPUUsableBytes > 0) || p.Storage.FreeKnown != (p.Storage.FreeBytes > 0) {
		t.Errorf("a number is known exactly when it is non-zero: %+v", p)
	}
	for _, g := range p.GPUs {
		if g.VRAMKnown != (g.VRAMBytes > 0) {
			t.Errorf("%s: vram_known disagrees with vram_bytes", g.Name)
		}
		if g.ExpectedBackend == "" || g.ExpectedBackendReason == "" || g.ExpectedBackendRule == "" {
			t.Errorf("%s: every GPU has an expected backend with a reason and a rule", g.Name)
		}
		if g.Name == "" || g.DriverVersion == "" || g.VRAMSource == "" {
			t.Errorf("%s: empty strings must be %q or say why: %+v", g.Name, Unknown, g)
		}
		if g.busID != "" || g.rawName != "" || g.driverMissing || g.problemCode != 0 || g.actionable {
			t.Errorf("%s: detection internals must be cleared", g.Name)
		}
	}
	for _, s := range []string{p.OSVersion, p.CPU.Model, p.Hostname, p.Storage.ModelsDir} {
		if s == "" {
			t.Errorf("empty string where %q belongs: %+v", Unknown, p)
		}
	}
	if strings.Contains(strings.ToLower(p.Summary), "vram") || strings.Contains(p.Summary, "0 GB") {
		t.Errorf("the summary is plain language and never shows 0 GB: %q", p.Summary)
	}
}

func wantGPU(t *testing.T, p Profile, i int, name string, backend RuntimePath, integrated bool) GPU {
	t.Helper()
	if len(p.GPUs) <= i {
		t.Fatalf("want GPU %d (%s), have %d GPUs: %+v", i, name, len(p.GPUs), p.GPUs)
	}
	g := p.GPUs[i]
	if g.Name != name || g.ExpectedBackend != backend || g.IsIntegrated != integrated || !g.IntegratedKnown {
		t.Fatalf("GPU %d = %q backend=%s integrated=%v(known %v); want %q %s integrated=%v",
			i, g.Name, g.ExpectedBackend, g.IsIntegrated, g.IntegratedKnown, name, backend, integrated)
	}
	return g
}

func wantTier(t *testing.T, p Profile, tier Tier, summary string) {
	t.Helper()
	if p.Tier != tier {
		t.Errorf("tier = %s, want %s (summary %q)", p.Tier, tier, p.Summary)
	}
	if summary != "" && p.Summary != summary {
		t.Errorf("summary =\n  %q\nwant\n  %q", p.Summary, summary)
	}
}

func hasNote(p Profile, substr string) bool {
	for _, n := range p.Notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

func hasProblem(p Profile, substr string) bool {
	for _, n := range p.Problems {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

const gib = 1 << 30

// 1. Windows + NVIDIA ------------------------------------------------------

func TestScenarioWindowsNVIDIADesktop(t *testing.T) {
	runScenario(t, scenario{fixture: "windows/rtx5070ti-desktop", goos: "windows", goarch: "amd64",
		setup: func(f *fakeEnv) { f.avx512 = true },
		check: func(t *testing.T, p Profile) {
			g := wantGPU(t, p, 0, "NVIDIA GeForce RTX 5070 Ti", PathCUDA, false)
			if g.VRAMBytes != 16303*mib || g.DriverVersion != "610.62" || g.ComputeCapability != "12.0" || g.PCIID != "10de:2c05" {
				t.Errorf("nvidia-smi must be authoritative for the NVIDIA card: %+v", g)
			}
			ig := wantGPU(t, p, 1, "AMD Radeon(TM) Graphics", PathVulkan, true)
			if ig.DriverVersion != "25.8.1" {
				t.Errorf("AMD's own version number is the driver version: %q", ig.DriverVersion)
			}
			if len(p.GPUs) != 2 || len(p.FilteredAdapters) != 1 || p.FilteredAdapters[0] != "Parsec Virtual Display Adapter" {
				t.Errorf("the virtual display must be filtered, not listed: %+v / %v", p.GPUs, p.FilteredAdapters)
			}
			if p.GPUUsableBytes != 16303*mib || !strings.Contains(p.GPUUsableSource, "RTX 5070 Ti") {
				t.Errorf("budget = the discrete card: %d %q", p.GPUUsableBytes, p.GPUUsableSource)
			}
			wantTier(t, p, TierGPULarge, "A desktop with an NVIDIA GeForce RTX 5070 Ti (16 GB of graphics memory): large models run on the graphics card.")
			if p.OSVersion != "Windows 11 Pro 24H2 (build 26100.4652)" || p.CPU.CoresPhysical != 8 || p.CPU.CoresLogical != 16 {
				t.Errorf("OS/CPU: %q %+v", p.OSVersion, p.CPU)
			}
			if !p.CPU.HasAVX2 || !p.CPU.HasAVX512 || !p.CPU.VectorKnown {
				t.Errorf("vector extensions: %+v", p.CPU)
			}
			if p.Storage.ModelsDir != `C:\Users\itay\.ollama\models` || p.Storage.ModelsDirExists || !p.Storage.FreeKnown {
				t.Errorf("storage: %+v", p.Storage)
			}
			if len(p.Notes) != 0 || len(p.Problems) != 0 {
				t.Errorf("a clean machine has no notes or problems: %v %v", p.Notes, p.Problems)
			}
		}})
}

func TestScenarioWindowsNVIDIALaptop(t *testing.T) {
	runScenario(t, scenario{fixture: "windows/rtx4060-laptop-optimus", goos: "windows", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			wantGPU(t, p, 0, "NVIDIA GeForce RTX 4060 Laptop GPU", PathCUDA, false)
			wantGPU(t, p, 1, "Intel(R) UHD Graphics", PathVulkan, true)
			if !p.IsLaptop || !p.LaptopKnown {
				t.Error("PCSystemType 2 is a laptop")
			}
			wantTier(t, p, TierGPUMedium, "A laptop with an NVIDIA GeForce RTX 4060 Laptop GPU (8 GB of graphics memory): small and medium-sized models run on the graphics card.")
			if hasNote(p, "graphics cards") {
				t.Error("an iGPU beside one discrete card is not multi-GPU")
			}
		}})
}

// 2. Apple Silicon ---------------------------------------------------------

func TestScenarioAppleSiliconM1Pro(t *testing.T) {
	runScenario(t, scenario{fixture: "darwin/m1pro-16gb", goos: "darwin", goarch: "arm64",
		check: func(t *testing.T, p Profile) {
			g := wantGPU(t, p, 0, "Apple M1 Pro", PathMetal, true)
			if g.VRAMKnown || g.Note != "16-core GPU" {
				t.Errorf("Apple Silicon has no VRAM of its own: %+v", g)
			}
			if !p.UnifiedMemory || p.GPUUsableBytes != 12713115648 || !strings.Contains(p.GPUUsableSource, "recommendedMaxWorkingSetSize") {
				t.Errorf("unset wired limit: the budget is Metal's default: %d %q", p.GPUUsableBytes, p.GPUUsableSource)
			}
			wantTier(t, p, TierGPUMedium, "A Mac laptop with an Apple M1 Pro chip and 16 GB of memory, of which the graphics can use 11.8 GB: small and medium-sized models run on the chip's graphics.")
			if p.OSVersion != "macOS 26.6.2 (25G83)" || p.CPU.Model != "Apple M1 Pro" || p.RAMBytes != 16*gib {
				t.Errorf("OS/CPU/RAM: %q %q %d", p.OSVersion, p.CPU.Model, p.RAMBytes)
			}
			if !p.CPU.VectorKnown || p.CPU.HasAVX2 || hasNote(p, "AVX2") {
				t.Error("AVX does not exist on ARM: known, false, and no complaint about it")
			}
			if p.Storage.ModelsDir != "/Users/itay/.ollama/models" {
				t.Errorf("models dir: %+v", p.Storage)
			}
		}})
}

func TestScenarioAppleSiliconWiredLimitWins(t *testing.T) {
	runScenario(t, scenario{fixture: "darwin/m4max-128gb-wired-limit", goos: "darwin", goarch: "arm64",
		check: func(t *testing.T, p Profile) {
			if p.GPUUsableBytes != 120000*mib || !strings.Contains(p.GPUUsableSource, "iogpu.wired_limit_mb = 120000") {
				t.Errorf("a set wired limit is what the OS enforces: %d %q", p.GPUUsableBytes, p.GPUUsableSource)
			}
			wantTier(t, p, TierGPUXL, "")
			if p.IsLaptop || !p.LaptopKnown {
				t.Error("a Mac Studio is a desktop")
			}
			if p.Storage.ModelsDir != "/Volumes/Models/ollama" || !strings.Contains(p.Storage.ModelsDirSource, "launchctl") {
				t.Errorf("OLLAMA_MODELS from launchd: %+v", p.Storage)
			}
		}})
}

func TestScenarioAppleSiliconSmall(t *testing.T) {
	runScenario(t, scenario{fixture: "darwin/m2-air-8gb", goos: "darwin", goarch: "arm64",
		check: func(t *testing.T, p Profile) {
			wantTier(t, p, TierGPUSmall, "A Mac laptop with an Apple M2 chip and 8 GB of memory, of which the graphics can use 5.3 GB: small models run on the chip's graphics.")
		}})
}

func TestScenarioAppleSiliconMetalUnreadableIsUnknown(t *testing.T) {
	runScenario(t, scenario{fixture: "darwin/m1pro-16gb", goos: "darwin", goarch: "arm64",
		setup: func(f *fakeEnv) {
			f.cmds["osascript -l JavaScript"] = fakeCmd{err: errFake("execution error: Error: ReferenceError")}
		},
		check: func(t *testing.T, p Profile) {
			if p.GPUUsableKnown || p.GPUUsableBytes != 0 || !strings.HasPrefix(p.GPUUsableSource, "unknown") {
				t.Errorf("no wired limit and no Metal answer: unknown, never a percentage of RAM: %d %q", p.GPUUsableBytes, p.GPUUsableSource)
			}
			if !hasProblem(p, "Metal") {
				t.Errorf("the reason is named: %v", p.Problems)
			}
			wantTier(t, p, TierUnknown, "")
		}})
}

func TestScenarioRosettaDescribesTheMachine(t *testing.T) {
	runScenario(t, scenario{fixture: "darwin/m1pro-16gb", goos: "darwin", goarch: "amd64",
		setup: func(f *fakeEnv) {
			out := f.cmds["sysctl"].out
			f.cmds["sysctl"] = fakeCmd{out: strings.Replace(out, "sysctl.proc_translated: 0", "sysctl.proc_translated: 1", 1)}
			f.avx2 = true // what Rosetta would report; must not be believed
		},
		check: func(t *testing.T, p Profile) {
			if p.Arch != "arm64" || !hasNote(p, "Rosetta") {
				t.Errorf("the Intel build under Rosetta must describe an Apple Silicon Mac: arch %s notes %v", p.Arch, p.Notes)
			}
			if p.CPU.HasAVX2 {
				t.Error("an ARM machine has no AVX2, whatever the emulator reports")
			}
			wantGPU(t, p, 0, "Apple M1 Pro", PathMetal, true)
			wantTier(t, p, TierGPUMedium, "")
		}})
}

func TestScenarioMacOSTooOldForOllama(t *testing.T) {
	runScenario(t, scenario{fixture: "darwin/m1pro-16gb", goos: "darwin", goarch: "arm64",
		setup: func(f *fakeEnv) {
			f.cmds["sw_vers"] = fakeCmd{out: "ProductName:\t\tmacOS\nProductVersion:\t\t13.6.9\nBuildVersion:\t\t22G830\n"}
		},
		check: func(t *testing.T, p Profile) {
			g := p.GPUs[0]
			if g.ExpectedBackend != PathNone || g.ExpectedBackendRule != "os-minimum" || !strings.Contains(g.ExpectedBackendReason, "macOS 14.0") {
				t.Errorf("below the OS floor nothing is expected to run: %+v", g)
			}
			wantTier(t, p, TierUnknown, "")
			if !strings.Contains(p.Summary, "too old") {
				t.Errorf("the sentence says why: %q", p.Summary)
			}
		}})
}

// 3. Windows + AMD ---------------------------------------------------------

func TestScenarioWindowsAMDROCm(t *testing.T) {
	runScenario(t, scenario{fixture: "windows/rx7900xtx", goos: "windows", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			g := wantGPU(t, p, 0, "AMD Radeon RX 7900 XTX", PathROCm, false)
			if g.GFXTarget != "gfx1100" || g.DriverVersion != "25.8.1" || g.ExpectedBackendRule != "amd-rocm-windows" {
				t.Errorf("gfx from the name table on Windows: %+v", g)
			}
			wantTier(t, p, TierGPULarge, "A desktop with an AMD Radeon RX 7900 XTX (24 GB of graphics memory): large models run on the graphics card.")
			if !hasProblem(p, "") && len(p.Problems) != 0 {
				t.Errorf("no nvidia-smi on an AMD machine is not a problem: %v", p.Problems)
			}
		}})
}

func TestScenarioWindowsAMDVulkan(t *testing.T) {
	runScenario(t, scenario{fixture: "windows/rx6700xt", goos: "windows", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			g := wantGPU(t, p, 0, "AMD Radeon RX 6700 XT", PathVulkan, false)
			if g.GFXTarget != "gfx1031" {
				t.Errorf("gfx: %q", g.GFXTarget)
			}
			wantTier(t, p, TierGPUMedium, "")
		}})
}

// 4. Linux + NVIDIA, then + AMD -------------------------------------------

func TestScenarioLinuxNVIDIA(t *testing.T) {
	runScenario(t, scenario{fixture: "linux/ubuntu-rtx3090", goos: "linux", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			g := wantGPU(t, p, 0, "NVIDIA GeForce RTX 3090", PathCUDA, false)
			if g.LinuxDriver != "nvidia" || g.VRAMBytes != 24*gib || g.DriverVersion != "575.64.05" {
				t.Errorf("merged by PCI address with nvidia-smi: %+v", g)
			}
			if p.OSVersion != "Ubuntu 24.04.3 LTS" || p.Kernel != "6.8.0-79-generic" || p.RAMBytes != 65766484*1024 {
				t.Errorf("OS/RAM: %q %q %d", p.OSVersion, p.Kernel, p.RAMBytes)
			}
			if p.Storage.ModelsDir != "/usr/share/ollama/.ollama/models" || !p.Storage.ModelsDirExists {
				t.Errorf("a system install's models folder: %+v", p.Storage)
			}
			wantTier(t, p, TierGPULarge, "A desktop with an NVIDIA GeForce RTX 3090 (24 GB of graphics memory): large models run on the graphics card.")
		}})
}

func TestScenarioLinuxAMDROCmFromKFD(t *testing.T) {
	runScenario(t, scenario{fixture: "linux/ubuntu-rx7900xtx", goos: "linux", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			g := wantGPU(t, p, 0, "AMD NITRO+ Radeon RX 7900 XTX Vapor-X", PathROCm, false)
			if g.GFXTarget != "gfx1100" || g.LinuxDriver != "amdgpu" || g.ExpectedBackendRule != "amd-rocm-linux" {
				t.Errorf("gfx from KFD: %+v", g)
			}
			if !strings.Contains(g.DriverVersion, "amdgpu") || !strings.Contains(g.DriverVersion, "6.14.0") {
				t.Errorf("in-kernel driver version names the kernel: %q", g.DriverVersion)
			}
			wantTier(t, p, TierGPULarge, "")
		}})
}

func TestScenarioLinuxMacProTwoD700sOnVulkan(t *testing.T) {
	runScenario(t, scenario{fixture: "linux/ubuntu-macpro-d700", goos: "linux", goarch: "amd64",
		setup: func(f *fakeEnv) { f.avx2 = false },
		check: func(t *testing.T, p Profile) {
			wantGPU(t, p, 0, "AMD FirePro D700", PathVulkan, false)
			wantGPU(t, p, 1, "AMD FirePro D700", PathVulkan, false)
			if p.GPUUsableBytes != 6*gib || !strings.Contains(p.GPUUsableSource, "not added up") {
				t.Errorf("two 6 GiB cards are a 6 GiB budget, never 12: %d %q", p.GPUUsableBytes, p.GPUUsableSource)
			}
			if !hasNote(p, "2 graphics cards") || !hasNote(p, "not optimised") {
				t.Errorf("multi-GPU is noted as not optimised: %v", p.Notes)
			}
			if !hasNote(p, "AVX2") {
				t.Errorf("a Xeon E5 v2 lacks AVX2 and the note says so: %v", p.Notes)
			}
			if p.CPU.CoresPhysical != 6 || p.CPU.CoresLogical != 12 {
				t.Errorf("cores from sysfs sibling sets: %+v", p.CPU)
			}
			if p.LaptopKnown {
				t.Error("no chassis type: unknown, not desktop")
			}
			wantTier(t, p, TierGPUSmall, "This computer with an AMD FirePro D700 (6 GB of graphics memory): small models run on the graphics card.")
		}})
}

// 5. Integrated graphics only, or no GPU ----------------------------------

func TestScenarioWindowsIntegratedOnlyFromPowerShell51(t *testing.T) {
	runScenario(t, scenario{fixture: "windows/iris-xe-laptop", goos: "windows", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			wantGPU(t, p, 0, "Intel(R) Iris(R) Xe Graphics", PathVulkan, true)
			if p.GPUUsableKnown {
				t.Error("an integrated GPU's carve-out is not a model budget")
			}
			wantTier(t, p, TierIntegrated, "A laptop with an Intel Iris Xe Graphics built into its processor and 15.8 GB of memory: small models only, roughly a few words a second.")
		}})
}

func TestScenarioLinuxIntegratedIntel(t *testing.T) {
	runScenario(t, scenario{fixture: "linux/laptop-intel-iris-xe", goos: "linux", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			wantGPU(t, p, 0, "Intel Iris Xe Graphics", PathVulkan, true)
			wantTier(t, p, TierIntegrated, "")
			if !p.IsLaptop {
				t.Error("chassis 10 is a notebook")
			}
		}})
}

func TestScenarioLinuxAMDAPUBySysfsSignal(t *testing.T) {
	runScenario(t, scenario{fixture: "linux/laptop-amd-780m", goos: "linux", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			g := wantGPU(t, p, 0, "AMD Phoenix1", PathVulkan, true)
			if g.GFXTarget != "gfx1103" || !g.VRAMKnown || g.VRAMBytes != 512*mib {
				t.Errorf("KFD target and the carve-out as read: %+v", g)
			}
			wantTier(t, p, TierIntegrated, "")
			if p.CPU.Model != "AMD Ryzen 7 7840U w/ Radeon 780M Graphics" {
				t.Errorf("CPU name is tidied: %q", p.CPU.Model)
			}
		}})
}

func TestScenarioLinuxServerWithoutGPU(t *testing.T) {
	runScenario(t, scenario{fixture: "linux/server-aspeed-only", goos: "linux", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			if len(p.GPUs) != 0 || len(p.FilteredAdapters) != 1 || p.FilteredAdapters[0] != "ASPEED Graphics Family" {
				t.Errorf("a BMC display chip is filtered, not a GPU: %+v %v", p.GPUs, p.FilteredAdapters)
			}
			wantTier(t, p, TierCPUOnly, "A desktop with no graphics card, so models run on the processor (Intel Xeon Silver 4314 CPU @ 2.40GHz) with 252 GB of memory: small models only, roughly a few words a second.")
			if p.Storage.ModelsDir != "/srv/ollama/models" || !strings.Contains(p.Storage.ModelsDirSource, "systemd") {
				t.Errorf("OLLAMA_MODELS from the service's drop-in: %+v", p.Storage)
			}
		}})
}

func TestScenarioLinuxNouveauBesideIntel(t *testing.T) {
	runScenario(t, scenario{fixture: "linux/laptop-nouveau-optimus", goos: "linux", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			wantGPU(t, p, 0, "Intel Iris Xe Graphics", PathVulkan, true)
			nv := wantGPU(t, p, 1, "NVIDIA GeForce RTX 3050 Mobile", PathNone, false)
			if nv.ExpectedBackendRule != "nvidia-nouveau" || nv.LinuxDriver != "nouveau" {
				t.Errorf("nouveau is named: %+v", nv)
			}
			if !hasNote(p, "NVIDIA GeForce RTX 3050 Mobile: ") || !hasNote(p, "Installing the NVIDIA driver") {
				t.Errorf("what would unlock the card is a note: %v", p.Notes)
			}
			wantTier(t, p, TierIntegrated, "")
		}})
}

func TestScenarioLinuxNVIDIAWithNoDriver(t *testing.T) {
	runScenario(t, scenario{fixture: "linux/desktop-nvidia-no-driver", goos: "linux", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			g := wantGPU(t, p, 0, "NVIDIA GeForce RTX 3060 Lite Hash Rate", PathNone, false)
			if g.LinuxDriver != "none" || g.ExpectedBackendRule != "nvidia-no-driver" || g.VRAMKnown {
				t.Errorf("no driver bound: %+v", g)
			}
			wantTier(t, p, TierCPUOnly, "A desktop whose graphics Ollama cannot use, so models run on the processor (Intel Core i5-12400F) with 15.4 GB of memory: small models only, roughly a few words a second.")
		}})
}

func TestScenarioWindowsNVIDIAWithoutDriver(t *testing.T) {
	runScenario(t, scenario{fixture: "windows/nvidia-no-driver", goos: "windows", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			g := wantGPU(t, p, 0, "NVIDIA graphics card (model unknown: no driver installed)", PathNone, false)
			if g.PCIID != "10de:2504" || g.ExpectedBackendRule != "nvidia-no-driver" {
				t.Errorf("the PCI id says whose card it is: %+v", g)
			}
			if len(p.FilteredAdapters) != 1 {
				t.Errorf("the remote display is filtered: %v", p.FilteredAdapters)
			}
			wantTier(t, p, TierCPUOnly, "")
			if !hasNote(p, "Installing the NVIDIA driver") {
				t.Errorf("the fix is a note: %v", p.Notes)
			}
		}})
}

func TestScenarioWindowsTooOldForOllama(t *testing.T) {
	runScenario(t, scenario{fixture: "windows/win10-21h2-too-old", goos: "windows", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			if p.GPUs[0].ExpectedBackend != PathNone || p.GPUs[0].ExpectedBackendRule != "os-minimum" {
				t.Errorf("below Ollama's Windows floor: %+v", p.GPUs[0])
			}
			wantTier(t, p, TierUnknown, "")
		}})
}

// 6. Intel Arc, Intel Macs, multi-GPU — detect, label, do not optimise ----

func TestScenarioIntelArc(t *testing.T) {
	runScenario(t, scenario{fixture: "windows/arc-a770", goos: "windows", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			wantGPU(t, p, 0, "Intel(R) Arc(TM) A770 Graphics", PathVulkan, false)
			wantTier(t, p, TierGPULarge, "")
			if !hasNote(p, "Intel Arc") {
				t.Errorf("Arc is labelled as not tuned: %v", p.Notes)
			}
		}})
}

func TestScenarioIntelMac(t *testing.T) {
	runScenario(t, scenario{fixture: "darwin/intel-mbp-2019", goos: "darwin", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			wantGPU(t, p, 0, "AMD Radeon Pro 5500M", PathNone, false)
			wantGPU(t, p, 1, "Intel UHD Graphics 630", PathNone, true)
			if p.UnifiedMemory || p.Arch != "amd64" || p.GPUs[0].VRAMBytes != 8*gib || p.GPUs[0].PCIID != "1002:7340" {
				t.Errorf("an Intel Mac reads as one: %+v", p)
			}
			wantTier(t, p, TierCPUOnly, "A Mac laptop with an Intel processor (Intel Core i9-9880H CPU @ 2.30GHz) and 32 GB of memory: on Intel Macs Ollama runs models on the processor, so small models only, roughly a few words a second.")
		}})
}

func TestScenarioLinuxTwoNVIDIACards(t *testing.T) {
	runScenario(t, scenario{fixture: "linux/ubuntu-2x-rtx3090", goos: "linux", goarch: "amd64",
		check: func(t *testing.T, p Profile) {
			if len(p.GPUs) != 2 || p.GPUUsableBytes != 24*gib {
				t.Errorf("two cards listed, budget of one: %d GPUs, %d bytes", len(p.GPUs), p.GPUUsableBytes)
			}
			if !hasNote(p, "not optimised") {
				t.Errorf("multi-GPU note: %v", p.Notes)
			}
			wantTier(t, p, TierGPULarge, "")
		}})
}

// Nothing readable: every OS path degrades to unknown, never to a default.

func TestScenarioNothingReadableIsUnknownEverywhere(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			f := newFakeEnv(goos, "amd64")
			f.vecOK = false
			f.freeErr = errFake("no disk")
			p := detectFixture(t, f)
			checkInvariants(t, p)
			if p.OSVersion != Unknown || p.CPU.Model != Unknown || p.RAMKnown || p.GPUUsableKnown || p.CPU.VectorKnown || p.LaptopKnown || p.Storage.FreeKnown {
				t.Errorf("nothing readable must read as unknown: %+v", p)
			}
			if p.Tier != TierUnknown {
				t.Errorf("an unread device list is not \"no GPU\": tier %s", p.Tier)
			}
			if len(p.Problems) == 0 {
				t.Error("Problems says what could not be read")
			}
		})
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

// Windows always has a video controller and every Mac lists its graphics:
// an empty list from either is a failed query, never "no graphics card".
func TestEmptyDeviceListIsNotNoGPU(t *testing.T) {
	win := newFakeEnv("windows", "amd64")
	win.home = `C:\Users\itay`
	win.cmds["powershell -NoProfile"] = fakeCmd{out: `{"schema":"1","os_caption":"Microsoft Windows 11 Pro","os_build":"26100","ram_bytes":"17179869184","adapters":[]}`}
	win.onPath["powershell"] = true
	p := detectFixture(t, win)
	if p.Tier != TierUnknown || !p.RAMKnown {
		t.Errorf("windows: tier %s, want unknown; the rest of the probe still counts (ram known %v)", p.Tier, p.RAMKnown)
	}

	mac := newFakeEnv("darwin", "arm64")
	mac.home = "/Users/itay"
	loadFixture(t, mac, "darwin/m1pro-16gb")
	mac.cmds["system_profiler"] = fakeCmd{out: `{"SPDisplaysDataType":[],"SPHardwareDataType":[]}`}
	p = detectFixture(t, mac)
	if p.Tier != TierUnknown {
		t.Errorf("macOS: tier %s, want unknown", p.Tier)
	}
}

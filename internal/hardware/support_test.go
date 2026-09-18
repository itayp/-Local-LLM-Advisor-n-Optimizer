package hardware

import (
	"io/fs"
	"strings"
	"testing"

	"advisor/data"
)

func TestRuntimeSupportTableLoads(t *testing.T) {
	sup, err := loadSupport()
	if err != nil {
		t.Fatal(err)
	}
	if sup.OllamaVersion == "" || len(sup.Rules) == 0 || len(sup.AMDGFX) == 0 || len(sup.Integrated) == 0 {
		t.Fatalf("empty table: %+v", sup)
	}
	for _, r := range sup.Rules {
		if r.Backend == PathCPU {
			t.Errorf("rule %s: expectations are about GPUs; cpu is what step 3 may observe, not expect", r.ID)
		}
	}
}

// TestExpectedBackends is the table's contract, one row per case the build
// plan names. A change to runtime-support.yaml that changes an answer here
// is a change to what customers are told.
func TestExpectedBackends(t *testing.T) {
	sup, err := loadSupport()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		g    gpuFacts
		want RuntimePath
		rule string
	}{
		{"RTX 5070 Ti, Windows", gpuFacts{goos: "windows", goarch: "amd64", vendor: VendorNVIDIA, name: "NVIDIA GeForce RTX 5070 Ti", compute: "12.0", driverVersion: "610.62"}, PathCUDA, "nvidia-cuda"},
		{"RTX 2060 laptop", gpuFacts{goos: "windows", goarch: "amd64", vendor: VendorNVIDIA, name: "NVIDIA GeForce RTX 2060", compute: "7.5", driverVersion: "566.36"}, PathCUDA, "nvidia-cuda"},
		{"old driver", gpuFacts{goos: "windows", goarch: "amd64", vendor: VendorNVIDIA, name: "NVIDIA GeForce RTX 3080", compute: "8.6", driverVersion: "537.58"}, PathVulkan, "nvidia-driver-too-old"},
		{"GTX 1080 on 560", gpuFacts{goos: "linux", goarch: "amd64", vendor: VendorNVIDIA, name: "NVIDIA GeForce GTX 1080", compute: "6.1", driverVersion: "560.35.03"}, PathVulkan, "nvidia-maxwell-pascal-driver-too-old"},
		{"GTX 1080 on 575", gpuFacts{goos: "linux", goarch: "amd64", vendor: VendorNVIDIA, name: "NVIDIA GeForce GTX 1080", compute: "6.1", driverVersion: "575.64.05"}, PathCUDA, "nvidia-cuda"},
		{"Kepler", gpuFacts{goos: "linux", goarch: "amd64", vendor: VendorNVIDIA, name: "NVIDIA GeForce GTX 780", compute: "3.5", driverVersion: "470.256.02"}, PathNone, "nvidia-too-old"},
		{"no compute cap, RTX name", gpuFacts{goos: "windows", goarch: "amd64", vendor: VendorNVIDIA, name: "NVIDIA GeForce RTX 3060"}, PathCUDA, "nvidia-by-name"},
		{"no compute cap, unknown name", gpuFacts{goos: "linux", goarch: "amd64", vendor: VendorNVIDIA, name: "NVIDIA Device 2f00"}, PathUnknown, "nvidia-unknown"},
		{"nouveau", gpuFacts{goos: "linux", goarch: "amd64", vendor: VendorNVIDIA, name: "NVIDIA GeForce RTX 3060", linuxDriver: "nouveau"}, PathNone, "nvidia-nouveau"},
		{"no NVIDIA driver", gpuFacts{goos: "windows", goarch: "amd64", vendor: VendorNVIDIA, driverMissing: true}, PathNone, "nvidia-no-driver"},
		{"M1 Pro", gpuFacts{goos: "darwin", goarch: "arm64", vendor: VendorApple, name: "Apple M1 Pro"}, PathMetal, "apple-metal"},
		{"Intel Mac Radeon", gpuFacts{goos: "darwin", goarch: "amd64", vendor: VendorAMD, name: "AMD Radeon Pro 5500M"}, PathNone, "intel-mac"},
		{"RX 7900 XTX, Windows", gpuFacts{goos: "windows", goarch: "amd64", vendor: VendorAMD, gfx: "gfx1100"}, PathROCm, "amd-rocm-windows"},
		{"RX 6800 XT, Windows (build ships gfx1030)", gpuFacts{goos: "windows", goarch: "amd64", vendor: VendorAMD, gfx: "gfx1030"}, PathROCm, "amd-rocm-windows"},
		{"RX 9070 XT, Windows", gpuFacts{goos: "windows", goarch: "amd64", vendor: VendorAMD, gfx: "gfx1201"}, PathROCm, "amd-rocm-windows"},
		{"RX 6700 XT, Windows", gpuFacts{goos: "windows", goarch: "amd64", vendor: VendorAMD, gfx: "gfx1031"}, PathVulkan, "amd-vulkan"},
		{"MI210, Linux", gpuFacts{goos: "linux", goarch: "amd64", vendor: VendorAMD, gfx: "gfx90a"}, PathROCm, "amd-rocm-linux"},
		{"MI210, Windows", gpuFacts{goos: "windows", goarch: "amd64", vendor: VendorAMD, gfx: "gfx90a"}, PathVulkan, "amd-vulkan"},
		{"780M, Linux", gpuFacts{goos: "linux", goarch: "amd64", vendor: VendorAMD, gfx: "gfx1103", linuxDriver: "amdgpu"}, PathVulkan, "amd-vulkan"},
		{"Ryzen AI Max+ 395, Linux", gpuFacts{goos: "linux", goarch: "amd64", vendor: VendorAMD, gfx: "gfx1151", linuxDriver: "amdgpu"}, PathROCm, "amd-rocm-linux"},
		{"D700 on amdgpu (no KFD target)", gpuFacts{goos: "linux", goarch: "amd64", vendor: VendorAMD, name: "AMD FirePro D700", linuxDriver: "amdgpu"}, PathVulkan, "amd-vulkan"},
		{"D700 on radeon", gpuFacts{goos: "linux", goarch: "amd64", vendor: VendorAMD, name: "AMD FirePro D700", linuxDriver: "radeon"}, PathNone, "amd-radeon-kernel-driver"},
		{"Arc A770", gpuFacts{goos: "windows", goarch: "amd64", vendor: VendorIntel, name: "Intel(R) Arc(TM) A770 Graphics"}, PathVulkan, "intel-vulkan"},
		{"Iris Xe, Linux", gpuFacts{goos: "linux", goarch: "amd64", vendor: VendorIntel, name: "Intel Iris Xe Graphics", linuxDriver: "i915"}, PathVulkan, "intel-vulkan"},
		{"Adreno", gpuFacts{goos: "windows", goarch: "arm64", vendor: VendorQualcomm, name: "Qualcomm(R) Adreno(TM) X1-85 GPU"}, PathUnknown, "unchecked"},
	}
	for _, c := range cases {
		r := sup.expect(c.g)
		if r.Backend != c.want || r.ID != c.rule {
			t.Errorf("%s: got %s (%s), want %s (%s)", c.name, r.Backend, r.ID, c.want, c.rule)
		}
	}
}

func TestAMDTargetsByName(t *testing.T) {
	sup, _ := loadSupport()
	for name, want := range map[string]string{
		"AMD Radeon RX 7900 XTX":        "gfx1100",
		"AMD Radeon(TM) RX 7900 GRE":    "gfx1100",
		"AMD Radeon RX 7800 XT":         "gfx1101",
		"AMD Radeon RX 7600M XT":        "gfx1102",
		"AMD Radeon RX 6800 XT":         "gfx1030",
		"AMD Radeon RX 6950 XT":         "gfx1030",
		"AMD Radeon RX 6800M":           "gfx1031",
		"AMD Radeon RX 6700 XT":         "gfx1031",
		"AMD Radeon RX 6800S":           "gfx1032",
		"AMD Radeon RX 6600":            "gfx1032",
		"AMD Radeon RX 6500 XT":         "gfx1034",
		"AMD Radeon RX 9070 XT":         "gfx1201",
		"AMD Radeon RX 9060 XT":         "gfx1200",
		"AMD Radeon 890M Graphics":      "gfx1150",
		"AMD Radeon(TM) 8060S Graphics": "gfx1151",
		"AMD Radeon 780M Graphics":      "gfx1103",
		"AMD Radeon PRO W7900":          "gfx1100",
		"AMD Radeon RX 580 2048SP":      "gfx803",
		"AMD Radeon(TM) Graphics":       "", // several chips share the name
		"AMD FirePro D700":              "",
	} {
		if got := sup.gfxFor(name); got != want {
			t.Errorf("gfxFor(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestIntegratedByName(t *testing.T) {
	sup, _ := loadSupport()
	cases := []struct {
		v                 Vendor
		name              string
		integrated, known bool
	}{
		{VendorIntel, "Intel(R) Iris(R) Xe Graphics", true, true},
		{VendorIntel, "Intel(R) Arc(TM) Graphics", true, true}, // Meteor Lake's built-in graphics
		{VendorIntel, "Intel(R) Arc(TM) 140V GPU", true, true}, // Lunar Lake's
		{VendorIntel, "Intel(R) Arc(TM) A770 Graphics", false, true},
		{VendorIntel, "Intel(R) Arc(TM) B580 Graphics", false, true},
		{VendorIntel, "Intel(R) Arc(TM) A370M Graphics", false, true},
		{VendorIntel, "DG2 [Arc A770]", false, true},
		{VendorAMD, "AMD Radeon(TM) Graphics", true, true},
		{VendorAMD, "AMD Radeon(TM) Vega 8 Graphics", true, true},
		{VendorAMD, "AMD Radeon RX Vega 10 Graphics", true, true},
		{VendorAMD, "AMD Radeon RX Vega 56", false, true},
		{VendorAMD, "AMD Radeon 780M Graphics", true, true},
		{VendorAMD, "Phoenix1", true, true},
		{VendorAMD, "Strix Halo [Radeon Graphics / Radeon 8050S / 8060S]", true, true},
		{VendorAMD, "AMD Radeon RX 7900 XTX", false, true},
		{VendorAMD, "AMD Radeon Pro 5500M", false, true},
		{VendorAMD, "Tahiti XT [Radeon HD 7970/8970 OEM / R9 280X] AMD FirePro D700", false, true},
		{VendorAMD, "AMD Mystery Accelerator", false, false},
		{VendorNVIDIA, "NVIDIA GeForce RTX 4060 Laptop GPU", false, true},
		{VendorNVIDIA, "NVIDIA GB10", true, true},
		{VendorApple, "Apple M4 Max", true, true},
		{VendorQualcomm, "Qualcomm(R) Adreno(TM) X1-85 GPU", false, false},
	}
	for _, c := range cases {
		in, k := sup.integratedFor(c.v, c.name)
		if in != c.integrated || k != c.known {
			t.Errorf("integratedFor(%s, %q) = %v known=%v, want %v known=%v", c.v, c.name, in, k, c.integrated, c.known)
		}
	}
}

func TestOSFloors(t *testing.T) {
	sup, _ := loadSupport()
	for v, want := range map[string]bool{"13.6.9": true, "14.0": false, "26.6.2": false} {
		if old, ok := sup.darwinTooOld(v); !ok || old != want {
			t.Errorf("macOS %s: tooOld=%v ok=%v", v, old, ok)
		}
	}
	if _, ok := sup.darwinTooOld(Unknown); ok {
		t.Error("an unknown version is not judged")
	}
	for b, want := range map[int]bool{19044: true, 19045: false, 26100: false} {
		if old, ok := sup.windowsTooOld(b); !ok || old != want {
			t.Errorf("build %d: tooOld=%v", b, old)
		}
	}
}

func TestRuntimeSupportValidation(t *testing.T) {
	good, err := fs.ReadFile(data.Files, data.RuntimeSupportPath)
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(old, new string) []byte {
		t.Helper()
		s := string(good)
		if !strings.Contains(s, old) {
			t.Fatalf("fixture drift: %q not in the table", old)
		}
		return []byte(strings.Replace(s, old, new, 1))
	}
	bad := map[string][]byte{
		"unknown key":        mutate("    backend: cuda\n", "    backend: cuda\n    bakend: cuda\n"),
		"bad backend":        mutate("    backend: cuda\n", "    backend: opencl\n"),
		"bad date":           mutate("checked: 2026-09-18\n    source: Ollama v0.34.2 CMakePresets.json — cuda_v12", "checked: last week\n    source: Ollama v0.34.2 CMakePresets.json — cuda_v12"),
		"bad regex":          mutate(`name: ['\bRTX\b'`, `name: ['(RTX'`),
		"bad gfx":            mutate("gfx: [gfx908,", "gfx: [vega,"),
		"catch-all required": mutate("  - id: unchecked\n    backend: unknown\n", "  - id: unchecked\n    vendor: intel\n    backend: unknown\n"),
		"duplicate id":       mutate("  - id: nvidia-unknown\n", "  - id: nvidia-cuda\n"),
	}
	for name, b := range bad {
		if _, err := parseSupport(b); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
	if _, err := parseSupport(good); err != nil {
		t.Fatalf("the shipped table must parse: %v", err)
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{{"550", "550.54.14", -1}, {"549.99", "550", -1}, {"610.62", "550", 1}, {"5.0", "5", 0}, {"12.0", "5.0", 1}, {"6.2", "6.10", -1}}
	for _, c := range cases {
		a, _ := parseVersion(c.a)
		b, _ := parseVersion(c.b)
		if got := compareVersions(a, b); got != c.want {
			t.Errorf("compare(%s, %s) = %d", c.a, c.b, got)
		}
	}
	if _, err := parseVersion("unknown"); err == nil {
		t.Error("unknown is not a version")
	}
}

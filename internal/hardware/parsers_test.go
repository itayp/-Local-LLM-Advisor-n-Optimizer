package hardware

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf16"
)

// One test per tool the detector reads, against the fixtures' real-format
// output. None of them needs the tool, so all run on every CI runner.

// --- nvidia-smi (Windows and Linux) ---------------------------------------

func TestParseNvidiaSMI(t *testing.T) {
	out := section(t, "windows/rtx5070ti-desktop", "$ nvidia-smi")
	gpus, err := parseNvidiaSMI(out, nvidiaSMIFields)
	if err != nil {
		t.Fatal(err)
	}
	g := gpus[0]
	if len(gpus) != 1 || g.Vendor != VendorNVIDIA || g.Name != "NVIDIA GeForce RTX 5070 Ti" ||
		g.VRAMBytes != 16303*mib || !g.VRAMKnown || g.DriverVersion != "610.62" ||
		g.ComputeCapability != "12.0" || g.busID != "0000:01:00.0" || g.PCIID != "10de:2c05" {
		t.Fatalf("parsed %+v", g)
	}

	two, err := parseNvidiaSMI(section(t, "linux/ubuntu-2x-rtx3090", "$ nvidia-smi"), nvidiaSMIFields)
	if err != nil || len(two) != 2 || two[1].busID != "0000:4a:00.0" {
		t.Fatalf("two GPUs: %+v %v", two, err)
	}

	// Values the driver does not have, Windows line endings.
	na, err := parseNvidiaSMI("0, NVIDIA GB10, [N/A], 580.95.05, 12.1, 0000000F:01:00.0, 0x2E1210DE\r\n", nvidiaSMIFields)
	if err != nil || na[0].VRAMKnown || na[0].VRAMBytes != 0 || !strings.Contains(na[0].VRAMSource, "did not report") {
		t.Fatalf("[N/A] memory must stay unknown: %+v %v", na, err)
	}
	if na[0].busID != "000f:01:00.0" {
		t.Fatalf("bus id normalised: %q", na[0].busID)
	}

	if _, err := parseNvidiaSMI("0, NVIDIA GeForce RTX 3090, 24576\n", nvidiaSMIFields); err == nil {
		t.Fatal("a line with the wrong number of fields is an error, not a guess")
	}
	if _, err := parseNvidiaSMI("\n\n", nvidiaSMIFields); err == nil {
		t.Fatal("no GPUs listed is an error")
	}
}

func TestQueryNvidiaSMIRetriesWithoutComputeCap(t *testing.T) {
	f := newFakeEnv("linux", "amd64")
	f.cmds["nvidia-smi --query-gpu=index,name,memory.total,driver_version,compute_cap"] = fakeCmd{
		err: errFake(`nvidia-smi: exit status 2: Field "compute_cap" is not a valid field to query.`)}
	f.cmds["nvidia-smi --query-gpu=index,name,memory.total,driver_version,pci.bus_id"] = fakeCmd{
		out: "0, NVIDIA GeForce GTX 1080, 8192, 470.256.02, 00000000:01:00.0, 0x1B8010DE\n"}
	gpus, ok, err := queryNvidiaSMI(context.Background(), f, "nvidia-smi")
	if !ok || err != nil || len(gpus) != 1 || gpus[0].ComputeCapability != "" || gpus[0].DriverVersion != "470.256.02" {
		t.Fatalf("old drivers answer without compute_cap: %+v %v %v", gpus, ok, err)
	}
}

func TestNvidiaIDHelpers(t *testing.T) {
	for in, want := range map[string]string{"00000000:0B:00.0": "0000:0b:00.0", "0000:01:00.0": "0000:01:00.0", "1:02:00.0": "0001:02:00.0"} {
		if got := normalizeBusID(in); got != want {
			t.Errorf("normalizeBusID(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"0x2C0510DE": "10de:2c05", "0x220410de": "10de:2204", "garbage": "", "0x12": ""} {
		if got := pciIDFromNvidia(in); got != want {
			t.Errorf("pciIDFromNvidia(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"32.0.15.6109": "561.09", "32.0.16.1062": "610.62", "31.0.15.5222": "552.22", "30.0.14.7141": "471.41",
		"32.0.101.5972": "", "10.0.26100.1": "", "": "",
	} {
		if got := nvidiaDriverFromWindows(in); got != want {
			t.Errorf("nvidiaDriverFromWindows(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- Windows: the PowerShell query (CIM + display-class registry values) ---

func TestParseWinProbe(t *testing.T) {
	w, err := parseWinProbe(section(t, "windows/rtx5070ti-desktop", "$ powershell -NoProfile"))
	if err != nil {
		t.Fatal(err)
	}
	if w.OSCaption != "Microsoft Windows 11 Pro" || w.OSBuild != "26100" || len(w.CPUs) != 1 || len(w.Adapters) != 3 ||
		w.Adapters[0].QwMemorySize != "17094934528" || w.Adapters[1].RadeonSoftwareVersion != "25.8.1" || w.ChassisTypes[0] != "3" {
		t.Fatalf("parsed %+v", w)
	}

	// Windows PowerShell 5.1: a BOM, and one-element arrays unrolled.
	w, err = parseWinProbe(section(t, "windows/iris-xe-laptop", "$ powershell -NoProfile"))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Adapters) != 1 || len(w.CPUs) != 1 || len(w.ChassisTypes) != 1 || w.ChassisTypes[0] != "10" ||
		w.Adapters[0].Name != "Intel(R) Iris(R) Xe Graphics" {
		t.Fatalf("unrolled arrays must read as one-element lists: %+v", w)
	}

	if _, err := parseWinProbe("WARNING: something\n" + `{"schema":"1","adapters":[]}`); err != nil {
		t.Fatalf("text before the JSON is skipped: %v", err)
	}
	if _, err := parseWinProbe("Get-CimInstance : Access denied"); err == nil {
		t.Fatal("output that is not JSON is an error")
	}
}

func TestWindowsAdapterClassification(t *testing.T) {
	cases := []struct {
		name, pnp     string
		filtered      bool
		vendor        Vendor
		driverMissing bool
		note          string
	}{
		{"NVIDIA GeForce RTX 3060", `PCI\VEN_10DE&DEV_2504&SUBSYS_1&REV_A1\4&1`, false, VendorNVIDIA, false, ""},
		{"Parsec Virtual Display Adapter", `ROOT\DISPLAY\0000`, true, "", false, ""},
		{"Microsoft Remote Display Adapter", `SWD\REMOTEDISPLAYENUM\RDPIDD`, true, "", false, ""},
		{"Microsoft Basic Render Driver", `ROOT\BasicRender\0000`, true, "", false, ""},
		{"Microsoft Hyper-V Video", `VMBUS\{DA0A7802-E377-4AAC-8E77-0558EB1073F8}`, true, "", false, ""},
		{"ASPEED Graphics Family(WDDM)", `PCI\VEN_1A03&DEV_2000&SUBSYS_1&REV_52\4&1`, true, "", false, ""},
		{"Meta Virtual Monitor", `PCI\VEN_10DE&DEV_2504`, true, "", false, ""},
		{"Microsoft Basic Display Adapter", `PCI\VEN_1002&DEV_744C&SUBSYS_1&REV_C8\6&1`, false, VendorAMD, true, "basic display driver"},
		{"Qualcomm(R) Adreno(TM) X1-85 GPU", `ACPI\QCOM0C36\2&DABA3FF&0`, false, VendorQualcomm, false, ""},
	}
	for _, c := range cases {
		g, filtered := windowsAdapterGPU(winAdapter{Name: c.name, PNPDeviceID: c.pnp, ErrorCode: "0"})
		if filtered != c.filtered {
			t.Errorf("%s: filtered = %v, want %v", c.name, filtered, c.filtered)
			continue
		}
		if filtered {
			continue
		}
		if g.Vendor != c.vendor || g.driverMissing != c.driverMissing || (c.note != "" && !strings.Contains(g.Note, c.note)) {
			t.Errorf("%s: %+v", c.name, g)
		}
	}
	g, _ := windowsAdapterGPU(winAdapter{Name: "NVIDIA GeForce RTX 3080", PNPDeviceID: `PCI\VEN_10DE&DEV_2206`, ErrorCode: "43", QwMemorySize: "10737418240"})
	if g.problemCode != 43 || !strings.Contains(g.Note, "code 43") || g.VRAMBytes != 10<<30 {
		t.Errorf("a Device Manager problem code is kept and said: %+v", g)
	}
}

func TestPowerShellScriptTravelsSafely(t *testing.T) {
	if strings.Contains(winProbeScript, `"`) {
		t.Error("the script promises no double quotes")
	}
	raw, err := base64.StdEncoding.DecodeString(encodedCommand(winProbeScript))
	if err != nil || len(raw)%2 != 0 {
		t.Fatal(err)
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if string(utf16.Decode(u)) != winProbeScript {
		t.Fatal("-EncodedCommand must be base64 of UTF-16LE")
	}
	for _, want := range []string{"HardwareInformation.qwMemorySize", "Win32_VideoController", "PCSystemType", "ChassisTypes", "RadeonSoftwareVersion", "ConvertTo-Json -Depth"} {
		if !strings.Contains(winProbeScript, want) {
			t.Errorf("the script must read %s", want)
		}
	}
	if strings.Contains(winProbeScript, "AdapterRAM") {
		t.Error("AdapterRAM is 32-bit and wraps at 4 GB; never use it")
	}
}

func TestLaptopFromChassisAndPCSystemType(t *testing.T) {
	for code, want := range map[int][2]bool{9: {true, true}, 10: {true, true}, 31: {true, true}, 3: {false, true}, 35: {false, true}, 23: {false, true}, 1: {false, false}, 2: {false, false}} {
		l, k := laptopFromChassis(code)
		if l != want[0] || k != want[1] {
			t.Errorf("chassis %d: laptop=%v known=%v", code, l, k)
		}
	}
	for pc, want := range map[string][2]bool{"2": {true, true}, "8": {true, true}, "1": {false, true}, "3": {false, true}, "0": {false, false}} {
		p := Profile{}
		applyWinProbe(&p, winProbe{PCSystemType: pc})
		if p.IsLaptop != want[0] || p.LaptopKnown != want[1] {
			t.Errorf("PCSystemType %s: laptop=%v known=%v", pc, p.IsLaptop, p.LaptopKnown)
		}
	}
}

// --- macOS: sysctl, sw_vers, system_profiler, Metal -----------------------

func TestParseSysctlAndSwVers(t *testing.T) {
	sc := parseSysctl(section(t, "darwin/m1pro-16gb", "$ sysctl"))
	if sc["hw.memsize"] != "17179869184" || sc["machdep.cpu.brand_string"] != "Apple M1 Pro" || sc["iogpu.wired_limit_mb"] != "0" {
		t.Fatalf("%v", sc)
	}
	intel := parseSysctl(section(t, "darwin/intel-mbp-2019", "$ sysctl"))
	if _, ok := intel["iogpu.wired_limit_mb"]; ok || intel["hw.logicalcpu"] != "16" {
		t.Fatalf("an Intel Mac has no iogpu key: %v", intel)
	}
	v := parseSwVers(section(t, "darwin/m1pro-16gb", "$ sw_vers"))
	if v.name != "macOS" || v.version != "26.6.2" || v.build != "25G83" {
		t.Fatalf("%+v", v)
	}
}

func TestParseSystemProfiler(t *testing.T) {
	sp, err := parseSystemProfiler(section(t, "darwin/m1pro-16gb", "$ system_profiler"))
	if err != nil {
		t.Fatal(err)
	}
	if sp.Hardware[0].MachineName != "MacBook Pro" || sp.Hardware[0].ChipType != "Apple M1 Pro" {
		t.Fatalf("%+v", sp.Hardware)
	}
	gpus := darwinGPUs(sp, "macOS 26.6.2 (25G83)")
	if len(gpus) != 1 || gpus[0].Vendor != VendorApple || !gpus[0].IsIntegrated || gpus[0].VRAMKnown || gpus[0].DriverVersion != "part of macOS 26.6.2 (25G83)" {
		t.Fatalf("%+v", gpus)
	}

	sp, err = parseSystemProfiler(section(t, "darwin/intel-mbp-2019", "$ system_profiler"))
	if err != nil {
		t.Fatal(err)
	}
	gpus = darwinGPUs(sp, "macOS 15.7.1")
	if len(gpus) != 2 {
		t.Fatalf("%+v", gpus)
	}
	uhd, amd := gpus[0], gpus[1]
	if uhd.Vendor != VendorIntel || !uhd.IsIntegrated || uhd.VRAMBytes != 1536*mib || uhd.PCIID != "8086:3e9b" {
		t.Errorf("Intel UHD 630: %+v", uhd)
	}
	if amd.Vendor != VendorAMD || amd.IsIntegrated || amd.VRAMBytes != 8*gib || amd.PCIID != "1002:7340" {
		t.Errorf("Radeon Pro 5500M: %+v", amd)
	}
	if _, err := parseSystemProfiler("system_profiler: command failed"); err == nil {
		t.Error("non-JSON output is an error")
	}
}

func TestParseSizeString(t *testing.T) {
	for in, want := range map[string]uint64{"8 GB": 8 << 30, "1536 MB": 1536 << 20, "1.5 GB": 3 << 29, "512mb": 512 << 20, "": 0, "8 bananas": 0} {
		got, ok := parseSizeString(in)
		if got != want || ok != (want > 0) {
			t.Errorf("parseSizeString(%q) = %d, %v", in, got, ok)
		}
	}
}

func TestParseMetalProbe(t *testing.T) {
	m, err := parseMetalProbe(section(t, "darwin/m1pro-16gb", "$ osascript -l JavaScript"))
	if err != nil || m.RecommendedMaxWorkingSetSize != 12713115648 || !m.HasUnifiedMemory || m.Name != "Apple M1 Pro" {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := parseMetalProbe("osascript: execution error: -2700"); err == nil {
		t.Error("no JSON is an error")
	}
	if m, err := parseMetalProbe("{}"); err != nil || m.RecommendedMaxWorkingSetSize != 0 {
		t.Errorf("no device: zero, which detection treats as unknown: %+v %v", m, err)
	}
	for _, want := range []string{"MTLCreateSystemDefaultDevice", "recommendedMaxWorkingSetSize", "JSON.stringify"} {
		if !strings.Contains(metalProbeScript, want) {
			t.Errorf("the Metal query must use %s", want)
		}
	}
}

// --- Linux: /proc, /etc, sysfs, KFD, pci.ids, systemd ---------------------

func linuxFS(t *testing.T, fixture string) fstest.MapFS {
	t.Helper()
	f := newFakeEnv("linux", "amd64")
	loadFixture(t, f, fixture)
	return f.fsys
}

func TestLinuxTextFiles(t *testing.T) {
	if got := osReleaseName("NAME=\"Fedora Linux\"\nVERSION=\"42 (Workstation Edition)\"\n"); got != "Fedora Linux 42 (Workstation Edition)" {
		t.Errorf("osReleaseName fallback: %q", got)
	}
	if got := osReleaseName(""); got != Unknown {
		t.Errorf("empty os-release: %q", got)
	}
	if n, ok := meminfoTotal("MemFree: 1 kB\nMemTotal:       65766484 kB\n"); !ok || n != 65766484*1024 {
		t.Errorf("meminfo: %d %v", n, ok)
	}
	if _, ok := meminfoTotal("MemFree: 1 kB\n"); ok {
		t.Error("no MemTotal is unknown")
	}
	fsys := linuxFS(t, "linux/ubuntu-macpro-d700")
	c := parseCPUInfo(string(fsys["proc/cpuinfo"].Data))
	if c.model != "Intel(R) Xeon(R) CPU E5-1650 v2 @ 3.50GHz" || c.logical != 12 || c.physical != 6 {
		t.Errorf("cpuinfo: %+v", c)
	}
	if n := sysfsPhysicalCores(fsys); n != 6 {
		t.Errorf("sibling sets: %d", n)
	}
	if m := nvidiaProcVersionRE.FindStringSubmatch(string(linuxFS(t, "linux/ubuntu-rtx3090")["proc/driver/nvidia/version"].Data)); m == nil || m[1] != "575.64.05" {
		t.Errorf("NVIDIA kernel module version: %v", m)
	}
	if m := nvidiaProcVersionRE.FindStringSubmatch("NVRM version: NVIDIA UNIX Open Kernel Module for x86_64  580.82.07  Release Build"); m == nil || m[1] != "580.82.07" {
		t.Errorf("open kernel module version: %v", m)
	}
}

func TestReadPCIDisplayDevices(t *testing.T) {
	devs, err := readPCIDisplayDevices(linuxFS(t, "linux/ubuntu-rx7900xtx"))
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 {
		t.Fatalf("only display-class functions: %+v", devs)
	}
	d := devs[0]
	if d.addr != "0000:03:00.0" || d.vendor != "1002" || d.dev != "744c" || d.subVendor != "1da2" || d.driver != "amdgpu" ||
		d.vramTotal != 25753026560 || !d.dgpuMarker || !d.gttKnown {
		t.Fatalf("%+v", d)
	}

	devs, _ = readPCIDisplayDevices(linuxFS(t, "linux/desktop-nvidia-no-driver"))
	if len(devs) != 1 || devs[0].driver != "" {
		t.Fatalf("a card with no driver bound is still listed: %+v", devs)
	}
	if _, err := readPCIDisplayDevices(fstest.MapFS{}); err == nil {
		t.Fatal("an unreadable PCI tree is an error, so the caller says unknown rather than none")
	}
}

func TestAMDGPUIntegratedSignal(t *testing.T) {
	cases := []struct {
		d                 pciDevice
		integrated, known bool
	}{
		{pciDevice{driver: "amdgpu", dgpuMarker: true}, false, true},
		{pciDevice{driver: "amdgpu", vramTotal: 512 << 20, vramKnown: true, gttTotal: 15 << 30, gttKnown: true}, true, true},
		{pciDevice{driver: "amdgpu", vramTotal: 96 << 30, vramKnown: true, gttTotal: 16 << 30, gttKnown: true}, false, false}, // Strix Halo with a big carve-out: undecided here
		{pciDevice{driver: "amdgpu", vramTotal: 2 << 30, vramKnown: true, gttTotal: 6 << 30, gttKnown: true}, false, false},
		{pciDevice{driver: "radeon", vramTotal: 512 << 20, vramKnown: true, gttTotal: 15 << 30, gttKnown: true}, false, false},
	}
	for i, c := range cases {
		in, k := amdgpuIntegrated(c.d)
		if in != c.integrated || k != c.known {
			t.Errorf("case %d: integrated=%v known=%v", i, in, k)
		}
	}
}

func TestKFDTargets(t *testing.T) {
	for v, want := range map[uint64]string{100300: "gfx1030", 90010: "gfx90a", 110003: "gfx1103", 120001: "gfx1201", 90400: "gfx940", 0: "", 101600: ""} {
		if got := gfxFromKFD(v); got != want {
			t.Errorf("gfxFromKFD(%d) = %q, want %q", v, got, want)
		}
	}
	m := kfdGFXTargets(linuxFS(t, "linux/ubuntu-rx7900xtx"))
	if len(m) != 1 || m["0000:03:00.0"] != "gfx1100" {
		t.Fatalf("GPU nodes only, keyed by PCI address: %v", m)
	}
	if m := kfdGFXTargets(linuxFS(t, "linux/laptop-amd-780m")); m["0000:c4:00.0"] != "gfx1103" {
		t.Fatalf("location_id 50176 is bus c4: %v", m)
	}
}

func TestLookupPCINames(t *testing.T) {
	fsys := linuxFS(t, "linux/ubuntu-macpro-d700")
	keys := []pciKey{{"1002", "6798", "", ""}, {"1002", "6798", "106b", "0128"}, {"1002", "744c", "", ""}, {"1a03", "2000", "", ""}, {"dead", "beef", "", ""}}
	names := lookupPCINames(fsys, keys)
	if names[keys[0]] != "Tahiti XT [Radeon HD 7970/8970 OEM / R9 280X]" || names[keys[1]] != "FirePro D700" ||
		names[keys[2]] != "Navi 31 [Radeon RX 7900 XT/7900 XTX/7900 GRE/7900M]" || names[keys[3]] != "ASPEED Graphics Family" {
		t.Fatalf("%v", names)
	}
	if _, ok := names[keys[4]]; ok {
		t.Fatal("unknown ids stay unnamed")
	}

	// Compressed, as some distributions ship it; and nothing after the
	// class list is read as a vendor.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(fsys["usr/share/misc/pci.ids"].Data)
	_ = zw.Close()
	gz := fstest.MapFS{"usr/share/misc/pci.ids.gz": {Data: buf.Bytes()}}
	if n := lookupPCINames(gz, keys[2:3])[keys[2]]; n != "Navi 31 [Radeon RX 7900 XT/7900 XTX/7900 GRE/7900M]" {
		t.Fatalf("gzipped pci.ids: %q", n)
	}
	if len(lookupPCINames(fstest.MapFS{}, keys)) != 0 {
		t.Fatal("no pci.ids: no names, no error")
	}
}

func TestLinuxDisplayName(t *testing.T) {
	d := pciDevice{vendor: "1002", dev: "744c"}
	cases := []struct {
		v              Vendor
		dev, sub, want string
	}{
		{VendorNVIDIA, "GA102 [GeForce RTX 3090]", "", "NVIDIA GeForce RTX 3090"},
		{VendorAMD, "Tahiti XT [Radeon HD 7970/8970 OEM / R9 280X]", "FirePro D700", "AMD FirePro D700"},
		{VendorAMD, "Navi 21 [Radeon RX 6800/6800 XT / 6900 XT]", "", "AMD Radeon RX 6800/6800 XT / 6900 XT"},
		{VendorAMD, "Phoenix1", "", "AMD Phoenix1"},
		{VendorIntel, "DG2 [Arc A770]", "", "Intel Arc A770"},
		{VendorAMD, "", "", "AMD graphics device (PCI 1002:744c)"},
	}
	for _, c := range cases {
		if got := linuxDisplayName(c.v, c.dev, c.sub, d); got != c.want {
			t.Errorf("linuxDisplayName(%q, %q) = %q, want %q", c.dev, c.sub, got, c.want)
		}
	}
}

func TestSystemdOllamaModels(t *testing.T) {
	if got := systemdOllamaModels(linuxFS(t, "linux/server-aspeed-only")); got != "/srv/ollama/models" {
		t.Errorf("drop-in overrides the unit: %q", got)
	}
	words := splitSystemdWords(`"OLLAMA_HOST=0.0.0.0" OLLAMA_MODELS='/data/my models' PATH=/usr/bin`)
	if len(words) != 3 || words[1] != "OLLAMA_MODELS=/data/my models" {
		t.Errorf("quoted assignments: %q", words)
	}
}

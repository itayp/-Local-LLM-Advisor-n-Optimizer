// Package hardware describes the machine the advisor runs on: operating
// system, processor, memory, graphics, the disk that holds the models, and a
// plain-language class of machine (Tier) with one sentence for the UI.
//
// The contract, from step 1 and probe0: a value the code cannot read stays
// unknown — "unknown" for strings, 0 with a *Known flag false for numbers —
// never a default, never a guess. Problems says what could not be read and
// why; it feeds a "Show details" disclosure, never an error screen.
//
// Layout of the package:
//
//	hardware.go     the types and Detect
//	env.go          everything that touches the machine (commands, files,
//	                environment, disk), behind an interface so every OS path
//	                runs against fixtures on every CI runner
//	winprobe.go     Windows: one PowerShell query (CIM + display-class registry) + nvidia-smi
//	macprobe.go     macOS: sysctl, sw_vers, system_profiler, Metal via osascript
//	linuxprobe.go   Linux: /proc, /etc/os-release, PCI and DRM sysfs, KFD, pci.ids, nvidia-smi
//	nvidiasmi.go    the nvidia-smi CSV parser, shared by Windows and Linux
//	support.go      data/hardware/runtime-support.yaml: expected backend, AMD
//	                LLVM targets, integrated-or-discrete by name
//	derive.go       ordering, the GPU memory budget, tier, summary, notes, fingerprint
//	storage.go      free space on the volume that holds (or will hold) the models
//	common.go       vendor ids and names, SMBIOS chassis types
//	sys_*.go        the only OS-specific code: free disk space, hidden console windows
//
// Tests: testdata/<os>/<machine>.txtar are machines as their tools print
// them; testdata/golden/*.json are the profiles detection derives from them
// (go test -run Scenario -update rewrites them; review the diff).
//
// The numbers here are read from the OS, so they are neither estimated nor
// measured in the sense of product rule 4 and carry `source:"n/a"`. What is
// derived from them — the fit, the speed — lives in internal/estimate as
// figures with provenance.
package hardware

import (
	"context"
	"runtime"
	"strings"
)

// Unknown is the value of every string field the detector could not read.
const Unknown = "unknown"

// Vendor of a GPU.
type Vendor string

const (
	VendorUnknown  Vendor = "unknown"
	VendorNVIDIA   Vendor = "nvidia"
	VendorAMD      Vendor = "amd"
	VendorIntel    Vendor = "intel"
	VendorApple    Vendor = "apple"
	VendorQualcomm Vendor = "qualcomm" // Snapdragon laptops on Windows: detected and labelled, not checked
)

// RuntimePath is how a runtime (Ollama today) drives a GPU. It is shared
// with internal/backend: hardware predicts it per GPU (ExpectedBackend), the
// backend records what actually happened (step 3). The two are shown side by
// side when they differ.
type RuntimePath string

const (
	PathUnknown RuntimePath = "unknown"
	PathCUDA    RuntimePath = "cuda"
	PathMetal   RuntimePath = "metal"
	PathROCm    RuntimePath = "rocm"
	PathVulkan  RuntimePath = "vulkan"
	PathCPU     RuntimePath = "cpu"
	PathNone    RuntimePath = "none" // no GPU path exists for this device
)

// UsesGPU reports whether the path runs the model on a graphics device.
func (p RuntimePath) UsesGPU() bool {
	switch p {
	case PathCUDA, PathMetal, PathROCm, PathVulkan:
		return true
	}
	return false
}

// Tier is the plain-language class of the machine. Weak hardware is a tier,
// not an error (product rule 6). The GPU tiers are sized by the memory the
// best usable graphics device offers (Profile.GPUUsableBytes); the
// thresholds and why they sit where they do are in derive.go.
type Tier string

const (
	// TierUnknown: the detector could not tell — for example a graphics card
	// Ollama should be able to use whose memory could not be read.
	TierUnknown Tier = "unknown"
	// TierCPUOnly: no graphics device Ollama can use; models run on the
	// processor.
	TierCPUOnly Tier = "cpu_only"
	// TierIntegrated: only graphics built into the processor (Intel, AMD),
	// which share system memory.
	TierIntegrated Tier = "integrated"
	TierGPUSmall   Tier = "gpu_small"  // under 7 GiB on the best device
	TierGPUMedium  Tier = "gpu_medium" // 7 to 14 GiB
	TierGPULarge   Tier = "gpu_large"  // 14 to 30 GiB
	TierGPUXL      Tier = "gpu_xl"     // 30 GiB and more
)

// GPU is one graphics device.
type GPU struct {
	Vendor Vendor `json:"vendor"`
	Name   string `json:"name"`

	// VRAMBytes is the device's own memory as the OS or driver reports it.
	// For an integrated GPU it is the dedicated slice only (often a BIOS
	// carve-out); for Apple Silicon it does not apply — see
	// Profile.GPUUsableBytes.
	VRAMBytes  uint64 `json:"vram_bytes" source:"n/a"` // read from the OS/driver; 0 when unknown
	VRAMKnown  bool   `json:"vram_known"`
	VRAMSource string `json:"vram_source"` // where the number came from, or why there is none

	DriverVersion string `json:"driver_version"` // Unknown if unread

	IsIntegrated    bool `json:"is_integrated"`    // shares system memory (iGPU, Apple Silicon)
	IntegratedKnown bool `json:"integrated_known"` // false: neither the OS nor the name table could say

	PCIID             string `json:"pci_id,omitempty"`             // vendor:device, lower-case hex ("10de:2c05")
	ComputeCapability string `json:"compute_capability,omitempty"` // NVIDIA only ("8.9")
	GFXTarget         string `json:"gfx_target,omitempty"`         // AMD only, the LLVM target ("gfx1100")
	LinuxDriver       string `json:"linux_driver,omitempty"`       // Linux: kernel driver bound to the card; "none" when no driver is bound

	// ExpectedBackend is what the runtime should be able to use for this
	// card, from vendor + model against data/hardware/runtime-support.yaml.
	// Step 3 records what actually happened on the backend row.
	ExpectedBackend       RuntimePath `json:"expected_backend"`
	ExpectedBackendReason string      `json:"expected_backend_reason"` // one plain sentence
	ExpectedBackendRule   string      `json:"expected_backend_rule"`   // the rule id in runtime-support.yaml

	Note string `json:"note,omitempty"` // plain words, when something about this card needs saying

	// Detection internals. Not serialised; cleared before Detect returns.
	busID         string // PCI address "0000:01:00.0", to merge two sources for one card
	rawName       string // Linux: the pci.ids device name, which carries the chip codename, for matching
	driverMissing bool   // no vendor driver: Linux none bound, Windows Basic Display
	problemCode   int    // Windows Device Manager problem code (0 = working)
	actionable    bool   // the expected-backend reason tells the user what to do
}

// CPU describes the processor. CPU inference depends on the vector
// extensions, so they are first-class.
type CPU struct {
	Model         string `json:"model"`
	CoresPhysical int    `json:"cores_physical" source:"n/a"` // a count; 0 when unknown
	CoresLogical  int    `json:"cores_logical" source:"n/a"`  // a count; 0 when unknown
	// HasAVX2 / HasAVX512 are x86 facts (AVX-512 means AVX-512F, with the OS
	// saving its registers). On an ARM processor both are false and
	// VectorKnown is true: the instructions do not exist there.
	HasAVX2     bool `json:"has_avx2"`
	HasAVX512   bool `json:"has_avx512"`
	VectorKnown bool `json:"vector_known"` // false: the flags could not be read
}

// Storage is the volume where the backend keeps its models.
type Storage struct {
	ModelsDir       string `json:"models_dir"`              // OLLAMA_MODELS, or Ollama's default folder for this OS
	ModelsDirSource string `json:"models_dir_source"`       // how ModelsDir was chosen, in words
	ModelsDirExists bool   `json:"models_dir_exists"`       // false: not created yet; free space is read from the nearest existing parent
	FreeBytes       uint64 `json:"free_bytes" source:"n/a"` // read from the OS; 0 when unknown
	FreeKnown       bool   `json:"free_known"`
}

// Profile is the machine as the advisor understands it. It is stored on
// every daemon start (hardware_profiles) so that benchmarks stay
// attributable to the hardware they ran on.
type Profile struct {
	OS        string `json:"os"`               // runtime.GOOS: linux | darwin | windows
	OSVersion string `json:"os_version"`       // "macOS 26.6.2 (25G83)", "Ubuntu 24.04.3 LTS", "Windows 11 Pro 24H2 (build 26100.4652)"; Unknown if unread
	Kernel    string `json:"kernel,omitempty"` // Linux kernel release, when read
	// Arch is the machine's architecture. It is runtime.GOARCH except when
	// the Intel build runs under Rosetta on Apple Silicon, where it is arm64
	// and Notes says so.
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`

	CPU      CPU    `json:"cpu"`
	RAMBytes uint64 `json:"ram_bytes" source:"n/a"` // read from the OS; 0 when unknown
	RAMKnown bool   `json:"ram_known"`

	GPUs []GPU `json:"gpus"` // primary first; an iGPU beside a discrete GPU is listed and marked

	// GPUUsableBytes is the memory the best graphics device can give a model.
	// Apple Silicon: unified memory, and macOS caps the GPU's working set
	// below total RAM — the value is what the OS says (derivation in
	// macprobe.go). Discrete GPUs: the largest single device, because a model
	// runs on one device unless the runtime splits it — VRAM is never summed.
	UnifiedMemory   bool   `json:"unified_memory"`
	GPUUsableBytes  uint64 `json:"gpu_usable_bytes" source:"n/a"` // read/derived from the OS; 0 when unknown
	GPUUsableKnown  bool   `json:"gpu_usable_known"`
	GPUUsableSource string `json:"gpu_usable_source"` // how the number was derived, or why it is unknown

	Storage     Storage `json:"storage"`
	IsLaptop    bool    `json:"is_laptop"`
	LaptopKnown bool    `json:"laptop_known"`

	// Tier and Summary are what the UI shows first: the class of machine and
	// one plain-language sentence about it.
	Tier    Tier   `json:"tier"`
	Summary string `json:"summary"`

	// Notes are plain-language facts worth telling the user (several graphics
	// cards, an old driver, the wrong build under Rosetta).
	Notes []string `json:"notes,omitempty"`
	// FilteredAdapters are display devices that cannot run a model (remote
	// desktop and virtual displays, server management chips), named so the
	// list of GPUs is not a mystery.
	FilteredAdapters []string `json:"filtered_adapters,omitempty"`
	// ExpectationsFrom names the runtime release the expected backends were
	// checked against ("Ollama v0.34.2").
	ExpectationsFrom string `json:"expectations_from"`

	// Problems lists what could not be read and why, for the "Show details"
	// disclosure — never for an error screen.
	Problems []string `json:"problems,omitempty"`

	// Detection internals. Not serialised.
	macOSVersion string // "26.6.2", for the runtime's OS floor
	windowsBuild int    // 26100, likewise
	gpuListRead  bool   // the OS's device list was read, so an empty GPUs means "none", not "unknown"
}

// Detect reads the machine. It never fails for want of a tool or a file:
// what it cannot read is unknown and named in Problems. The error is only
// the context's, when ctx ends before detection does.
func Detect(ctx context.Context) (Profile, error) {
	return detect(ctx, newSystemEnv())
}

// detect is Detect against any environment; the tests pass fixtures.
func detect(ctx context.Context, e env) (Profile, error) {
	p := Profile{
		OS:        e.goos(),
		OSVersion: Unknown,
		Arch:      e.goarch(),
		Hostname:  Unknown,
		CPU:       CPU{Model: Unknown},
		GPUs:      []GPU{},
		Storage:   Storage{ModelsDir: Unknown},
		Tier:      TierUnknown,
	}
	if h, err := e.hostname(); err == nil && strings.TrimSpace(h) != "" {
		p.Hostname = strings.TrimSpace(h)
	}

	switch p.OS {
	case "windows":
		detectWindows(ctx, e, &p)
	case "darwin":
		detectDarwin(ctx, e, &p)
	case "linux":
		detectLinux(ctx, e, &p)
	default:
		p.problem("hardware detection does not know this operating system (%s)", p.OS)
	}

	// The vector extensions, when the OS path did not already settle them
	// (Rosetta does): CPUID on x86, not applicable on ARM.
	if !p.CPU.VectorKnown {
		switch p.Arch {
		case "amd64", "386":
			if avx2, avx512, ok := e.cpuVector(); ok {
				p.CPU.HasAVX2, p.CPU.HasAVX512, p.CPU.VectorKnown = avx2, avx512, true
			} else {
				p.problem("could not read the processor's vector extensions")
			}
		case "arm64", "arm":
			p.CPU.VectorKnown = true // AVX does not exist on ARM
		}
	}

	detectStorage(ctx, e, &p)
	finish(&p)

	if err := ctx.Err(); err != nil {
		p.problem("detection was cut short: %v", err)
		return p, err
	}
	return p, nil
}

// systemGOOS/GOARCH exist so the real environment reads them in one place.
var (
	systemGOOS   = runtime.GOOS
	systemGOARCH = runtime.GOARCH
)

func (p *Profile) problem(format string, args ...any) {
	p.Problems = append(p.Problems, sprintf(format, args...))
}

func (p *Profile) note(format string, args ...any) {
	p.Notes = append(p.Notes, sprintf(format, args...))
}

// cleanName tidies a device or processor name as tools print it: trims,
// collapses runs of spaces. Trademark marks are kept for display; support.go
// strips them for matching.
func cleanName(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

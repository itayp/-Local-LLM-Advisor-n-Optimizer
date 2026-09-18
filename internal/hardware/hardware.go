// Package hardware describes the machine the advisor runs on.
//
// Step 1 ships the Profile shape and a Detect that knows nothing: every
// string is "unknown", every number is 0 with its Known flag false. Step 2
// fills Detect in for the real-world matrix (BUILD_PLAN.md, step 2) and must
// keep this contract: a value the code cannot read stays unknown — never a
// default, never a guess (probe0 established the habit; the product keeps it).
//
// The numbers here are read from the OS, so they are neither estimated nor
// measured in the sense of product rule 4 and carry `source:"n/a"`. What is
// derived from them — the fit, the speed — lives in internal/estimate as
// figures with provenance.
package hardware

import (
	"context"
	"runtime"
)

// Unknown is the value of every string field the detector could not read.
const Unknown = "unknown"

// Vendor of a GPU.
type Vendor string

const (
	VendorUnknown Vendor = "unknown"
	VendorNVIDIA  Vendor = "nvidia"
	VendorAMD     Vendor = "amd"
	VendorIntel   Vendor = "intel"
	VendorApple   Vendor = "apple"
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

// Tier is the plain-language class of the machine, derived by step 2. Weak
// hardware is a tier, not an error (product rule 6).
type Tier string

// TierUnknown is the only tier step 1 knows. Step 2 adds the rest.
const TierUnknown Tier = "unknown"

// GPU is one graphics device.
type GPU struct {
	Vendor        Vendor `json:"vendor"`
	Name          string `json:"name"`
	VRAMBytes     uint64 `json:"vram_bytes" source:"n/a"` // read from the OS/driver; 0 when unknown
	VRAMKnown     bool   `json:"vram_known"`
	DriverVersion string `json:"driver_version"`
	IsIntegrated  bool   `json:"is_integrated"`
	// ExpectedBackend is what the runtime should be able to use for this
	// card, from vendor + model against data/hardware/runtime-support.yaml
	// (step 2). Step 3 records what actually happened on the backend row.
	ExpectedBackend RuntimePath `json:"expected_backend"`
	Note            string      `json:"note,omitempty"`
}

// CPU describes the processor. CPU inference depends on the vector
// extensions, so they are first-class.
type CPU struct {
	Model         string `json:"model"`
	CoresPhysical int    `json:"cores_physical" source:"n/a"` // a count; 0 when unknown
	CoresLogical  int    `json:"cores_logical" source:"n/a"`  // a count; 0 when unknown
	HasAVX2       bool   `json:"has_avx2"`
	HasAVX512     bool   `json:"has_avx512"`
	VectorKnown   bool   `json:"vector_known"` // false: AVX flags could not be read
}

// Storage is the volume where the backend keeps its models.
type Storage struct {
	ModelsDir string `json:"models_dir"`              // the default Ollama models folder per OS, or OLLAMA_MODELS
	FreeBytes uint64 `json:"free_bytes" source:"n/a"` // read from the OS; 0 when unknown
	FreeKnown bool   `json:"free_known"`
}

// Profile is the machine as the advisor understands it. It is stored on
// every daemon start (hardware_profiles) so that benchmarks stay
// attributable to the hardware they ran on.
type Profile struct {
	OS        string `json:"os"`         // runtime.GOOS: linux | darwin | windows
	OSVersion string `json:"os_version"` // "macOS 26.6.2", "Ubuntu 24.04", "Windows 11 23H2"; Unknown if unread
	Arch      string `json:"arch"`       // runtime.GOARCH
	Hostname  string `json:"hostname"`

	CPU      CPU    `json:"cpu"`
	RAMBytes uint64 `json:"ram_bytes" source:"n/a"` // read from the OS; 0 when unknown
	RAMKnown bool   `json:"ram_known"`

	GPUs []GPU `json:"gpus"` // primary first; an iGPU beside a discrete GPU is listed and marked

	// Apple Silicon is unified memory and macOS caps the GPU's working set
	// below total RAM. GPUUsableBytes is what the OS actually says (step 2
	// writes the derivation next to the code); on discrete-GPU machines it is
	// the largest single device — a model runs on one device unless the
	// runtime splits it, so VRAM is never summed across devices.
	UnifiedMemory  bool   `json:"unified_memory"`
	GPUUsableBytes uint64 `json:"gpu_usable_bytes" source:"n/a"` // read/derived from the OS; 0 when unknown
	GPUUsableKnown bool   `json:"gpu_usable_known"`

	Storage     Storage `json:"storage"`
	IsLaptop    bool    `json:"is_laptop"`
	LaptopKnown bool    `json:"laptop_known"`

	// Tier and Summary are what the UI shows first: the class of machine and
	// one plain-language sentence about it.
	Tier    Tier   `json:"tier"`
	Summary string `json:"summary"`

	// Problems lists what could not be read and why, for the "Show details"
	// disclosure — never for an error screen.
	Problems []string `json:"problems,omitempty"`
}

// Detect returns the machine's Profile.
//
// Step 1: a stub. It labels only what the Go runtime knows for certain (OS,
// architecture) and reports everything else as unknown. Step 2 replaces the
// body, not the signature.
func Detect(ctx context.Context) (Profile, error) {
	_ = ctx
	return Profile{
		OS:        runtime.GOOS,
		OSVersion: Unknown,
		Arch:      runtime.GOARCH,
		Hostname:  Unknown,
		CPU: CPU{
			Model: Unknown,
		},
		GPUs: []GPU{},
		Storage: Storage{
			ModelsDir: Unknown,
		},
		Tier:     TierUnknown,
		Summary:  "This computer has not been looked at yet.",
		Problems: []string{"hardware detection is not implemented yet (build plan step 2)"},
	}, nil
}

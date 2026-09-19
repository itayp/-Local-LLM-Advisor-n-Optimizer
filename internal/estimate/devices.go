package estimate

import (
	"bytes"
	"fmt"
	"io/fs"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-yaml"

	"advisor/data"
	"advisor/internal/hardware"
)

// DeviceFile mirrors data/hardware/gpus.yaml. The file's header comment is
// the schema of record; this type follows it field for field.
type DeviceFile struct {
	Sources      map[string]string `yaml:"sources"`
	GPUs         []GPURow          `yaml:"gpus"`
	SystemMemory []SystemMemoryRow `yaml:"system_memory"`
}

// GPURow is one graphics part's memory bandwidth, with where it came from.
type GPURow struct {
	ID           string          `yaml:"id"`
	Vendor       hardware.Vendor `yaml:"vendor"`
	Name         []string        `yaml:"name"`
	MinVRAMGiB   float64         `yaml:"min_vram_gib"`
	MaxVRAMGiB   float64         `yaml:"max_vram_gib"`
	GPUCores     []int           `yaml:"gpu_cores"`
	BandwidthGBs float64         `yaml:"bandwidth_gbs"`
	DataRateGbps float64         `yaml:"data_rate_gbps"`
	BusWidthBits int             `yaml:"bus_width_bits"`

	Efficiency        []float64 `yaml:"efficiency"`
	EfficiencySource  string    `yaml:"efficiency_source"`
	PromptRatio       []float64 `yaml:"prompt_ratio"`
	PromptRatioSource string    `yaml:"prompt_ratio_source"`

	Source  string `yaml:"source"`
	Checked string `yaml:"checked"`
	Note    string `yaml:"note"`

	nameRE []*regexp.Regexp
}

// SystemMemoryRow is the memory a processor family supports, as a range.
type SystemMemoryRow struct {
	ID      string   `yaml:"id"`
	CPU     []string `yaml:"cpu"`
	LowGBs  float64  `yaml:"low_gbs"`
	HighGBs float64  `yaml:"high_gbs"`
	Memory  string   `yaml:"memory"`
	Source  string   `yaml:"source"`
	Checked string   `yaml:"checked"`
	Note    string   `yaml:"note"`

	cpuRE []*regexp.Regexp
}

// DeviceTable is the parsed, validated file.
type DeviceTable struct {
	file DeviceFile
}

// GPUSpec is what the table knows about one detected graphics device.
type GPUSpec struct {
	RowID        string
	BandwidthGBs float64
	Efficiency   *Range // set only where a published measurement overrides the path's range
	PromptRatio  *Range
	Source       string // the citation, resolved
	Note         string
}

// MemorySpec is the range of memory bandwidth a processor's platform offers.
type MemorySpec struct {
	RowID   string
	LowGBs  float64
	HighGBs float64
	Memory  string
	Source  string
}

var (
	devicesOnce sync.Once
	devicesData *DeviceTable
	devicesErr  error
)

// DefaultDevices parses the table embedded in the binary, once.
func DefaultDevices() (*DeviceTable, error) {
	devicesOnce.Do(func() {
		b, err := fs.ReadFile(data.Files, data.DevicesPath)
		if err != nil {
			devicesErr = fmt.Errorf("estimate: reading the embedded %s: %w", data.DevicesPath, err)
			return
		}
		devicesData, devicesErr = ParseDevices(b)
	})
	return devicesData, devicesErr
}

// ParseDevices decodes gpus.yaml strictly (an unknown key is an error, so a
// typo cannot silently drop a condition) and validates every row.
func ParseDevices(b []byte) (*DeviceTable, error) {
	var f DeviceFile
	dec := yaml.NewDecoder(bytes.NewReader(b), yaml.DisallowUnknownField())
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("estimate: gpus.yaml: %w", err)
	}
	bad := func(format string, args ...any) error {
		return fmt.Errorf("estimate: gpus.yaml: "+format, args...)
	}
	if len(f.GPUs) == 0 || len(f.SystemMemory) == 0 {
		return nil, bad("gpus and system_memory must both have rows")
	}
	vendors := map[hardware.Vendor]bool{hardware.VendorNVIDIA: true, hardware.VendorAMD: true, hardware.VendorIntel: true, hardware.VendorApple: true}
	seen := map[string]bool{}
	for i := range f.GPUs {
		r := &f.GPUs[i]
		where := fmt.Sprintf("gpus[%d] (%s)", i, r.ID)
		if r.ID == "" || seen[r.ID] {
			return nil, bad("%s: id missing or duplicated", where)
		}
		seen[r.ID] = true
		if !vendors[r.Vendor] {
			return nil, bad("%s: vendor %q", where, r.Vendor)
		}
		if len(r.Name) == 0 {
			return nil, bad("%s: at least one name pattern", where)
		}
		var err error
		if r.nameRE, err = compilePatterns(r.Name); err != nil {
			return nil, bad("%s: %v", where, err)
		}
		if r.BandwidthGBs <= 0 || r.BandwidthGBs > 5000 {
			return nil, bad("%s: bandwidth_gbs %v is not a memory bandwidth in GB/s", where, r.BandwidthGBs)
		}
		if (r.DataRateGbps > 0) != (r.BusWidthBits > 0) {
			return nil, bad("%s: data_rate_gbps and bus_width_bits go together", where)
		}
		if r.DataRateGbps > 0 {
			derived := r.DataRateGbps * float64(r.BusWidthBits) / 8
			if math.Abs(derived-r.BandwidthGBs) > 0.01*r.BandwidthGBs {
				return nil, bad("%s: bandwidth_gbs %v does not equal data_rate_gbps × bus_width_bits ÷ 8 = %v", where, r.BandwidthGBs, derived)
			}
		}
		if r.MinVRAMGiB < 0 || r.MaxVRAMGiB < 0 || (r.MaxVRAMGiB > 0 && r.MinVRAMGiB > r.MaxVRAMGiB) {
			return nil, bad("%s: min_vram_gib / max_vram_gib", where)
		}
		if err := checkRange(r.Efficiency, 0.05, 1.0, r.EfficiencySource); err != nil {
			return nil, bad("%s: efficiency: %v", where, err)
		}
		if err := checkRange(r.PromptRatio, 1, 200, r.PromptRatioSource); err != nil {
			return nil, bad("%s: prompt_ratio: %v", where, err)
		}
		if f.Sources[r.Source] == "" {
			return nil, bad("%s: source %q is not one of sources", where, r.Source)
		}
		if err := checkDate(r.Checked); err != nil {
			return nil, bad("%s: %v", where, err)
		}
	}
	for i := range f.SystemMemory {
		r := &f.SystemMemory[i]
		where := fmt.Sprintf("system_memory[%d] (%s)", i, r.ID)
		if r.ID == "" || seen[r.ID] {
			return nil, bad("%s: id missing or duplicated", where)
		}
		seen[r.ID] = true
		if len(r.CPU) == 0 {
			return nil, bad("%s: at least one cpu pattern", where)
		}
		var err error
		if r.cpuRE, err = compilePatterns(r.CPU); err != nil {
			return nil, bad("%s: %v", where, err)
		}
		if r.LowGBs <= 0 || r.HighGBs < r.LowGBs || r.HighGBs > 2000 {
			return nil, bad("%s: low_gbs %v .. high_gbs %v is not a range of memory bandwidth", where, r.LowGBs, r.HighGBs)
		}
		if strings.TrimSpace(r.Memory) == "" {
			return nil, bad("%s: memory (the configuration, in words) is required", where)
		}
		if f.Sources[r.Source] == "" {
			return nil, bad("%s: source %q is not one of sources", where, r.Source)
		}
		if err := checkDate(r.Checked); err != nil {
			return nil, bad("%s: %v", where, err)
		}
	}
	return &DeviceTable{file: f}, nil
}

func checkRange(v []float64, min, max float64, source string) error {
	if len(v) == 0 {
		if source != "" {
			return fmt.Errorf("a source without a range")
		}
		return nil
	}
	if len(v) != 2 || v[0] < min || v[1] > max || v[0] > v[1] {
		return fmt.Errorf("%v is not a [low, high] pair within %v..%v", v, min, max)
	}
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("an override needs the measurement behind it (the *_source key)")
	}
	return nil
}

func checkDate(s string) error {
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return fmt.Errorf("checked date %q is not YYYY-MM-DD", s)
	}
	return nil
}

func compilePatterns(list []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(list))
	for _, p := range list {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// File returns the parsed file, for tests and the calibration tool.
func (t *DeviceTable) File() DeviceFile { return t.file }

// GPU looks a detected device up. ok is false when the table does not know
// the part — then there is no speed estimate, and the caller says so.
//
// An ordinary name takes the first row whose conditions hold. A name that
// lists several cards ("Radeon RX 7900 XT/7900 XTX/7900 GRE", which is how
// Linux's pci.ids names a chip id when the board's own name is missing)
// takes the slowest of every row that matches, and says so.
func (t *DeviceTable) GPU(g hardware.GPU) (GPUSpec, bool) {
	name := hardware.MatchName(g.Name)
	ambiguous := strings.Contains(name, "/")
	var best *GPURow
	matches := 0
	for i := range t.file.GPUs {
		r := &t.file.GPUs[i]
		if !r.matches(g, name) {
			continue
		}
		matches++
		if !ambiguous {
			best = r
			break
		}
		if best == nil || r.BandwidthGBs < best.BandwidthGBs {
			best = r
		}
	}
	if best == nil {
		return GPUSpec{}, false
	}
	spec := GPUSpec{RowID: best.ID, BandwidthGBs: best.BandwidthGBs, Source: t.file.Sources[best.Source], Note: best.Note}
	if len(best.Efficiency) == 2 {
		spec.Efficiency = &Range{best.Efficiency[0], best.Efficiency[1]}
	}
	if len(best.PromptRatio) == 2 {
		spec.PromptRatio = &Range{best.PromptRatio[0], best.PromptRatio[1]}
	}
	if ambiguous && matches > 1 {
		spec.Note = strings.TrimSpace("this device id covers several cards; the slowest of them is assumed. " + spec.Note)
	}
	return spec, true
}

func (r *GPURow) matches(g hardware.GPU, name string) bool {
	if r.Vendor != g.Vendor {
		return false
	}
	hit := false
	for _, re := range r.nameRE {
		if re.MatchString(name) {
			hit = true
			break
		}
	}
	if !hit {
		return false
	}
	if r.MinVRAMGiB > 0 || r.MaxVRAMGiB > 0 {
		if !g.VRAMKnown { // a condition on a value that could not be read does not hold
			return false
		}
		v := float64(g.VRAMBytes) / gib
		if r.MinVRAMGiB > 0 && v < r.MinVRAMGiB {
			return false
		}
		if r.MaxVRAMGiB > 0 && v >= r.MaxVRAMGiB {
			return false
		}
	}
	if len(r.GPUCores) > 0 {
		cores, ok := g.AppleGPUCores()
		if !ok {
			return false
		}
		found := false
		for _, c := range r.GPUCores {
			if c == cores {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// SystemMemory looks a processor up. ok is false when the table does not
// know the family.
func (t *DeviceTable) SystemMemory(cpu hardware.CPU) (MemorySpec, bool) {
	name := hardware.MatchName(cpu.Model)
	if name == "" || name == hardware.Unknown {
		return MemorySpec{}, false
	}
	for i := range t.file.SystemMemory {
		r := &t.file.SystemMemory[i]
		for _, re := range r.cpuRE {
			if re.MatchString(name) {
				return MemorySpec{RowID: r.ID, LowGBs: r.LowGBs, HighGBs: r.HighGBs, Memory: r.Memory, Source: t.file.Sources[r.Source]}, true
			}
		}
	}
	return MemorySpec{}, false
}

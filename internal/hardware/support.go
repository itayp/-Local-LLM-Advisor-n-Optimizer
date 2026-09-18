package hardware

import (
	"bytes"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-yaml"

	"advisor/data"
)

// SupportFile mirrors data/hardware/runtime-support.yaml. The file's header
// comment is the schema of record; this type follows it field for field.
type SupportFile struct {
	OllamaVersion string          `yaml:"ollama_version"`
	OSMinimum     OSMinimum       `yaml:"os_minimum"`
	Rules         []SupportRule   `yaml:"rules"`
	AMDGFX        []GFXRow        `yaml:"amd_gfx"`
	Integrated    []IntegratedRow `yaml:"integrated"`
}

// OSMinimum is the oldest OS release the runtime runs on at all.
type OSMinimum struct {
	Darwin struct {
		Version string `yaml:"version"`
		Checked string `yaml:"checked"`
		Source  string `yaml:"source"`
	} `yaml:"darwin"`
	Windows struct {
		Build   int    `yaml:"build"`
		Checked string `yaml:"checked"`
		Source  string `yaml:"source"`
	} `yaml:"windows"`
}

// SupportRule is one ordered row of `rules`: conditions, then the expected
// backend and the sentence that explains it.
type SupportRule struct {
	ID            string      `yaml:"id"`
	Vendor        Vendor      `yaml:"vendor"`
	OS            []string    `yaml:"os"`
	Arch          []string    `yaml:"arch"`
	GFX           []string    `yaml:"gfx"`
	ComputeMin    string      `yaml:"compute_min"`
	ComputeMax    string      `yaml:"compute_max"`
	DriverBelow   string      `yaml:"driver_below"`
	LinuxDriver   []string    `yaml:"linux_driver"`
	DriverMissing bool        `yaml:"driver_missing"`
	Name          []string    `yaml:"name"`
	Backend       RuntimePath `yaml:"backend"`
	Why           string      `yaml:"why"`
	Actionable    bool        `yaml:"actionable"`
	Checked       string      `yaml:"checked"`
	Source        string      `yaml:"source"`

	nameRE      []*regexp.Regexp
	computeMin  []int
	computeMax  []int
	driverBelow []int
}

// GFXRow maps AMD marketing names to an LLVM target.
type GFXRow struct {
	GFX     string   `yaml:"gfx"`
	Name    []string `yaml:"name"`
	Checked string   `yaml:"checked"`
	Source  string   `yaml:"source"`

	nameRE []*regexp.Regexp
}

// IntegratedRow classifies GPUs of a vendor as integrated or discrete by
// name. A row without names matches every GPU of the vendor.
type IntegratedRow struct {
	Vendor     Vendor   `yaml:"vendor"`
	Name       []string `yaml:"name"`
	Integrated bool     `yaml:"integrated"`
	Checked    string   `yaml:"checked"`
	Source     string   `yaml:"source"`

	nameRE []*regexp.Regexp
}

// gpuFacts is what a rule can look at: the card and the host it sits in.
// Every field may be empty (unknown); a condition on an empty field does
// not hold.
type gpuFacts struct {
	goos, goarch  string
	vendor        Vendor
	name          string // as detected; normalised for matching here
	gfx           string
	compute       string // NVIDIA compute capability, "8.9"
	driverVersion string // NVIDIA driver, "610.62"
	linuxDriver   string
	driverMissing bool
}

var (
	supportOnce sync.Once
	supportData *SupportFile
	supportErr  error
)

// loadSupport parses the embedded runtime-support table once. A broken
// table is a build defect (support_test.go loads it on every runner); at
// runtime it degrades to "unknown" expectations and a named problem.
func loadSupport() (*SupportFile, error) {
	supportOnce.Do(func() {
		b, err := fs.ReadFile(data.Files, data.RuntimeSupportPath)
		if err != nil {
			supportErr = fmt.Errorf("hardware: reading %s: %w", data.RuntimeSupportPath, err)
			return
		}
		supportData, supportErr = parseSupport(b)
	})
	return supportData, supportErr
}

var (
	validBackends = map[RuntimePath]bool{PathCUDA: true, PathMetal: true, PathROCm: true, PathVulkan: true, PathNone: true, PathUnknown: true}
	validVendors  = map[Vendor]bool{VendorNVIDIA: true, VendorAMD: true, VendorIntel: true, VendorApple: true, VendorQualcomm: true}
	validOS       = map[string]bool{"linux": true, "darwin": true, "windows": true}
	validArch     = map[string]bool{"amd64": true, "arm64": true}
	gfxRE         = regexp.MustCompile(`^gfx[0-9a-f]{3,4}$`)
)

// parseSupport decodes and validates the table. Unknown keys are errors, so
// a typo in the YAML cannot silently disable a condition.
func parseSupport(b []byte) (*SupportFile, error) {
	var f SupportFile
	dec := yaml.NewDecoder(bytes.NewReader(b), yaml.DisallowUnknownField())
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("hardware: runtime-support.yaml: %w", err)
	}
	bad := func(format string, args ...any) error {
		return fmt.Errorf("hardware: runtime-support.yaml: "+format, args...)
	}
	if f.OllamaVersion == "" {
		return nil, bad("ollama_version is required")
	}
	if f.OSMinimum.Darwin.Version == "" || f.OSMinimum.Windows.Build == 0 {
		return nil, bad("os_minimum needs darwin.version and windows.build")
	}
	for _, d := range []string{f.OSMinimum.Darwin.Checked, f.OSMinimum.Windows.Checked} {
		if err := checkDate(d); err != nil {
			return nil, bad("os_minimum: %v", err)
		}
	}
	if len(f.Rules) == 0 {
		return nil, bad("no rules")
	}
	seen := map[string]bool{}
	for i := range f.Rules {
		r := &f.Rules[i]
		where := fmt.Sprintf("rule %d (%s)", i, r.ID)
		if r.ID == "" || seen[r.ID] {
			return nil, bad("%s: id missing or duplicated", where)
		}
		seen[r.ID] = true
		if r.Vendor != "" && !validVendors[r.Vendor] {
			return nil, bad("%s: vendor %q", where, r.Vendor)
		}
		if !validBackends[r.Backend] {
			return nil, bad("%s: backend %q", where, r.Backend)
		}
		for _, o := range r.OS {
			if !validOS[o] {
				return nil, bad("%s: os %q", where, o)
			}
		}
		for _, a := range r.Arch {
			if !validArch[a] {
				return nil, bad("%s: arch %q", where, a)
			}
		}
		for _, g := range r.GFX {
			if !gfxRE.MatchString(g) {
				return nil, bad("%s: gfx %q", where, g)
			}
		}
		var err error
		if r.computeMin, err = parseVersionOpt(r.ComputeMin); err != nil {
			return nil, bad("%s: compute_min: %v", where, err)
		}
		if r.computeMax, err = parseVersionOpt(r.ComputeMax); err != nil {
			return nil, bad("%s: compute_max: %v", where, err)
		}
		if r.driverBelow, err = parseVersionOpt(r.DriverBelow); err != nil {
			return nil, bad("%s: driver_below: %v", where, err)
		}
		if r.nameRE, err = compileNames(r.Name); err != nil {
			return nil, bad("%s: %v", where, err)
		}
		if strings.TrimSpace(r.Why) == "" || strings.TrimSpace(r.Source) == "" {
			return nil, bad("%s: why and source are required", where)
		}
		if err := checkDate(r.Checked); err != nil {
			return nil, bad("%s: %v", where, err)
		}
	}
	// The last rule must match everything, so no GPU leaves without an
	// expectation (even if that expectation is "unknown").
	if last := f.Rules[len(f.Rules)-1]; last.Vendor != "" || len(last.OS)+len(last.Arch)+len(last.GFX)+len(last.Name)+len(last.LinuxDriver) > 0 ||
		last.ComputeMin+last.ComputeMax+last.DriverBelow != "" || last.DriverMissing {
		return nil, bad("the last rule must have no conditions (it is the catch-all)")
	}
	for i := range f.AMDGFX {
		row := &f.AMDGFX[i]
		if !gfxRE.MatchString(row.GFX) || len(row.Name) == 0 {
			return nil, bad("amd_gfx %d: gfx %q needs a valid target and names", i, row.GFX)
		}
		var err error
		if row.nameRE, err = compileNames(row.Name); err != nil {
			return nil, bad("amd_gfx %d (%s): %v", i, row.GFX, err)
		}
		if err := checkDate(row.Checked); err != nil {
			return nil, bad("amd_gfx %d (%s): %v", i, row.GFX, err)
		}
	}
	for i := range f.Integrated {
		row := &f.Integrated[i]
		if !validVendors[row.Vendor] {
			return nil, bad("integrated %d: vendor %q", i, row.Vendor)
		}
		var err error
		if row.nameRE, err = compileNames(row.Name); err != nil {
			return nil, bad("integrated %d: %v", i, err)
		}
		if err := checkDate(row.Checked); err != nil {
			return nil, bad("integrated %d: %v", i, err)
		}
	}
	return &f, nil
}

func checkDate(s string) error {
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return fmt.Errorf("checked date %q is not YYYY-MM-DD", s)
	}
	return nil
}

func compileNames(names []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(names))
	for _, n := range names {
		re, err := regexp.Compile("(?i)" + n)
		if err != nil {
			return nil, fmt.Errorf("name %q: %w", n, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// expect returns the first rule whose conditions all hold.
func (f *SupportFile) expect(g gpuFacts) SupportRule {
	name := matchName(g.name)
	for _, r := range f.Rules {
		if r.holds(g, name) {
			return r
		}
	}
	return f.Rules[len(f.Rules)-1] // unreachable: validation requires a catch-all
}

func (r SupportRule) holds(g gpuFacts, name string) bool {
	if r.Vendor != "" && r.Vendor != g.vendor {
		return false
	}
	if len(r.OS) > 0 && !contains(r.OS, g.goos) {
		return false
	}
	if len(r.Arch) > 0 && !contains(r.Arch, g.goarch) {
		return false
	}
	if len(r.GFX) > 0 && (g.gfx == "" || !contains(r.GFX, g.gfx)) {
		return false
	}
	if r.DriverMissing && !g.driverMissing {
		return false
	}
	if len(r.LinuxDriver) > 0 && (g.linuxDriver == "" || !contains(r.LinuxDriver, g.linuxDriver)) {
		return false
	}
	if r.computeMin != nil || r.computeMax != nil {
		cc, err := parseVersion(g.compute)
		if err != nil {
			return false // unknown compute capability: the condition does not hold
		}
		if r.computeMin != nil && compareVersions(cc, r.computeMin) < 0 {
			return false
		}
		if r.computeMax != nil && compareVersions(cc, r.computeMax) > 0 {
			return false
		}
	}
	if r.driverBelow != nil {
		dv, err := parseVersion(g.driverVersion)
		if err != nil || compareVersions(dv, r.driverBelow) >= 0 {
			return false
		}
	}
	if len(r.nameRE) > 0 && !anyMatch(r.nameRE, name) {
		return false
	}
	return true
}

// gfxFor maps an AMD marketing name to its LLVM target, or "".
func (f *SupportFile) gfxFor(name string) string {
	n := matchName(name)
	for _, row := range f.AMDGFX {
		if anyMatch(row.nameRE, n) {
			return row.GFX
		}
	}
	return ""
}

// integratedFor classifies a GPU by vendor and name. known is false when
// no row matches.
func (f *SupportFile) integratedFor(v Vendor, name string) (integrated, known bool) {
	n := matchName(name)
	for _, row := range f.Integrated {
		if row.Vendor != v {
			continue
		}
		if len(row.nameRE) == 0 || anyMatch(row.nameRE, n) {
			return row.Integrated, true
		}
	}
	return false, false
}

// darwinTooOld reports whether macOS productVersion ("13.6.1") is below the
// runtime's floor. ok is false when the version could not be parsed.
func (f *SupportFile) darwinTooOld(productVersion string) (tooOld, ok bool) {
	have, err := parseVersion(productVersion)
	if err != nil {
		return false, false
	}
	want, err := parseVersion(f.OSMinimum.Darwin.Version)
	if err != nil {
		return false, false
	}
	return compareVersions(have, want) < 0, true
}

// windowsTooOld reports whether a Windows build number is below the floor.
func (f *SupportFile) windowsTooOld(build int) (tooOld, ok bool) {
	if build <= 0 {
		return false, false
	}
	return build < f.OSMinimum.Windows.Build, true
}

var trademarkRE = regexp.MustCompile(`(?i)\((r|tm)\)|®|™`)

// matchName is the form names are matched in: trademark marks removed,
// spaces collapsed.
func matchName(s string) string {
	return strings.Join(strings.Fields(trademarkRE.ReplaceAllString(s, " ")), " ")
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// parseVersion reads "8.9", "610.62", "550.54.14", "26.6.2" as integers.
func parseVersion(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == Unknown {
		return nil, fmt.Errorf("empty version")
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("version %q is not numeric", s)
		}
		out = append(out, n)
	}
	return out, nil
}

func parseVersionOpt(s string) ([]int, error) {
	if s == "" {
		return nil, nil
	}
	return parseVersion(s)
}

// compareVersions compares component by component; a missing component
// counts as 0, so "550" == "550.0" and "550" < "550.54".
func compareVersions(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

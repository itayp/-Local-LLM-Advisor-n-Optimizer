package hardware

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The Linux path reads files, not tools, wherever the kernel publishes the
// fact: /proc for the processor and memory, /etc/os-release, the PCI and DRM
// sysfs trees for graphics devices, the KFD topology for AMD's LLVM target,
// pci.ids for names. nvidia-smi is the one tool, because only the NVIDIA
// driver knows its cards' memory.

func detectLinux(ctx context.Context, e env, p *Profile) {
	fsys := e.files()
	if fsys == nil {
		p.problem("the filesystem could not be read")
		return
	}
	if s, ok := readText(fsys, "etc/os-release"); ok {
		p.OSVersion = osReleaseName(s)
	} else {
		p.problem("/etc/os-release could not be read")
	}
	if s, ok := readText(fsys, "proc/sys/kernel/osrelease"); ok && s != "" {
		p.Kernel = s
	}

	linuxCPU(ctx, e, fsys, p)

	if s, ok := readText(fsys, "proc/meminfo"); ok {
		if n, ok := meminfoTotal(s); ok {
			p.RAMBytes, p.RAMKnown = n, true
		}
	}
	if !p.RAMKnown {
		p.problem("total memory could not be read from /proc/meminfo")
	}

	if s, ok := readText(fsys, "sys/class/dmi/id/chassis_type"); ok {
		if n, err := strconv.Atoi(s); err == nil {
			p.IsLaptop, p.LaptopKnown = laptopFromChassis(n)
		}
	}

	p.GPUs = linuxGPUs(ctx, e, fsys, p)
	p.Storage.ModelsDir, p.Storage.ModelsDirSource = linuxModelsDir(e, fsys)
}

// osReleaseName reads PRETTY_NAME ("Ubuntu 24.04.3 LTS"), falling back to
// NAME + VERSION.
func osReleaseName(s string) string {
	vals := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		vals[k] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	if v := vals["PRETTY_NAME"]; v != "" {
		return v
	}
	if n := vals["NAME"]; n != "" {
		return strings.TrimSpace(n + " " + vals["VERSION"])
	}
	return Unknown
}

// meminfoTotal reads MemTotal from /proc/meminfo (kB, meaning KiB).
func meminfoTotal(s string) (uint64, bool) {
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) != "MemTotal" {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			return 0, false
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil || n == 0 {
			return 0, false
		}
		return n * 1024, true
	}
	return 0, false
}

// cpuinfo is what /proc/cpuinfo says.
type cpuinfo struct {
	model    string
	logical  int
	physical int // distinct (physical id, core id) pairs; 0 when the file does not say
}

func parseCPUInfo(s string) cpuinfo {
	var c cpuinfo
	cores := map[string]bool{}
	var phys string
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			if strings.TrimSpace(line) == "" {
				phys = ""
			}
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			c.logical++
		case "model name":
			if c.model == "" {
				c.model = cleanName(v)
			}
		case "physical id":
			phys = v
		case "core id":
			cores[phys+"/"+v] = true
		}
	}
	c.physical = len(cores)
	return c
}

var cpuDirRE = regexp.MustCompile(`^cpu[0-9]+$`)

// sysfsPhysicalCores counts distinct thread-sibling sets under
// /sys/devices/system/cpu — physical cores on every architecture, where
// /proc/cpuinfo's core ids exist only on x86.
func sysfsPhysicalCores(fsys fs.FS) int {
	entries, err := fs.ReadDir(fsys, "sys/devices/system/cpu")
	if err != nil {
		return 0
	}
	sets := map[string]bool{}
	for _, en := range entries {
		if !cpuDirRE.MatchString(en.Name()) {
			continue
		}
		base := "sys/devices/system/cpu/" + en.Name() + "/topology/"
		s, ok := readText(fsys, base+"core_cpus_list")
		if !ok {
			s, ok = readText(fsys, base+"thread_siblings_list")
		}
		if ok && s != "" {
			sets[s] = true
		}
	}
	return len(sets)
}

func linuxCPU(ctx context.Context, e env, fsys fs.FS, p *Profile) {
	if s, ok := readText(fsys, "proc/cpuinfo"); ok {
		c := parseCPUInfo(s)
		if c.model != "" {
			p.CPU.Model = c.model
		}
		if c.logical > 0 {
			p.CPU.CoresLogical = c.logical
		}
		if c.physical > 0 {
			p.CPU.CoresPhysical = c.physical
		}
	} else {
		p.problem("/proc/cpuinfo could not be read")
	}
	if n := sysfsPhysicalCores(fsys); n > 0 {
		p.CPU.CoresPhysical = n
	}
	if p.CPU.Model == Unknown {
		// ARM kernels print no "model name"; lscpu decodes the part number.
		if out, err := e.run(ctx, timeoutQuick, "lscpu"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "Model name" {
					if m := cleanName(v); m != "" && m != "-" {
						p.CPU.Model = m
						break
					}
				}
			}
		}
	}
}

// pciDevice is one PCI display-class function as sysfs describes it.
type pciDevice struct {
	addr        string // "0000:03:00.0"
	vendor, dev string // "1002", "73bf"
	subVendor   string
	subDev      string
	class       string // "030000"
	driver      string // "amdgpu", "nvidia", "" when none is bound
	vramTotal   uint64
	vramKnown   bool
	vramFile    string
	gttTotal    uint64
	gttKnown    bool
	dgpuMarker  bool // mem_info_vram_vendor or board_info exists: amdgpu's discrete cards only
}

// readPCIDisplayDevices lists every PCI function of class 0x03 (VGA, 3D
// controller, other display). Starting from the PCI bus rather than
// /sys/class/drm catches cards with no driver bound at all — the state of
// an NVIDIA card on a fresh Ubuntu install.
func readPCIDisplayDevices(fsys fs.FS) ([]pciDevice, error) {
	const root = "sys/bus/pci/devices"
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, err
	}
	var out []pciDevice
	for _, en := range entries {
		dir := root + "/" + en.Name()
		class, ok := readText(fsys, dir+"/class")
		if !ok {
			continue
		}
		class = strings.ToLower(strings.TrimPrefix(class, "0x"))
		if !strings.HasPrefix(class, "03") {
			continue
		}
		d := pciDevice{addr: strings.ToLower(en.Name()), class: class}
		if s, ok := readText(fsys, dir+"/vendor"); ok {
			d.vendor = hexID(s)
		}
		if s, ok := readText(fsys, dir+"/device"); ok {
			d.dev = hexID(s)
		}
		if s, ok := readText(fsys, dir+"/subsystem_vendor"); ok {
			d.subVendor = hexID(s)
		}
		if s, ok := readText(fsys, dir+"/subsystem_device"); ok {
			d.subDev = hexID(s)
		}
		// uevent is a plain file (the driver link is a symlink, which test
		// fixtures cannot carry across operating systems).
		if s, ok := readText(fsys, dir+"/uevent"); ok {
			for _, line := range strings.Split(s, "\n") {
				if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && k == "DRIVER" {
					d.driver = strings.TrimSpace(v)
				}
			}
		}
		for _, f := range []string{"mem_info_vram_total", "lmem_total_bytes", "tile0/vram0/physical_vram_size_bytes"} {
			if s, ok := readText(fsys, dir+"/"+f); ok {
				if n, err := strconv.ParseUint(s, 10, 64); err == nil && n > 0 {
					d.vramTotal, d.vramKnown, d.vramFile = n, true, f
					break
				}
			}
		}
		if s, ok := readText(fsys, dir+"/mem_info_gtt_total"); ok {
			if n, err := strconv.ParseUint(s, 10, 64); err == nil {
				d.gttTotal, d.gttKnown = n, true
			}
		}
		for _, f := range []string{"mem_info_vram_vendor", "board_info"} {
			if _, err := fs.Stat(fsys, dir+"/"+f); err == nil {
				d.dgpuMarker = true
			}
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].addr < out[j].addr })
	return out, nil
}

// amdgpuIntegrated applies the signal Ollama itself uses (discover/amd.go,
// v0.34.2): amdgpu creates mem_info_vram_vendor and board_info only for
// discrete cards; an APU has at most a few GiB of carve-out and a GTT
// aperture at least four times larger. Anything else is undecided here.
func amdgpuIntegrated(d pciDevice) (integrated, known bool) {
	if d.driver != "amdgpu" {
		return false, false
	}
	if d.dgpuMarker {
		return false, true
	}
	const maxIntegratedVRAM, minSharedGTT = 4 << 30, 8 << 30
	if d.vramKnown && d.gttKnown && d.vramTotal > 0 && d.vramTotal <= maxIntegratedVRAM &&
		d.gttTotal >= minSharedGTT && d.gttTotal >= 4*d.vramTotal {
		return true, true
	}
	return false, false
}

// kfdGFXTargets reads AMD's compute topology: per GPU node, the LLVM target
// and the PCI address. gfx_target_version is major*10000 + minor*100 +
// stepping, printed as gfx<major><minor hex><stepping hex> (100300 →
// gfx1030, 90010 → gfx90a); location_id is bus<<8 | device<<3 | function.
func kfdGFXTargets(fsys fs.FS) map[string]string {
	const root = "sys/class/kfd/kfd/topology/nodes"
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, en := range entries {
		s, ok := readText(fsys, root+"/"+en.Name()+"/properties")
		if !ok {
			continue
		}
		props := map[string]uint64{}
		for _, line := range strings.Split(s, "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			if n, err := strconv.ParseUint(f[1], 10, 64); err == nil {
				props[f[0]] = n
			}
		}
		ver := props["gfx_target_version"]
		if ver == 0 || props["simd_count"] == 0 {
			continue // a CPU node
		}
		loc := props["location_id"]
		addr := fmt.Sprintf("%04x:%02x:%02x.%x", props["domain"], (loc>>8)&0xff, (loc>>3)&0x1f, loc&0x7)
		out[addr] = gfxFromKFD(ver)
	}
	return out
}

func gfxFromKFD(v uint64) string {
	major, minor, step := v/10000, (v/100)%100, v%100
	if major == 0 || minor > 15 || step > 15 {
		return ""
	}
	return fmt.Sprintf("gfx%d%x%x", major, minor, step)
}

var nvidiaProcVersionRE = regexp.MustCompile(`Kernel Module\s+(?:for\s+\S+\s+)?([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)

// linuxGPUs assembles the GPU list: PCI display devices, names from
// pci.ids, NVIDIA details from nvidia-smi merged by PCI address, AMD targets
// from KFD.
func linuxGPUs(ctx context.Context, e env, fsys fs.FS, p *Profile) []GPU {
	devs, err := readPCIDisplayDevices(fsys)
	if err != nil {
		p.problem("the PCI device list (/sys/bus/pci/devices) could not be read: %v", err)
	} else {
		p.gpuListRead = true
	}

	var smi []GPU
	smiRan := false
	if path, err := e.lookPath("nvidia-smi"); err == nil {
		smiRan = true
		gpus, ok, err := queryNvidiaSMI(ctx, e, path)
		if ok {
			smi = gpus
			p.gpuListRead = p.gpuListRead || len(gpus) > 0
		} else {
			p.problem("nvidia-smi did not answer: %v", err)
		}
	}

	var keys []pciKey
	for _, d := range devs {
		keys = append(keys, pciKey{d.vendor, d.dev, "", ""}, pciKey{d.vendor, d.dev, d.subVendor, d.subDev})
	}
	names := lookupPCINames(fsys, keys)
	gfx := kfdGFXTargets(fsys)
	nvidiaVersion := ""
	if s, ok := readText(fsys, "proc/driver/nvidia/version"); ok {
		if m := nvidiaProcVersionRE.FindStringSubmatch(s); m != nil {
			nvidiaVersion = m[1]
		}
	}

	out := []GPU{}
	usedSMI := make([]bool, len(smi))
	for _, d := range devs {
		vendor := vendorFromPCI(d.vendor)
		devName := names[pciKey{d.vendor, d.dev, "", ""}]
		subName := names[pciKey{d.vendor, d.dev, d.subVendor, d.subDev}]
		display := linuxDisplayName(vendor, devName, subName, d)
		if vendor == VendorUnknown || vendor == VendorApple {
			// ASPEED and Matrox server management chips, VM display
			// adapters: display only, no models.
			p.FilteredAdapters = append(p.FilteredAdapters, display)
			continue
		}
		g := GPU{
			Vendor:          vendor,
			Name:            display,
			DriverVersion:   Unknown,
			PCIID:           d.vendor + ":" + d.dev,
			LinuxDriver:     d.driver,
			ExpectedBackend: PathUnknown,
			busID:           d.addr,
			rawName:         devName,
		}
		if d.driver == "" {
			g.LinuxDriver = "none"
			g.driverMissing = true
			g.Note = "No driver is loaded for this card."
		}
		if d.vramKnown {
			g.VRAMBytes, g.VRAMKnown = d.vramTotal, true
			g.VRAMSource = "sysfs " + d.vramFile
		} else {
			g.VRAMSource = "the driver does not publish this card's memory in sysfs"
		}
		if integrated, known := amdgpuIntegrated(d); known {
			g.IsIntegrated, g.IntegratedKnown = integrated, true
		}
		if vendor == VendorAMD {
			g.GFXTarget = gfx[d.addr]
		}
		switch d.driver {
		case "amdgpu", "radeon", "i915", "xe", "nouveau":
			if p.Kernel != "" {
				g.DriverVersion = d.driver + " in Linux " + p.Kernel
			} else {
				g.DriverVersion = d.driver + " (built into the kernel)"
			}
		case "nvidia":
			if nvidiaVersion != "" {
				g.DriverVersion = nvidiaVersion
			}
		}
		if vendor == VendorNVIDIA {
			for i, s := range smi {
				if usedSMI[i] || (s.busID != "" && s.busID != d.addr) {
					continue
				}
				usedSMI[i] = true
				s.LinuxDriver = g.LinuxDriver
				if s.PCIID == "" {
					s.PCIID = g.PCIID
				}
				s.busID = d.addr
				s.rawName = devName
				g = s
				break
			}
			if !smiRan && d.driver == "nvidia" {
				g.VRAMSource = "unknown: nvidia-smi was not found"
			}
		}
		out = append(out, g)
	}
	// Cards nvidia-smi knows that the PCI walk did not (a sysfs that could
	// not be read): nvidia-smi is still the truth about them.
	for i, s := range smi {
		if !usedSMI[i] {
			s.LinuxDriver = "nvidia"
			out = append(out, s)
		}
	}
	if !smiRan {
		for _, g := range out {
			if g.Vendor == VendorNVIDIA && g.LinuxDriver == "nvidia" {
				p.problem("nvidia-smi was not found, so the NVIDIA card's memory and compute capability are unknown")
				break
			}
		}
	}
	return out
}

// linuxDisplayName builds "AMD Radeon RX 7900 XTX" style names from pci.ids.
// pci.ids names a device id by chip, with the retail names in brackets
// ("Navi 31 [Radeon RX 7900 XT/7900 XTX/7900 GRE/7900M]"). When the bracket
// lists several cards and the subsystem id names the exact one ("FirePro
// D700" under Apple's subsystem), the subsystem name is more precise.
func linuxDisplayName(v Vendor, devName, subName string, d pciDevice) string {
	short := vendorDisplayName(v)
	if v == VendorUnknown {
		short = ""
	}
	if devName == "" {
		return strings.TrimSpace(fmt.Sprintf("%s graphics device (PCI %s:%s)", short, d.vendor, d.dev))
	}
	retail := devName
	if i, j := strings.IndexByte(devName, '['), strings.LastIndexByte(devName, ']'); i >= 0 && j > i {
		retail = strings.TrimSpace(devName[i+1 : j])
	}
	if subName != "" && strings.Contains(retail, "/") {
		retail = subName
	}
	if short != "" && !strings.HasPrefix(strings.ToLower(retail), strings.ToLower(short)) {
		return short + " " + retail
	}
	return retail
}

// pciKey addresses pci.ids: a device (sub* empty) or a subsystem entry.
type pciKey struct{ vendor, device, subVendor, subDevice string }

// pciIDsPaths is where distributions keep the PCI name database lspci reads.
var pciIDsPaths = []string{"usr/share/misc/pci.ids", "usr/share/hwdata/pci.ids", "usr/share/pci.ids", "usr/share/misc/pci.ids.gz"}

// lookupPCINames reads pci.ids once and returns the names of the keys asked
// for. Format: a vendor at column 0 ("10de  NVIDIA Corporation"), its
// devices indented by one tab ("\t2c05  GB203 [GeForce RTX 5070 Ti]"),
// subsystems by two ("\t\t106b 0128  FirePro D700"); the class list at the
// end ("C 00 ...") is not read.
func lookupPCINames(fsys fs.FS, keys []pciKey) map[pciKey]string {
	out := map[pciKey]string{}
	if len(keys) == 0 {
		return out
	}
	want := map[pciKey]bool{}
	vendors := map[string]bool{}
	for _, k := range keys {
		want[k] = true
		vendors[k.vendor] = true
	}
	var r io.Reader
	for _, p := range pciIDsPaths {
		f, err := fsys.Open(p)
		if err != nil {
			continue
		}
		defer f.Close()
		r = f
		if strings.HasSuffix(p, ".gz") {
			zr, err := gzip.NewReader(f)
			if err != nil {
				return out
			}
			r = zr
		}
		break
	}
	if r == nil {
		return out
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var vendor, device string
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		if strings.HasPrefix(line, "C ") {
			break // device classes follow; vendors are done
		}
		switch {
		case strings.HasPrefix(line, "\t\t"):
			if vendor == "" || device == "" {
				continue
			}
			f := strings.Fields(line)
			if len(f) < 3 {
				continue
			}
			k := pciKey{vendor, device, strings.ToLower(f[0]), strings.ToLower(f[1])}
			if want[k] {
				out[k] = strings.TrimSpace(strings.Join(f[2:], " "))
			}
		case strings.HasPrefix(line, "\t"):
			if vendor == "" {
				continue
			}
			id, name, ok := strings.Cut(strings.TrimPrefix(line, "\t"), " ")
			device = strings.ToLower(id)
			if !ok {
				continue
			}
			k := pciKey{vendor, device, "", ""}
			if want[k] {
				out[k] = strings.TrimSpace(name)
			}
		default:
			id, _, _ := strings.Cut(line, " ")
			vendor, device = strings.ToLower(id), ""
			if !vendors[vendor] {
				vendor = "" // skip this vendor's devices quickly
			}
		}
	}
	return out
}

// linuxModelsDir: OLLAMA_MODELS in this process; then the one Ollama's
// systemd service is given (its FAQ's way of moving models on Linux); then
// the folder a system install uses (/usr/share/ollama/.ollama/models, the
// documented default); then ~/.ollama/models, where a user-run Ollama —
// and step 3's user-space install, unless it says otherwise — keeps them.
func linuxModelsDir(e env, fsys fs.FS) (dir, source string) {
	if v := strings.TrimSpace(e.getenv("OLLAMA_MODELS")); v != "" {
		return v, "OLLAMA_MODELS"
	}
	if v := systemdOllamaModels(fsys); v != "" {
		return v, "OLLAMA_MODELS in Ollama's systemd service"
	}
	const system = "/usr/share/ollama/.ollama/models"
	if fsExists(fsys, system) {
		return system, "Ollama's default for a system install on Linux"
	}
	home, err := e.homeDir()
	if err != nil || home == "" {
		if fsExists(fsys, "/usr/share/ollama") {
			return system, "Ollama's default for a system install on Linux"
		}
		return Unknown, "the home folder could not be found"
	}
	user := strings.TrimRight(home, "/") + "/.ollama/models"
	if fsExists(fsys, user) {
		return user, "Ollama's default when it runs as you"
	}
	if fsExists(fsys, "/usr/share/ollama") || fsExists(fsys, "/etc/systemd/system/ollama.service") {
		return system, "Ollama's default for a system install on Linux"
	}
	return user, "Ollama's default when it runs as you"
}

// systemdOllamaModels reads OLLAMA_MODELS from ollama.service and its
// drop-ins (later files win, as systemd applies them).
func systemdOllamaModels(fsys fs.FS) string {
	files := []string{"etc/systemd/system/ollama.service"}
	if entries, err := fs.ReadDir(fsys, "etc/systemd/system/ollama.service.d"); err == nil {
		var dropins []string
		for _, en := range entries {
			if strings.HasSuffix(en.Name(), ".conf") {
				dropins = append(dropins, "etc/systemd/system/ollama.service.d/"+en.Name())
			}
		}
		sort.Strings(dropins)
		files = append(files, dropins...)
	}
	val := ""
	for _, f := range files {
		s, ok := readText(fsys, f)
		if !ok {
			continue
		}
		for _, line := range strings.Split(s, "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok || strings.TrimSpace(k) != "Environment" {
				continue
			}
			for _, assign := range splitSystemdWords(v) {
				if name, value, ok := strings.Cut(assign, "="); ok && name == "OLLAMA_MODELS" {
					val = value
				}
			}
		}
	}
	return val
}

// splitSystemdWords splits an Environment= value into assignments, honouring
// double and single quotes.
func splitSystemdWords(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\''):
			quote = r
		case quote == 0 && (r == ' ' || r == '\t'):
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// fsExists checks an absolute path against the root filesystem.
func fsExists(fsys fs.FS, abs string) bool {
	rel := strings.TrimPrefix(path.Clean(abs), "/")
	if rel == "" || rel == "." {
		return true
	}
	_, err := fs.Stat(fsys, rel)
	return err == nil
}

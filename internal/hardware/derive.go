package hardware

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Tier thresholds, in bytes of memory on the best usable graphics device.
// They follow the model sizes step 0 measured (q4_K_M weights plus a 4k
// context and the runtime's overhead): an 8B model needs about 6 GB, so an
// 8 GB card (which reports 7.6–8 GiB) is the first "medium"; a 14B model
// needs about 10 GB, and 16 GB cards (15.9 GiB) are the first "large"; a
// 70B model does not fit below 40 GB, and 32 GB cards and 48 GB+ Macs are
// "xl". The tier is a sentence for the UI, not the fit — step 5's estimator
// decides what fits, model by model.
const (
	tierMediumFrom = 7 << 30
	tierLargeFrom  = 14 << 30
	tierXLFrom     = 30 << 30
)

// finish derives everything that is not read directly: each GPU's LLVM
// target (from its name, where the OS did not say), whether it is
// integrated, its expected backend; the order of the list; the GPU memory
// budget; the tier and its sentence; the notes.
func finish(p *Profile) {
	if p.GPUs == nil {
		p.GPUs = []GPU{}
	}
	sup, err := loadSupport()
	if err != nil {
		p.problem("the runtime-support table could not be read: %v", err)
	} else {
		p.ExpectationsFrom = "Ollama " + sup.OllamaVersion
	}

	osTooOld, osWhy := false, ""
	if sup != nil {
		switch p.OS {
		case "darwin":
			if old, ok := sup.darwinTooOld(p.macOSVersion); ok && old {
				osTooOld = true
				osWhy = fmt.Sprintf("Ollama needs macOS %s or newer; this Mac runs %s.", sup.OSMinimum.Darwin.Version, p.macOSVersion)
			}
		case "windows":
			if old, ok := sup.windowsTooOld(p.windowsBuild); ok && old {
				osTooOld = true
				osWhy = fmt.Sprintf("Ollama needs Windows 10 22H2 (build %d) or newer; this computer runs build %d.", sup.OSMinimum.Windows.Build, p.windowsBuild)
			}
		}
	}
	if osTooOld {
		p.note("%s", osWhy)
	}

	for i := range p.GPUs {
		g := &p.GPUs[i]
		match := g.Name
		if g.rawName != "" {
			match = g.rawName + " " + g.Name
		}
		if sup == nil {
			g.ExpectedBackend = PathUnknown
			g.ExpectedBackendReason = "The runtime-support table could not be read."
			continue
		}
		if g.Vendor == VendorAMD && g.GFXTarget == "" {
			g.GFXTarget = sup.gfxFor(match)
		}
		if !g.IntegratedKnown {
			g.IsIntegrated, g.IntegratedKnown = sup.integratedFor(g.Vendor, match)
		}
		switch {
		case osTooOld:
			g.ExpectedBackend, g.ExpectedBackendReason, g.ExpectedBackendRule = PathNone, osWhy, "os-minimum"
		case g.problemCode != 0 && !g.driverMissing:
			g.ExpectedBackend, g.ExpectedBackendRule = PathNone, "device-problem"
			g.ExpectedBackendReason = "Windows reports that this device is not working, so Ollama cannot use it."
		default:
			r := sup.expect(gpuFacts{
				goos:          p.OS,
				goarch:        p.Arch,
				vendor:        g.Vendor,
				name:          match,
				gfx:           g.GFXTarget,
				compute:       g.ComputeCapability,
				driverVersion: g.DriverVersion,
				linuxDriver:   g.LinuxDriver,
				driverMissing: g.driverMissing,
			})
			g.ExpectedBackend, g.ExpectedBackendReason, g.ExpectedBackendRule = r.Backend, r.Why, r.ID
			g.actionable = r.Actionable
		}
	}

	orderGPUs(p.GPUs)
	gpuBudget(p)
	gpuNotes(p)
	p.Tier = deriveTier(p, osTooOld)
	p.Summary = summarize(p)
	if osTooOld {
		p.Summary = summarizeOSTooOld(p, osWhy)
	}

	if p.CPU.VectorKnown && !p.CPU.HasAVX2 && (p.Arch == "amd64" || p.Arch == "386") {
		p.note("This processor lacks AVX2, which makes models that run on the processor noticeably slower.")
	}

	for i := range p.GPUs {
		g := &p.GPUs[i]
		g.busID, g.rawName, g.driverMissing, g.problemCode, g.actionable = "", "", false, 0, false
	}
}

// counts reports whether a GPU's own memory is a budget a model can be
// placed in: a card the runtime can use that is not sharing system memory —
// or an integrated GPU the runtime drives with ROCm (Ryzen AI), where the
// BIOS-dedicated memory is what ROCm allocates from.
func counts(g GPU) bool {
	if !g.ExpectedBackend.UsesGPU() || g.Vendor == VendorApple {
		return false
	}
	return !g.IsIntegrated || g.ExpectedBackend == PathROCm
}

// orderGPUs puts the answer first: devices the runtime can use before the
// rest, discrete before integrated (the discrete card is the answer on a
// laptop with both), then the most memory first — Ollama itself schedules
// onto the device with the most free memory. Equal devices keep their
// detected order.
func orderGPUs(gpus []GPU) {
	rank := func(g GPU) int {
		r := 0
		switch {
		case g.ExpectedBackend.UsesGPU():
		case g.ExpectedBackend == PathUnknown:
			r += 2
		default:
			r += 4
		}
		if g.IsIntegrated && !counts(g) {
			r++
		}
		return r
	}
	sort.SliceStable(gpus, func(i, j int) bool {
		ri, rj := rank(gpus[i]), rank(gpus[j])
		if ri != rj {
			return ri < rj
		}
		return gpus[i].VRAMKnown && gpus[i].VRAMBytes > gpus[j].VRAMBytes
	})
}

// gpuBudget sets GPUUsableBytes for machines with graphics memory of their
// own (macprobe.go sets it for unified memory). A model runs on one device
// unless the runtime splits it, so the budget is the largest single device
// and never a sum (probe0: the Mac Pro's two 6 GB cards are not a 12 GB
// pool; Ollama used one).
func gpuBudget(p *Profile) {
	if p.UnifiedMemory {
		return
	}
	var best *GPU
	usable := 0
	for i := range p.GPUs {
		g := &p.GPUs[i]
		if !counts(*g) {
			continue
		}
		usable++
		if best == nil {
			best = g // orderGPUs put the largest first
		}
	}
	switch {
	case best == nil:
		p.GPUUsableSource = "no graphics card with memory of its own that Ollama can use"
	case !best.VRAMKnown:
		p.GPUUsableSource = "unknown: the " + best.Name + "'s memory could not be read"
	default:
		p.GPUUsableBytes, p.GPUUsableKnown = best.VRAMBytes, true
		p.GPUUsableSource = "the " + best.Name + "'s own memory (" + best.VRAMSource + ")"
		if usable > 1 {
			p.GPUUsableSource += "; memory is not added up across cards"
		}
	}
}

func gpuNotes(p *Profile) {
	discrete := 0
	for _, g := range p.GPUs {
		if g.IntegratedKnown && !g.IsIntegrated {
			discrete++
		}
	}
	if discrete > 1 && len(p.GPUs) > 0 {
		p.note("This computer has %d graphics cards. The advisor plans for the first one, the %s; using several cards together is not optimised in this version.", discrete, p.GPUs[0].Name)
	}
	for _, g := range p.GPUs {
		if g.Vendor == VendorIntel && g.IntegratedKnown && !g.IsIntegrated {
			p.note("Intel Arc graphics are recognised and should work through Vulkan, but the advisor does not tune for them yet.")
			break
		}
	}
	// A card held back by something the user can fix (a missing or old
	// driver) is worth a sentence even when another device carries the load.
	for _, g := range p.GPUs {
		if g.actionable {
			p.note("%s: %s", matchName(g.Name), g.ExpectedBackendReason)
		}
	}
}

func deriveTier(p *Profile, osTooOld bool) Tier {
	if osTooOld {
		// Not a class of hardware: nothing runs until the OS is updated, and
		// the summary says so.
		return TierUnknown
	}
	if len(p.GPUs) == 0 {
		if !p.gpuListRead {
			return TierUnknown // could not look, which is not the same as "none"
		}
		return TierCPUOnly
	}
	primary := p.GPUs[0]
	switch {
	case primary.ExpectedBackend == PathUnknown:
		return TierUnknown
	case !primary.ExpectedBackend.UsesGPU():
		return TierCPUOnly
	case p.UnifiedMemory && primary.Vendor == VendorApple, counts(primary):
		if !p.GPUUsableKnown {
			return TierUnknown
		}
		return sizeTier(p.GPUUsableBytes)
	default:
		return TierIntegrated
	}
}

func sizeTier(b uint64) Tier {
	switch {
	case b >= tierXLFrom:
		return TierGPUXL
	case b >= tierLargeFrom:
		return TierGPULarge
	case b >= tierMediumFrom:
		return TierGPUMedium
	}
	return TierGPUSmall
}

// summarize writes the one sentence the UI shows first. It names the
// machine's deciding part and says what that means, in words; numbers are
// the OS's, rounded the way people say them.
func summarize(p *Profile) string {
	device := "This computer"
	switch {
	case p.OS == "darwin" && p.LaptopKnown && p.IsLaptop:
		device = "A Mac laptop"
	case p.OS == "darwin" && p.LaptopKnown:
		device = "A Mac desktop"
	case p.OS == "darwin":
		device = "A Mac"
	case p.LaptopKnown && p.IsLaptop:
		device = "A laptop"
	case p.LaptopKnown:
		device = "A desktop"
	}
	ram := "an unknown amount of memory"
	if p.RAMKnown {
		ram = humanGB(p.RAMBytes) + " of memory"
	}
	cpu := "processor"
	if p.CPU.Model != Unknown && p.CPU.Model != "" {
		cpu = "processor (" + matchName(p.CPU.Model) + ")"
	}
	const fewWords = "small models only, roughly a few words a second"

	var primary GPU
	if len(p.GPUs) > 0 {
		primary = p.GPUs[0]
	}
	switch p.Tier {
	case TierGPUSmall, TierGPUMedium, TierGPULarge, TierGPUXL:
		size := map[Tier]string{
			TierGPUSmall:  "small models",
			TierGPUMedium: "small and medium-sized models",
			TierGPULarge:  "large models",
			TierGPUXL:     "very large models",
		}[p.Tier]
		if p.UnifiedMemory && primary.Vendor == VendorApple {
			chip := matchName(primary.Name)
			if p.CPU.Model != Unknown && p.CPU.Model != "" {
				chip = matchName(p.CPU.Model)
			}
			return fmt.Sprintf("%s with %s %s chip and %s, of which the graphics can use %s: %s run on the chip's graphics.",
				device, article(chip), chip, ram, humanGB(p.GPUUsableBytes), size)
		}
		name := matchName(primary.Name)
		return fmt.Sprintf("%s with %s %s (%s of graphics memory): %s run on the graphics card.",
			device, article(name), name, humanGB(p.GPUUsableBytes), size)
	case TierIntegrated:
		name := matchName(primary.Name)
		return fmt.Sprintf("%s with %s %s built into its processor and %s: %s.",
			device, article(name), name, ram, fewWords)
	case TierCPUOnly:
		switch {
		case p.OS == "darwin" && p.Arch == "amd64":
			return fmt.Sprintf("%s with an Intel %s and %s: on Intel Macs Ollama runs models on the processor, so %s.", device, cpu, ram, fewWords)
		case len(p.GPUs) == 0:
			return fmt.Sprintf("%s with no graphics card, so models run on the %s with %s: %s.", device, cpu, ram, fewWords)
		default:
			return fmt.Sprintf("%s whose graphics Ollama cannot use, so models run on the %s with %s: %s.", device, cpu, ram, fewWords)
		}
	}
	// Unknown.
	switch {
	case len(p.GPUs) == 0:
		return "This computer could not be read fully; the details say what is missing."
	case primary.ExpectedBackend == PathUnknown:
		name := matchName(primary.Name)
		return fmt.Sprintf("%s with %s %s; whether Ollama can use it is not known yet.", device, article(name), name)
	case p.UnifiedMemory && primary.Vendor == VendorApple:
		chip := matchName(primary.Name)
		if p.CPU.Model != Unknown && p.CPU.Model != "" {
			chip = matchName(p.CPU.Model)
		}
		return fmt.Sprintf("%s with %s %s chip and %s; how much of it the graphics may use could not be read, so what fits is not known yet.", device, article(chip), chip, ram)
	default:
		name := matchName(primary.Name)
		return fmt.Sprintf("%s with %s %s whose memory could not be read, so what fits is not known yet.", device, article(name), name)
	}
}

func summarizeOSTooOld(p *Profile, why string) string {
	return "This computer's operating system is too old for Ollama: " + why + " Nothing can run here until it is updated."
}

// humanGB rounds a byte count (binary units, as every OS and vendor tool
// here reports memory) the way people say it: a whole number when it is
// within 1% of one ("16 GB" for a card reporting 15.9 GiB), else one decimal
// ("11.8 GB").
func humanGB(b uint64) string {
	g := float64(b) / (1 << 30)
	if g < 1 {
		return strconv.FormatFloat(float64(b)/(1<<20), 'f', 0, 64) + " MB"
	}
	r := math.Round(g)
	if math.Abs(g-r) <= 0.01*g {
		return strconv.FormatFloat(r, 'f', 0, 64) + " GB"
	}
	return strconv.FormatFloat(g, 'f', 1, 64) + " GB"
}

func article(name string) string {
	n := strings.ToUpper(strings.TrimSpace(name))
	if n == "" {
		return "a"
	}
	switch {
	case strings.ContainsRune("AEIOU", rune(n[0])):
		return "an"
	case strings.HasPrefix(n, "NVIDIA"), strings.HasPrefix(n, "RTX"), strings.HasPrefix(n, "M1"), strings.HasPrefix(n, "M2"),
		strings.HasPrefix(n, "M3"), strings.HasPrefix(n, "M4"), strings.HasPrefix(n, "M5"):
		return "an"
	}
	return "a"
}

// FingerprintVersion prefixes every fingerprint, so a change to what
// identifies a machine is a visible break rather than a silent one.
const FingerprintVersion = "v1"

// Fingerprint identifies the hardware, not its state: OS family and
// architecture, processor model, memory and each GPU (PCI id, or vendor and
// name where there is none, and its memory) — rounded to the GiB so a
// kernel or driver update that shifts a reported total by a few MiB is not a
// new machine. Hostname, OS version, drivers, free disk space and the GPU
// memory cap are deliberately left out: they change without the hardware
// changing. Two profiles with the same fingerprint are the same machine for
// the purpose of attributing benchmarks.
func Fingerprint(p Profile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s|%s|", FingerprintVersion, p.OS, p.Arch, matchName(p.CPU.Model))
	if p.RAMKnown {
		fmt.Fprintf(&b, "ram=%d", roundGiB(p.RAMBytes))
	} else {
		b.WriteString("ram=?")
	}
	gpus := make([]string, 0, len(p.GPUs))
	for _, g := range p.GPUs {
		id := g.PCIID
		if id == "" {
			id = string(g.Vendor) + "/" + strings.ToLower(matchName(g.Name))
		}
		mem := "?"
		if g.VRAMKnown {
			mem = strconv.FormatUint(roundGiB(g.VRAMBytes), 10)
		}
		gpus = append(gpus, id+"@"+mem)
	}
	sort.Strings(gpus)
	b.WriteString("|" + strings.Join(gpus, ","))
	sum := sha256.Sum256([]byte(b.String()))
	return FingerprintVersion + "-" + hex.EncodeToString(sum[:12])
}

func roundGiB(b uint64) uint64 {
	return uint64(math.Round(float64(b) / (1 << 30)))
}

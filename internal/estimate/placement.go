package estimate

import (
	"fmt"
	"strings"

	"advisor/internal/hardware"
)

// Placement is where a model would run on this machine and what memory it
// would be compared against — decided once per machine, before any model is
// looked at, so that Fit and the recommendation engine cannot disagree
// about it.
//
// The rules (build-plan step 5, ARCHITECTURE.md D-5, D-20 finding 8, D-25):
//
//   - Apple Silicon compares against gpu_usable_bytes — what macOS lets the
//     GPU use — never against RAM.
//   - A graphics card compares against the largest single device's memory,
//     never a sum.
//   - A machine whose models run on the processor compares against RAM with
//     the operating system's own needs reserved (Config.OSReserve).
//   - When the runtime was SEEN to run on the processor although a graphics
//     card exists, the plan is for the processor and says why.
type Placement struct {
	Path       hardware.RuntimePath
	PathSource PathSource
	// Device is the graphics device the plan is for; nil when the processor
	// does the work.
	Device *hardware.GPU
	// SharedMemory is true when Device has no memory of its own to plan
	// against (graphics built into the processor): the budget and the speed
	// are the system memory's.
	SharedMemory bool

	BudgetKind  BudgetKind
	BudgetBytes uint64
	BudgetKnown bool

	// SystemBytes is RAM less the operating system's reserve: where whatever
	// does not fit in graphics memory has to live.
	SystemBytes uint64
	SystemKnown bool

	// Unused is set when the machine has a graphics card and the plan is
	// nevertheless for the processor.
	Unused *UnusedGPU
	// Blocked is set when nothing can run at all (the operating system is
	// older than the runtime supports); it is the sentence to show.
	Blocked string

	Notes []string
}

// UnusedKind is why a graphics card is not part of the plan.
type UnusedKind string

const (
	// UnusedEstablished: the runtime was seen to run a model on the
	// processor although the card exists.
	UnusedEstablished UnusedKind = "not_used"
	// UnusedUnsupported: the runtime-support rules say the runtime cannot use
	// this card as it is set up (unsupported card, missing driver, Intel Mac).
	UnusedUnsupported UnusedKind = "cannot_use"
	// UnusedNotKnown: whether the runtime can use the card has not been
	// established either way.
	UnusedNotKnown UnusedKind = "not_known"
	// UnusedMemoryUnknown: the card should work, but its memory could not be
	// read, so nothing can be planned against it.
	UnusedMemoryUnknown UnusedKind = "memory_unknown"
)

// UnusedGPU names the card and says why, in words a person can act on.
type UnusedGPU struct {
	Name string     `json:"name"`
	Kind UnusedKind `json:"kind"`
	Why  string     `json:"why"`
}

// Place decides the placement for a machine.
func (e *Estimator) Place(m Machine) Placement {
	p := m.Profile
	var pl Placement

	if p.RAMKnown {
		reserve, ok := e.Config.OSReserve[p.OS]
		if !ok {
			reserve = e.Config.OSReserveDefault
		}
		if p.RAMBytes > reserve {
			pl.SystemBytes = p.RAMBytes - reserve
		}
		pl.SystemKnown = true
	}

	primary, hasGPU := p.PrimaryGPU()
	if hasGPU && primary.ExpectedBackendRule == "os-minimum" {
		pl.Blocked = p.Summary
	}
	discrete := discreteGPU(p)

	cpu := func(source PathSource, unused *UnusedGPU) Placement {
		pl.Path, pl.PathSource = hardware.PathCPU, source
		pl.BudgetKind, pl.BudgetBytes, pl.BudgetKnown = BudgetSystem, pl.SystemBytes, pl.SystemKnown
		pl.Unused = unused
		return pl
	}

	actual := m.ActualPath
	switch {
	case actual == hardware.PathCPU:
		// Seen: the last model ran on the processor.
		var unused *UnusedGPU
		if discrete != nil || (hasGPU && primary.Vendor == hardware.VendorApple) {
			g := primary
			if discrete != nil {
				g = *discrete
			}
			unused = &UnusedGPU{Name: hardware.MatchName(g.Name), Kind: UnusedEstablished, Why: whyNotUsed(g, m.RuntimeEnv)}
		}
		return cpu(PathEstablished, unused)

	case actual.UsesGPU() && hasGPU:
		pl.Path, pl.PathSource = actual, PathEstablished

	case !hasGPU:
		return cpu(PathExpected, nil)

	case primary.ExpectedBackend.UsesGPU():
		pl.Path, pl.PathSource = primary.ExpectedBackend, PathExpected

	case primary.ExpectedBackend == hardware.PathUnknown:
		var unused *UnusedGPU
		if discrete != nil {
			unused = &UnusedGPU{Name: hardware.MatchName(discrete.Name), Kind: UnusedNotKnown, Why: discrete.ExpectedBackendReason}
		}
		return cpu(PathExpected, unused)

	default: // the rules say the runtime cannot use what is there
		var unused *UnusedGPU
		if discrete != nil {
			unused = &UnusedGPU{Name: hardware.MatchName(discrete.Name), Kind: UnusedUnsupported, Why: discrete.ExpectedBackendReason}
		}
		return cpu(PathExpected, unused)
	}

	// A graphics path. Which memory does it draw on?
	g := primary
	pl.Device = &g
	switch {
	case p.UnifiedMemory && g.Vendor == hardware.VendorApple:
		pl.BudgetKind = BudgetUnified
		if p.GPUUsableKnown {
			pl.BudgetBytes, pl.BudgetKnown = p.GPUUsableBytes, true
		} else {
			// macOS did not say how much the GPU may use, and it is not a
			// ratio of RAM (D-25): the budget stays unknown, every fit is
			// CategoryUnknown, and the UI says why — never a stand-in.
			pl.Notes = append(pl.Notes, "how much memory macOS lets the graphics use could not be read, so the advisor cannot say what fits")
		}
	case g.IsIntegrated && g.ExpectedBackend != hardware.PathROCm:
		// Graphics built into the processor share system memory, and run at
		// its speed: plan as for the processor.
		pl.SharedMemory = true
		pl.BudgetKind, pl.BudgetBytes, pl.BudgetKnown = BudgetSystem, pl.SystemBytes, pl.SystemKnown
		pl.Notes = append(pl.Notes, "the graphics built into this processor share the computer's memory, so models run at the speed of that memory either way")
	case p.GPUUsableKnown:
		pl.BudgetKind, pl.BudgetBytes, pl.BudgetKnown = BudgetGraphics, p.GPUUsableBytes, true
	default:
		name := hardware.MatchName(g.Name)
		return cpu(PathExpected, &UnusedGPU{Name: name, Kind: UnusedMemoryUnknown,
			Why: fmt.Sprintf("How much memory the %s has could not be read, so the advisor cannot tell what fits on it.", name)})
	}
	return pl
}

// discreteGPU returns the first graphics card that is not built into the
// processor — what a person means by "my graphics card".
func discreteGPU(p hardware.Profile) *hardware.GPU {
	for i := range p.GPUs {
		if g := p.GPUs[i]; g.IntegratedKnown && !g.IsIntegrated {
			return &g
		}
	}
	return nil
}

// whyNotUsed explains, as far as the facts go, why a card the runtime was
// seen not to use was not used. It never guesses one cause: where the
// profile's own rule already says the card cannot be used, that sentence is
// the answer; otherwise the usual causes are listed as such.
func whyNotUsed(g hardware.GPU, env map[string]string) string {
	if !g.ExpectedBackend.UsesGPU() && strings.TrimSpace(g.ExpectedBackendReason) != "" {
		return g.ExpectedBackendReason
	}
	if g.ExpectedBackend == hardware.PathVulkan {
		if v, ok := env["OLLAMA_VULKAN"]; ok && (v == "0" || strings.EqualFold(v, "false")) {
			return "Ollama reaches this card through Vulkan, and Vulkan is switched off in Ollama's settings on this computer (OLLAMA_VULKAN is set to " + v + ")."
		}
	}
	return "Ollama should be able to use this card but ran the last model on the processor. The usual causes are a graphics driver that is missing or out of date, a card Ollama's build does not cover after all, or Vulkan support being switched off."
}

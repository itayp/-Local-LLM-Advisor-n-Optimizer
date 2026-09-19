package estimate

import (
	"fmt"
	"strconv"

	"advisor/internal/figure"
	"advisor/internal/hardware"
)

// The speed model (build-plan step 5, item 2):
//
//	generation tok/s ≈ effective memory bandwidth ÷ bytes read per token
//
// Producing one token reads every weight the token uses and the whole cache
// written so far; on every part a consumer owns, memory is slower than
// arithmetic at that, so bandwidth sets the pace. "Effective" is the
// efficiency factor: the share of the part's bandwidth the runtime actually
// achieves, which depends on the runtime path — and is a RANGE, because the
// parts a path covers do not agree (Config.Paths says what each range was
// measured on). Both ends are estimates:
//
//	high = bandwidth × efficiency.High ÷ bytes of weights used per token
//	       — an empty context
//	low  = bandwidth × efficiency.Low  ÷ (weights used per token + the cache at the context asked for)
//	       — a full one
//
// Prompt processing is estimated separately: it is bound by arithmetic, so
// it is the generation speed of the reference model scaled to this model's
// parameters, times the measured prompt-to-generation ratio of the path.
//
// A part that is not in gpus.yaml gets no estimate — Known is false and
// Unknown says so. The range narrows to a point the moment a benchmark
// measures it (WithMeasurement).

// speedRange is a bandwidth (GB/s) and efficiency pair, as a range.
type speedRange struct {
	bwLow, bwHigh   float64 // GB/s
	effLow, effHigh float64
	ratio           Range // prompt ÷ generation
	label           string
}

func (e *Estimator) speed(pl Placement, m Machine, model Model, t terms, gpuShare float64, cat Category) Speed {
	if cat == CategoryUnknown || cat == NotRecommended {
		// There is no configuration to be fast or slow: nothing is estimated
		// for a model the advisor would not run, or cannot place.
		return Speed{Unknown: "no speed estimate: the advisor does not suggest running this model on this computer as it is"}
	}
	cfg := e.Config

	// Bytes read per token, in GB (decimal, like the bandwidth figures).
	active := 1.0
	if model.Size.ActiveParameters > 0 && model.Size.Parameters > 0 {
		active = float64(model.Size.ActiveParameters) / float64(model.Size.Parameters)
	}
	weightsGB := float64(t.weights) * active / 1e9
	cacheGB := float64(t.kv) / 1e9
	if weightsGB <= 0 {
		return Speed{Unknown: "no speed estimate: the size of this model's file is not known"}
	}
	moe := model.File.Header.ExpertCount > 0

	var device, system *speedRange
	var why string
	if pl.Device != nil && !pl.SharedMemory {
		device, why = e.deviceSpeed(pl)
		if device == nil {
			return Speed{Unknown: why}
		}
	}
	if pl.Device == nil || pl.SharedMemory || gpuShare < 1 {
		system, why = e.systemSpeed(m.Profile, pl)
		if system == nil {
			if device != nil { // a split: the device is known, the processor's memory is not
				why = "no speed estimate: part of this model would run on the processor, and " + why
			} else {
				why = "no speed estimate: " + why
			}
			return Speed{Unknown: why}
		}
	}

	// Seconds per token at each end, summed over where the layers run.
	secLow, secHigh := 0.0, 0.0 // Low speed = long time
	add := func(r *speedRange, share float64) {
		if r == nil || share <= 0 {
			return
		}
		secLow += share * (weightsGB + cacheGB) / (r.bwLow * r.effLow)
		secHigh += share * weightsGB / (r.bwHigh * r.effHigh)
	}
	primary := device
	if device != nil {
		add(device, gpuShare)
		add(system, 1-gpuShare)
	} else {
		primary = system
		add(system, 1)
	}
	genLow, genHigh := 1/secLow, 1/secHigh
	if moe {
		genLow *= cfg.MoEGeneration.Low
		genHigh *= cfg.MoEGeneration.High
	}

	// Prompt processing: the reference model's generation speed, scaled to
	// this model's parameters, times the path's measured ratio; slowed by
	// the same factor a split slows generation.
	params := float64(model.Size.Parameters)
	if model.Size.ActiveParameters > 0 {
		params = float64(model.Size.ActiveParameters)
	}
	var prompt *figure.Rate
	if params > 0 {
		refGB := params * cfg.PromptReferenceBytesPerParam / 1e9
		pLow := primary.ratio.Low * primary.bwLow * primary.effLow / refGB
		pHigh := primary.ratio.High * primary.bwHigh * primary.effHigh / refGB
		if device != nil && gpuShare < 1 {
			whole := weightsGB / (device.bwHigh * device.effHigh)
			slow := whole / secHigh // < 1
			pLow, pHigh = pLow*slow, pHigh*slow
		}
		if moe {
			pLow *= cfg.MoEPrompt.Low
			pHigh *= cfg.MoEPrompt.High
		}
		r := figure.EstimatedRange(roundRate(pLow), roundRate(pHigh), "tok/s")
		prompt = &r
	}

	gen := figure.EstimatedRange(roundRate(genLow), roundRate(genHigh), "tok/s")
	basis := primary.label
	if device != nil && gpuShare < 1 {
		basis = fmt.Sprintf("%s for %.0f%% of the layers; %s for the rest", device.label, 100*gpuShare, system.label)
	}
	basis += fmt.Sprintf("; %s GB of weights read per token, %s GB of cache when the context is full",
		strconv.FormatFloat(weightsGB, 'f', 1, 64), strconv.FormatFloat(cacheGB, 'f', 1, 64))
	if moe {
		basis += "; scaled for a mixture-of-experts model"
	}
	return Speed{Known: true, Generation: &gen, Prompt: prompt, Basis: basis}
}

// deviceSpeed is the bandwidth and efficiency of the placement's graphics
// device, or nil and the sentence that says why there is none.
func (e *Estimator) deviceSpeed(pl Placement) (*speedRange, string) {
	g := *pl.Device
	name := hardware.MatchName(g.Name)
	if e.Devices == nil {
		return nil, "no speed estimate: the table of graphics cards could not be read"
	}
	spec, ok := e.Devices.GPU(g)
	if !ok {
		return nil, fmt.Sprintf("no speed estimate: the %s is not in the advisor's list of graphics cards yet, so its memory speed is not known. A short test on this computer will measure it.", name)
	}
	ps, ok := e.Config.Paths[pl.Path]
	if pl.Path == hardware.PathVulkan {
		if v, found := e.Config.VulkanByVendor[g.Vendor]; found {
			ps, ok = v, true
		}
	}
	if !ok {
		return nil, fmt.Sprintf("no speed estimate: the advisor has no speed figures for the %q runtime path", pl.Path)
	}
	r := &speedRange{bwLow: spec.BandwidthGBs, bwHigh: spec.BandwidthGBs, effLow: ps.Efficiency.Low, effHigh: ps.Efficiency.High, ratio: ps.PromptRatio}
	if spec.Efficiency != nil {
		r.effLow, r.effHigh = spec.Efficiency.Low, spec.Efficiency.High
	}
	if spec.PromptRatio != nil {
		r.ratio = *spec.PromptRatio
	}
	r.label = fmt.Sprintf("%s: %s GB/s of memory bandwidth, of which %s reaches %.0f–%.0f%%",
		name, trimFloat(spec.BandwidthGBs), pl.Path, 100*r.effLow, 100*r.effHigh)
	return r, ""
}

// systemSpeed is the same for models (or parts of models) that run on the
// processor: the range of memory the processor's platform supports, and the
// cpu path's efficiency, halved-ish without AVX2.
func (e *Estimator) systemSpeed(p hardware.Profile, pl Placement) (*speedRange, string) {
	cpuName := hardware.MatchName(p.CPU.Model)
	if e.Devices == nil {
		return nil, "the table of processors could not be read"
	}
	ps := e.Config.Paths[hardware.PathCPU]
	r := &speedRange{effLow: ps.Efficiency.Low, effHigh: ps.Efficiency.High, ratio: ps.PromptRatio}

	// Apple Silicon's processor reads the same unified memory as its GPU.
	if p.UnifiedMemory {
		if g, ok := p.PrimaryGPU(); ok {
			if spec, found := e.Devices.GPU(g); found {
				r.bwLow, r.bwHigh = spec.BandwidthGBs, spec.BandwidthGBs
				r.label = fmt.Sprintf("%s: %s GB/s of memory bandwidth, of which the processor reaches %.0f–%.0f%%", cpuName, trimFloat(spec.BandwidthGBs), 100*r.effLow, 100*r.effHigh)
				return r, ""
			}
		}
		return nil, "this chip is not in the advisor's list yet, so its memory speed is not known"
	}

	spec, ok := e.Devices.SystemMemory(p.CPU)
	if !ok {
		if cpuName == "" || cpuName == hardware.Unknown {
			return nil, "the processor could not be identified, so its memory speed is not known"
		}
		return nil, fmt.Sprintf("the %s is not in the advisor's list of processors yet, so its memory speed is not known. A short test on this computer will measure it.", cpuName)
	}
	r.bwLow, r.bwHigh = spec.LowGBs, spec.HighGBs
	x86 := p.Arch == "amd64" || p.Arch == "386"
	avx := ""
	switch {
	case x86 && p.CPU.VectorKnown && !p.CPU.HasAVX2:
		r.effLow *= e.Config.NoAVX2Factor
		r.effHigh *= e.Config.NoAVX2Factor
		avx = "; no AVX2, which about halves it"
	case x86 && !p.CPU.VectorKnown:
		r.effLow *= e.Config.NoAVX2Factor
		avx = "; whether it has AVX2 could not be read, so the low end assumes not"
	}
	r.label = fmt.Sprintf("%s: %s–%s GB/s of memory bandwidth (%s; what is installed is not read), of which the processor reaches %.0f–%.0f%%%s",
		cpuName, trimFloat(spec.LowGBs), trimFloat(spec.HighGBs), spec.Memory, 100*r.effLow, 100*r.effHigh, avx)
	return r, ""
}

// roundRate keeps a rate to the precision it deserves: whole numbers above
// ten tokens a second, one decimal below.
func roundRate(v float64) float64 {
	if v >= 10 {
		return float64(int(v + 0.5))
	}
	return round1(v)
}

func trimFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

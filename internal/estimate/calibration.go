package estimate

import (
	"fmt"
	"math"
	"strconv"

	"advisor/internal/catalog"
	"advisor/internal/hardware"
)

// Calibration (build-plan step 6, ARCHITECTURE.md D-48): what this machine's
// own benchmarks say about its speed.
//
// Config.Paths gives each runtime path an efficiency RANGE because the parts
// a path drives do not agree with each other — the range is the spread of a
// population. One benchmark on this machine says where in (or outside) that
// spread this machine is: tokens a second × bytes read per token is the
// memory bandwidth the runtime actually reached here, and prompt tokens a
// second × parameters per token is the rate it reads a prompt at. For every
// other model on the same path, the estimate is then built from those two
// numbers instead of the population's, with a margin that grows with how far
// the model's size is from the one measured:
//
//	margin = CalibrationMargin + CalibrationMarginPerDoubling × |log2(size ÷ measured size)|
//
// capped at the population's own half-width (a measurement never makes a
// range wider than it was) and at CalibrationMaxMargin. A part that is not
// in gpus.yaml, which has no population range at all, gets an estimate for
// the first time. The exact configuration that was measured does not go
// through this: its estimate is replaced by the measurement itself
// (WithMeasurement).

// Observation is one benchmarked configuration on this machine, with the
// facts the speed model needs to turn it into achieved rates.
type Observation struct {
	// Label names what was measured, for the sentence ("llama3.1:8b").
	Label string
	Model Model
	Path  hardware.RuntimePath
	// Resident is true when the whole model was where Path puts it: every
	// layer on the graphics device, or (cpu) none on it. A split run says
	// nothing clean about either part and does not calibrate.
	Resident bool
	// ContextTokens is how full the cache was, on average, while the answer
	// was timed: the prompt and half the answer.
	ContextTokens int
	KVCacheType   KVCacheType
	GenerationTPS float64
	PromptTPS     float64 // 0 when not measured
}

// Calibration is the achieved rates per runtime path.
type Calibration struct {
	Points map[hardware.RuntimePath][]CalibrationPoint `json:"points"`
}

// CalibrationPoint is one measurement turned into rates.
type CalibrationPoint struct {
	Label string `json:"label"`
	// WeightsBytes is what the measured model reads per token: the size its
	// margin is scaled from.
	WeightsBytes uint64 `json:"weights_bytes" source:"n/a"` // a size of the measured model, from its file
	// GenerationGBs is the memory bandwidth generation reached, in GB/s.
	GenerationGBs float64 `json:"generation_gbs" source:"n/a"` // derived from a measurement; shown only as the rates it produces
	// PromptRate is prompt tokens a second × parameters used per token; 0
	// when the prompt was not measured.
	PromptRate float64 `json:"prompt_rate" source:"n/a"` // likewise
}

// Calibrate turns observations into a Calibration. Mixture-of-experts runs
// and runs that were split between graphics and processor are left out: the
// first read a share of their weights that the model of speed can only
// scale by a range, the second mix two parts' speeds. Observations are
// taken in the order given; the first of each label wins, so callers pass
// the newest first. nil when nothing calibrates.
func (e *Estimator) Calibrate(obs []Observation) *Calibration {
	c := &Calibration{Points: map[hardware.RuntimePath][]CalibrationPoint{}}
	seen := map[string]bool{}
	n := 0
	for _, o := range obs {
		key := string(o.Path) + "\x00" + o.Label
		if !o.Resident || o.GenerationTPS <= 0 || o.Model.File.Bytes == 0 || seen[key] || o.Path == "" {
			continue
		}
		if o.Model.File.Header.ExpertCount > 0 {
			continue
		}
		seen[key] = true
		weights := float64(o.Model.File.Bytes) * activeShare(o.Model)
		layout := o.Model.File.Layout
		if len(layout.Groups) == 0 && layout.RecurrentLayers == 0 {
			layout = catalog.NewLayout(o.Model.File.Header, nil)
		}
		kvType := o.KVCacheType
		if !kvType.Valid() {
			kvType = KVF16
		}
		cache := float64(e.kvBytes(layout, max(o.ContextTokens, 0), kvType))
		p := CalibrationPoint{
			Label:         o.Label,
			WeightsBytes:  uint64(weights),
			GenerationGBs: o.GenerationTPS * (weights + cache) / 1e9,
		}
		params := float64(o.Model.Size.Parameters)
		if o.Model.Size.ActiveParameters > 0 {
			params = float64(o.Model.Size.ActiveParameters)
		}
		if o.PromptTPS > 0 && params > 0 {
			p.PromptRate = o.PromptTPS * params
		}
		c.Points[o.Path] = append(c.Points[o.Path], p)
		n++
	}
	if n == 0 {
		return nil
	}
	return c
}

// withCalibration returns the range for one part of the machine: the
// population's (pop, which may be nil when the part is not in gpus.yaml)
// unless this machine's own benchmarks on the same path say better.
func (e *Estimator) withCalibration(pop *speedRange, path hardware.RuntimePath, weightsGB float64) *speedRange {
	if e.Calibration == nil || weightsGB <= 0 {
		return pop
	}
	points := e.Calibration.Points[path]
	if len(points) == 0 {
		return pop
	}
	// The measurement nearest in size says the most about this model.
	best, bestDist := points[0], math.Inf(1)
	for _, p := range points {
		if p.WeightsBytes == 0 {
			continue
		}
		d := math.Abs(math.Log2(weightsGB * 1e9 / float64(p.WeightsBytes)))
		if d < bestDist {
			best, bestDist = p, d
		}
	}
	if math.IsInf(bestDist, 1) || best.GenerationGBs <= 0 {
		return pop
	}
	cfg := e.Config
	margin := cfg.CalibrationMargin + cfg.CalibrationMarginPerDoubling*bestDist
	limit := cfg.CalibrationMaxMargin
	if pop != nil && pop.high > 0 {
		if half := (pop.high - pop.low) / (pop.high + pop.low); half < limit {
			limit = half
		}
	}
	if margin > limit {
		margin = limit
	}
	r := &speedRange{low: best.GenerationGBs * (1 - margin), high: best.GenerationGBs * (1 + margin)}
	switch {
	case best.PromptRate > 0:
		r.promptLow, r.promptHigh = best.PromptRate*(1-margin), best.PromptRate*(1+margin)
	case pop != nil:
		r.promptLow, r.promptHigh = pop.promptLow, pop.promptHigh
	}
	b := best
	r.calibrated = &b
	r.label = fmt.Sprintf("measured on this computer: while answering, %s read %s GB/s of memory (%s), so this model is estimated within %.0f%% of that",
		best.Label, strconv.FormatFloat(round1(best.GenerationGBs), 'f', -1, 64), path, 100*margin)
	return r
}

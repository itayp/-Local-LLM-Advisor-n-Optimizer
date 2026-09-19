package estimate

import (
	"fmt"
	"math"
	"strconv"

	"advisor/internal/catalog"
	"advisor/internal/figure"
)

// Estimator is the configured estimator: the constants and the device table.
type Estimator struct {
	Config  Config
	Devices *DeviceTable
}

// New returns the estimator the advisor ships with: DefaultConfig and the
// embedded gpus.yaml.
func New() (*Estimator, error) {
	d, err := DefaultDevices()
	if err != nil {
		return nil, err
	}
	return &Estimator{Config: DefaultConfig(), Devices: d}, nil
}

// Fit estimates one model file at one context on one machine: the memory
// terms, where they would live, how fast it would run, and which of the
// five categories that adds up to — with the threshold that decided it.
func (e *Estimator) Fit(m Machine, model Model, req Request) Estimate {
	return e.FitPlaced(e.Place(m), m, model, req)
}

// terms is the memory arithmetic before it is compared with anything.
type terms struct {
	ctx       int    // effective context
	weights   uint64 // language-model file
	projector uint64 // vision encoder loaded beside it
	hostOnly  uint64 // part of the weights that stays in system memory by design
	kv        uint64
	overhead  uint64
	layers    int
}

func (t terms) total() uint64 { return t.weights + t.projector + t.kv + t.overhead }

// device is what has to live where the plan put the model.
func (t terms) device() uint64 { return t.total() - t.hostOnly }

// FitPlaced is Fit for a placement already decided (the recommendation
// engine places once and fits many models).
func (e *Estimator) FitPlaced(pl Placement, m Machine, model Model, req Request) Estimate {
	cfg := e.Config
	if req.NumCtx <= 0 {
		req.NumCtx = cfg.MinUsefulContext
	}
	if !req.KVCacheType.Valid() {
		req.KVCacheType = KVF16
	}
	req.CatalogFileID = model.File.ID
	req.RuntimePath = pl.Path

	est := Estimate{
		Request:     req,
		BudgetBytes: pl.BudgetBytes,
		BudgetKnown: pl.BudgetKnown,
		BudgetKind:  pl.BudgetKind,
		Basis:       Basis{PathSource: pl.PathSource, BudgetKnown: pl.BudgetKnown, SpeedSource: SpeedUnknown},
	}
	note := func(format string, args ...any) { est.Notes = append(est.Notes, fmt.Sprintf(format, args...)) }
	est.Notes = append(est.Notes, pl.Notes...)

	h := model.File.Header
	layout := model.File.Layout
	if len(layout.Groups) == 0 && layout.RecurrentLayers == 0 {
		layout = catalog.NewLayout(h, nil)
	}
	est.Notes = append(est.Notes, layout.Notes...)

	// ctx = min(num_ctx, the model's trained context): Ollama clamps, and
	// estimating a cache it never allocates was step 0's +29% row.
	trained := h.ContextLength
	if trained <= 0 {
		trained = model.Size.ContextLength
	}
	ctx := req.NumCtx
	if trained > 0 && ctx > trained {
		ctx = trained
		note("the context asked for (%s) is longer than the model was trained for; Ollama clamps it to %s, and so does this estimate", commas(req.NumCtx), commas(trained))
	}

	t := terms{
		ctx:      ctx,
		weights:  model.File.Bytes,
		kv:       e.kvBytes(layout, ctx, req.KVCacheType),
		overhead: cfg.Overhead[pl.Path],
		layers:   h.BlockCount,
	}
	if model.Projector != nil {
		t.projector = model.Projector.Bytes
		note("includes the model's vision encoder (%s), which the runtime loads beside the weights", gb(t.projector))
	}
	perLayerTables := model.Size.ActiveParameters > 0 && model.Size.Parameters > model.Size.ActiveParameters && h.ExpertCount == 0
	if perLayerTables && pl.BudgetKind != BudgetSystem {
		// Gemma's "E" sizes: the per-layer embedding tables are looked up,
		// not multiplied, and the runtime keeps them in system memory. The
		// model card's effective parameter count is the share that computes.
		onDevice := uint64(float64(t.weights) * float64(model.Size.ActiveParameters) / float64(model.Size.Parameters))
		t.hostOnly = t.weights - onDevice
		note("about %s of this model are per-layer lookup tables that stay in ordinary memory by design; they do not slow it down", gb(t.hostOnly))
	}
	if h.ExpertCount > 0 {
		note("a mixture-of-experts model: every expert has to be in memory, but only some are used for each word, so it runs faster than its size suggests")
	}
	if req.KVCacheType != KVF16 {
		note("estimated for a %s cache; step 0 measured the default (f16) only", req.KVCacheType)
	}

	switch {
	case layout.Basis == catalog.LayoutIncomplete:
		est.Basis.MemoryModel = MemoryIncomplete
	case layout.Basis == catalog.LayoutUniform && h.ExpertCount == 0 && model.Projector == nil && !perLayerTables && req.KVCacheType == KVF16:
		est.Basis.MemoryModel = MemoryValidated
	default:
		est.Basis.MemoryModel = MemoryModelled
	}

	est.Memory = Memory{
		Weights:      figure.EstimatedBytes(t.weights + t.projector),
		KVCache:      figure.EstimatedBytes(t.kv),
		Overhead:     figure.EstimatedBytes(t.overhead),
		Total:        figure.EstimatedBytes(t.total()),
		EffectiveCtx: ctx,
	}

	gpuShare := e.categorise(&est, pl, t, layout, req.KVCacheType)
	est.Speed = e.speed(pl, m, model, t, gpuShare, est.Category)
	if est.Speed.Known {
		est.Basis.SpeedSource = SpeedEstimated
	}
	return est
}

// categorise compares the terms with the placement's budget, fills in the
// category, the threshold sentence and the split, and returns the share of
// the model's layers that runs on the graphics device (1 = all, 0 = none).
func (e *Estimator) categorise(est *Estimate, pl Placement, t terms, layout catalog.Layout, kvType KVCacheType) float64 {
	cfg := e.Config
	total := t.total()

	if !pl.BudgetKnown {
		est.Category = CategoryUnknown
		est.Threshold = "the memory this model would run in could not be read, so there is nothing to compare its " + gb(total) + " against"
		est.Memory.GPUResident = figure.EstimatedBytes(0)
		est.Memory.CPUOffload = figure.EstimatedBytes(0)
		return 0
	}

	budget := pl.BudgetBytes
	headLimit := uint64(float64(budget) * cfg.HeadroomFraction)
	fitLimit := uint64(float64(budget) * cfg.FitsFraction)
	what := map[BudgetKind]string{
		BudgetGraphics: "of graphics memory",
		BudgetUnified:  "the graphics can use",
		BudgetSystem:   "of memory left after the system's own needs",
	}[pl.BudgetKind]
	compare := func(need uint64) string {
		return fmt.Sprintf("%s needed of %s %s (%s)", gb(need), gb(budget), what, percent(need, budget))
	}

	onDevice := pl.BudgetKind != BudgetSystem
	place := func(device, host uint64) {
		if onDevice {
			est.Memory.GPUResident = figure.EstimatedBytes(device)
			est.Memory.CPUOffload = figure.EstimatedBytes(host)
		} else {
			est.Memory.GPUResident = figure.EstimatedBytes(0)
			est.Memory.CPUOffload = figure.EstimatedBytes(device + host)
		}
	}

	need := t.device()
	hostOK := t.hostOnly == 0 || !pl.SystemKnown || t.hostOnly <= pl.SystemBytes
	switch {
	case need <= headLimit && hostOK:
		est.Category = FitsWithHeadroom
		est.Threshold = compare(need) + ": at or under " + percentOf(cfg.HeadroomFraction) + " fits with headroom"
		place(need, t.hostOnly)
		return share(onDevice, 1)
	case need <= fitLimit && hostOK:
		est.Category = Fits
		est.Threshold = compare(need) + ": at or under " + percentOf(cfg.FitsFraction) + " fits"
		place(need, t.hostOnly)
		return share(onDevice, 1)
	}

	// It does not fit as asked. A shorter context that fits keeps the whole
	// model where it runs fastest, so that is looked for before splitting.
	if hostOK {
		for i := len(cfg.ContextLadder) - 1; i >= 0; i-- {
			c := cfg.ContextLadder[i]
			if c >= t.ctx || c < cfg.MinUsefulContext {
				continue
			}
			shorter := t
			shorter.ctx, shorter.kv = c, e.kvBytes(layout, c, kvType)
			if shorter.device() <= fitLimit {
				est.Category = ReducedContextOnly
				est.SuggestedCtx = c
				est.Threshold = fmt.Sprintf("%s: over the %s that fits; at a context of %s it needs %s, which fits",
					compare(need), percentOf(cfg.FitsFraction), commas(c), gb(shorter.device()))
				place(need, t.hostOnly)
				return share(onDevice, 1)
			}
		}
	}

	if !onDevice {
		est.Category = NotRecommended
		est.Threshold = compare(need) + ": more than this computer's memory can hold, even at the shortest useful context"
		place(need, t.hostOnly)
		return 0
	}

	// Split: the runtime puts whole layers on the graphics device until it is
	// full and runs the rest on the processor. Each layer costs its share of
	// the weights and of the cache; the path's overhead and the vision
	// encoder stay on the device.
	fixed := t.overhead + t.projector
	perLayer := uint64(1)
	if t.layers > 0 {
		perLayer = (t.weights - t.hostOnly + t.kv) / uint64(t.layers)
	}
	layersOn := 0
	if fitLimit > fixed && perLayer > 0 {
		layersOn = int((fitLimit - fixed) / perLayer)
	}
	if layersOn > t.layers {
		layersOn = t.layers
	}
	device := fixed + uint64(layersOn)*perLayer
	if layersOn == 0 {
		device = 0
	}
	host := total - device
	place(device, host)

	// What spills has to fit in system memory — on Apple Silicon the same
	// memory the graphics share, so the whole model has to.
	hostNeed := host
	if pl.BudgetKind == BudgetUnified {
		hostNeed = total
	}
	switch {
	case pl.SystemKnown && hostNeed > pl.SystemBytes:
		est.Category = NotRecommended
		est.Threshold = fmt.Sprintf("%s: over the %s that fits, and the %s that would spill is more than the %s of ordinary memory left after the system's own needs",
			compare(need), percentOf(cfg.FitsFraction), gb(hostNeed), gb(pl.SystemBytes))
		return 0
	case layersOn == 0:
		est.Category = NotRecommended
		est.Threshold = compare(need) + ": not even one layer fits beside the runtime's own overhead, so the graphics would not help"
		return 0
	default:
		est.Category = NeedsCPUOffload
		est.Threshold = fmt.Sprintf("%s: over the %s that fits; about %d of %d layers fit on the graphics, the rest (%s) runs on the processor",
			compare(need), percentOf(cfg.FitsFraction), layersOn, t.layers, gb(host))
		if !pl.SystemKnown {
			est.Notes = append(est.Notes, "how much ordinary memory this computer has could not be read, so whether the spilled part fits there is not checked")
		}
		return float64(layersOn) / float64(t.layers)
	}
}

func share(onDevice bool, s float64) float64 {
	if !onDevice {
		return 0
	}
	return s
}

// kvBytes is the context-dependent memory: the key/value cache of every
// layer that keeps one, plus hybrid models' fixed recurrent state. For a
// uniform layout this is exactly D-20's
// 2 · block_count · head_count_kv · head_dim · ctx · 2 B.
func (e *Estimator) kvBytes(l catalog.Layout, ctx int, kvType KVCacheType) uint64 {
	cfg := e.Config
	per := cfg.KVBytesPerElement[kvType]
	if per == 0 {
		per = cfg.KVBytesPerElement[KVF16]
	}
	var elements float64
	for _, g := range l.Groups {
		cells := ctx
		if g.Kind == catalog.LayersSliding && g.Window > 0 {
			window := g.Window + cfg.SlidingBatchTokens
			if pad := cfg.SlidingPadTokens; pad > 0 {
				window = (window + pad - 1) / pad * pad
			}
			if window < cells {
				cells = window
			}
		}
		elements += float64(g.Layers) * float64(g.KVHeads) * float64(g.KeyLength+g.ValueLength) * float64(cells)
	}
	kv := uint64(math.Round(elements * per))
	kv += uint64(l.RecurrentLayers) * l.RecurrentStateElements * cfg.RecurrentBytesPerElement
	return kv
}

// Measurement is what a benchmark of the same configuration on the same
// machine found (build-plan step 6 writes it back).
type Measurement struct {
	GenerationTPS float64
	PromptTPS     float64
	PeakBytes     uint64 // peak device (or process) memory; 0 when no sampler could read one
}

// WithMeasurement replaces what was estimated with what was measured:
// product rule 4's second sentence. The speed range narrows to a point and
// its Source flips; a measured peak replaces the estimated total. The
// estimated terms stay, as estimates, for the Advanced view.
func (est Estimate) WithMeasurement(m Measurement) Estimate {
	if m.GenerationTPS > 0 {
		r := figure.MeasuredRate(round1(m.GenerationTPS), "tok/s")
		est.Speed.Generation = &r
		est.Speed.Known, est.Speed.Unknown = true, ""
		est.Speed.Basis = "measured on this computer"
		est.Basis.SpeedSource = SpeedMeasured
		if m.PromptTPS > 0 {
			p := figure.MeasuredRate(round1(m.PromptTPS), "tok/s")
			est.Speed.Prompt = &p
		}
	}
	if m.PeakBytes > 0 {
		est.Memory.Total = figure.MeasuredBytes(m.PeakBytes)
		est.Basis.MemoryModel = MemoryMeasured
	}
	return est
}

// OllamaDefaultContext is the context Ollama runs a model at when nobody
// set one: 4k, 32k or 256k by total graphics memory (ollama/ollama
// server/routes.go at 6383a0f, 2026-09-18: thresholds 23 and 47 GiB), which
// is what a beginner gets. The model's trained context still clamps it.
func OllamaDefaultContext(pl Placement) int {
	if pl.BudgetKind == BudgetSystem || !pl.BudgetKnown {
		return 4096
	}
	switch {
	case pl.BudgetBytes >= 47*gib:
		return 262144
	case pl.BudgetBytes >= 23*gib:
		return 32768
	}
	return 4096
}

// gb writes a byte count the way the UI's Figure does: binary gigabytes,
// one decimal, labelled GB — the convention every OS and vendor tool in
// internal/hardware uses for memory.
func gb(b uint64) string {
	g := float64(b) / gib
	if g < 1 {
		return strconv.FormatFloat(float64(b)/mib, 'f', 0, 64) + " MB"
	}
	return strconv.FormatFloat(g, 'f', 1, 64) + " GB"
}

func percent(part, whole uint64) string {
	if whole == 0 {
		return "—"
	}
	return strconv.FormatFloat(100*float64(part)/float64(whole), 'f', 0, 64) + "%"
}

func percentOf(f float64) string { return strconv.FormatFloat(100*f, 'f', 0, 64) + "%" }

func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

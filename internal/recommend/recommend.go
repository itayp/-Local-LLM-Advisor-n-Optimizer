// Package recommend turns a machine, the user's purposes and the catalogue
// into at most three recommendations, each with reasons written for the
// customer (product rule 3) and a confidence derived from what was measured,
// estimated or unknown (PRD §21, last risk). Build-plan step 5.
//
// The advisor calls no LLM to do this job: recommendation is rules over data.
// If a step finds itself wanting a model to decide, the catalogue is missing
// a field — add the field. The reasons are templated from the facts the
// rules used (reasons.go), which keeps them true and testable
// (ARCHITECTURE.md D-8).
//
// The files:
//
//	recommend.go   the types the API serves, and Recommend
//	config.go      every weight and threshold, each saying what it encodes
//	reasons.go     the sentences, the comparison with the model the user has,
//	               and the confidence
package recommend

import (
	"math"
	"sort"
	"strings"

	"advisor/internal/catalog"
	"advisor/internal/estimate"
	"advisor/internal/figure"
)

// Confidence is derived from which inputs were measured, estimated or
// unknown. The UI shows it on every card.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Preferences are the user's standing choices that shape a recommendation.
type Preferences struct {
	// CurrentModel is the installed model the user runs today, by the
	// runtime's name for it ("llama3.1:8b"). Empty: the engine takes the
	// installed model that serves the purposes best. Every recommendation
	// then says what it changes versus that model, or is dropped.
	CurrentModel string `json:"current_model,omitempty"`
	// MinContext is the smallest context the user needs (0: no preference).
	MinContext int `json:"min_context" source:"n/a"` // configuration
	// GPUOnly: never suggest a configuration that spills onto the processor,
	// even when nothing else fits.
	GPUOnly bool `json:"gpu_only"`
	// AllowSplit: let models that spill onto the processor compete with the
	// ones that fit. Off by default: a beginner is not steered to a model
	// that runs several times slower while one that fits exists; such models
	// are only suggested when nothing fits at all.
	AllowSplit bool `json:"allow_split"`
}

// Entry is one catalogue size with the family fields the engine reads.
type Entry struct {
	FamilyID    string
	DisplayName string
	Purposes    []catalog.Purpose // most credible first, as families.yaml lists them
	Model       catalog.Model     // with its files
}

// InstalledModel is a model the runtime has on disk, with how it maps onto
// the catalogue (store.InstalledModelRow, less what the engine does not read).
type InstalledModel struct {
	Name           string
	SizeBytes      uint64
	ParameterSize  string // as the runtime reports it: "8.0B"
	CatalogModelID int64  // 0: the catalogue does not know this size
	CatalogFileID  int64  // 0: the catalogue does not know this quant
}

// Reason is one line of the "why", written for someone who does not know
// what a KV cache is: "Fits your 16 GB graphics card with room for long
// documents.", "About 5 GB to download."
type Reason struct {
	Text string `json:"text"`
	// Kind lets the UI pick an icon: warning | fit | purpose | speed | size |
	// change.
	Kind string `json:"kind"`
	// Explainer names the explainer the UI links from this reason (step 7's
	// glossary owns the page; "gpu_not_used" is the one this step needs), and
	// Detail is what that explainer says about THIS machine — why the card
	// is not used, as far as the facts go.
	Explainer string `json:"explainer,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// Recommendation is one card.
type Recommendation struct {
	FamilyID    string        `json:"family_id"`
	DisplayName string        `json:"display_name"` // "Qwen3.5 9B"
	Model       catalog.Model `json:"model"`        // the size, without its file list
	File        catalog.File  `json:"file"`         // the weights file recommended
	// PullName is the exact name to give the runtime — what "Use it" copies.
	PullName string `json:"pull_name"`
	// NumCtx is the context to run it at. Configuration.
	NumCtx   int               `json:"num_ctx" source:"n/a"`
	Estimate estimate.Estimate `json:"estimate"`
	// DownloadBytes is what it costs to get: the weights file plus the vision
	// encoder, from the catalogue's listing — a fact, shown as-is. 0 with
	// Installed true when it is already on this computer.
	DownloadBytes uint64 `json:"download_bytes" source:"n/a"`
	Installed     bool   `json:"installed"`
	// Speed is the headline number on the card: the estimated range, or the
	// measurement once one exists. Absent when there is no estimate — the
	// speed reason says so in words.
	Speed   *figure.Rate `json:"speed,omitempty"`
	Reasons []Reason     `json:"reasons"`

	Confidence Confidence `json:"confidence"`
	// ConfidenceWhy says, in plain words, which inputs limit the confidence.
	ConfidenceWhy string `json:"confidence_why"`

	// Score is internal ranking material; the UI shows the order, not the
	// number. Factors is its breakdown, for the Advanced view and the tests.
	Score   float64 `json:"score" source:"n/a"`
	Factors Factors `json:"factors"`

	// VersusCurrent is what this changes compared with the model the user
	// has, in one sentence; empty when the user has none.
	VersusCurrent string `json:"versus_current,omitempty"`
}

// Factors are the four terms of the score. Internal ranking material.
type Factors struct {
	Purpose float64 `json:"purpose" source:"n/a"`
	Fit     float64 `json:"fit" source:"n/a"`
	Speed   float64 `json:"speed" source:"n/a"`
	Size    float64 `json:"size" source:"n/a"`
}

// Current is the installed model the recommendations were compared with.
type Current struct {
	Name        string `json:"name"`
	InCatalogue bool   `json:"in_catalogue"`
	// Estimate is how the current model does on this machine, when the
	// catalogue knows its file.
	Estimate *estimate.Estimate `json:"estimate,omitempty"`
	// Verdict is one sentence about keeping it.
	Verdict string `json:"verdict"`
}

// Result is the API response for GET /api/recommend.
type Result struct {
	Purposes        []catalog.Purpose `json:"purposes"`
	Recommendations []Recommendation  `json:"recommendations"` // at most three
	// Warning is set when the honest answer needs a preface — a graphics card
	// Ollama is not using, a machine that runs models on its processor (weak
	// hardware is a tier, not an error: product rule 6).
	Warning string `json:"warning,omitempty"`
	// GPUNotUsed accompanies the warning when a graphics card exists and the
	// numbers are without it: the card and why, for the explainer.
	GPUNotUsed *estimate.UnusedGPU `json:"gpu_not_used,omitempty"`
	// Current is the installed model every recommendation was compared with.
	Current *Current `json:"current,omitempty"`
	// Empty says why there are no recommendations, when there are none, in
	// words; EmptyCode is the same for the UI's logic (it offers to fetch the
	// model list when that is what is missing).
	Empty     string    `json:"empty,omitempty"`
	EmptyCode EmptyCode `json:"empty_code,omitempty"`
	// RuntimePath and PathSource say what the numbers were built for.
	RuntimePath string              `json:"runtime_path"`
	PathSource  estimate.PathSource `json:"path_source"`
}

// EmptyCode is why a Result has no recommendations.
type EmptyCode string

const (
	EmptyBlocked        EmptyCode = "blocked"         // nothing can run here (the OS is older than the runtime supports)
	EmptyBudgetUnknown  EmptyCode = "budget_unknown"  // the memory a model would run in could not be read
	EmptyNoCatalogue    EmptyCode = "catalogue_empty" // the model list has never been fetched
	EmptyNothingFits    EmptyCode = "nothing_fits"    // no model in the list fits this machine
	EmptyNothingChanges EmptyCode = "nothing_changes" // nothing would be a real change from the model they have
)

// Engine is the configured recommendation engine.
type Engine struct {
	Estimator *estimate.Estimator
	Config    Config
	// Catalogue is every present catalogue size with its files.
	Catalogue []Entry
	// Measurements, when step 6 has any, are keyed by catalogue file id and
	// context; a measured configuration's estimate is replaced before it is
	// scored (product rule 4).
	Measurements map[MeasurementKey]estimate.Measurement
}

// MeasurementKey identifies a benchmarked configuration on this machine.
type MeasurementKey struct {
	CatalogFileID int64
	NumCtx        int
}

// New returns an engine with the shipped configuration.
func New(catalogue []Entry) (*Engine, error) {
	est, err := estimate.New()
	if err != nil {
		return nil, err
	}
	return &Engine{Estimator: est, Config: DefaultConfig(), Catalogue: catalogue}, nil
}

// candidate is one catalogue size, fitted and scored.
type candidate struct {
	entry     Entry
	file      catalog.File
	projector *catalog.File
	ctx       int
	wanted    int
	est       estimate.Estimate
	factors   Factors
	score     float64
	installed bool
	// purposesAsked is what the user asked for, kept for the comparison with
	// the model they have.
	purposesAsked []catalog.Purpose
}

// Recommend is the engine: at most Config.MaxRecommendations cards for this
// machine and these purposes, best first.
func (e *Engine) Recommend(m estimate.Machine, purposes []catalog.Purpose, installed []InstalledModel, prefs Preferences) Result {
	purposes = cleanPurposes(purposes)
	pl := e.Estimator.Place(m)
	res := Result{Purposes: purposes, Recommendations: []Recommendation{}, RuntimePath: string(pl.Path), PathSource: pl.PathSource}

	if pl.Blocked != "" {
		res.Empty, res.EmptyCode = pl.Blocked, EmptyBlocked
		return res
	}
	res.Warning, res.GPUNotUsed = warning(m, pl), pl.Unused
	if !pl.BudgetKnown {
		res.Empty = "The advisor could not read how much memory this computer can give a model, so it cannot say what fits. \"Your computer\" lists what could not be read."
		res.EmptyCode = EmptyBudgetUnknown
		return res
	}

	resolved := 0
	var cands []candidate
	for _, entry := range e.Catalogue {
		file, projector, ok := e.defaultFile(entry)
		if !ok {
			continue
		}
		resolved++
		if c, ok := e.consider(m, pl, entry, file, projector, purposes, prefs, false); ok {
			c.installed = isInstalled(installed, entry.Model.ID)
			cands = append(cands, c)
		}
	}
	if resolved == 0 {
		res.Empty = "The list of models has not been fetched yet, so there is nothing to recommend from. Fetching it downloads descriptions only, never the models themselves."
		res.EmptyCode = EmptyNoCatalogue
		return res
	}
	if len(cands) == 0 {
		// Product rule 6: weak hardware is a tier, not an error. Before saying
		// nothing fits, let models that spill or run short be considered.
		for _, entry := range e.Catalogue {
			if file, projector, ok := e.defaultFile(entry); ok {
				if c, ok := e.consider(m, pl, entry, file, projector, purposes, prefs, true); ok {
					c.installed = isInstalled(installed, entry.Model.ID)
					cands = append(cands, c)
				}
			}
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].file.Bytes < cands[j].file.Bytes // a tie goes to the smaller download
	})

	cur := e.current(m, pl, installed, purposes, prefs)
	if cur != nil {
		res.Current = &cur.Current
	}

	seenFamily := map[string]bool{}
	for _, c := range cands {
		if len(res.Recommendations) >= e.Config.MaxRecommendations {
			break
		}
		if e.Config.OnePerFamily && seenFamily[c.entry.FamilyID] {
			continue
		}
		versus := ""
		if cur != nil {
			var changes bool
			versus, changes = e.versus(c, cur)
			if !changes {
				continue // says nothing new against the model the user has: dropped
			}
		}
		seenFamily[c.entry.FamilyID] = true
		res.Recommendations = append(res.Recommendations, e.card(c, pl, m, purposes, versus))
	}

	if len(res.Recommendations) == 0 {
		switch {
		case cur != nil && len(cands) > 0:
			res.Empty = "Nothing in the list would be a real change from " + cur.Name + " for this, on this computer. " + cur.Verdict
			res.EmptyCode = EmptyNothingChanges
		case len(cands) == 0 && e.servesAny(purposes):
			res.Empty = "Even the smallest model in the list for this needs more memory than this computer can give it."
			res.EmptyCode = EmptyNothingFits
		default:
			res.Empty = "Nothing in the list is made for this yet."
			res.EmptyCode = EmptyNothingFits
		}
	}
	return res
}

// servesAny reports whether any catalogue family lists one of the purposes.
func (e *Engine) servesAny(purposes []catalog.Purpose) bool {
	for _, entry := range e.Catalogue {
		for _, p := range entry.Purposes {
			for _, q := range purposes {
				if p == q {
					return true
				}
			}
		}
	}
	return false
}

// cleanPurposes drops what is not a purpose and repeats; nothing asked for
// means general chat, the purpose every beginner has.
func cleanPurposes(in []catalog.Purpose) []catalog.Purpose {
	var out []catalog.Purpose
	seen := map[catalog.Purpose]bool{}
	for _, p := range in {
		if p.Valid() && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []catalog.Purpose{catalog.PurposeChat}
	}
	return out
}

// defaultFile is the weights file a size's Ollama tag pulls, and the vision
// encoder loaded beside it.
func (e *Engine) defaultFile(entry Entry) (file catalog.File, projector *catalog.File, ok bool) {
	if !entry.Model.Present {
		return file, nil, false
	}
	for _, q := range e.Config.DefaultQuants {
		for _, f := range entry.Model.Files {
			if f.Present && f.Role == catalog.RoleModel && strings.EqualFold(f.Quant, q) {
				file, ok = f, true
				break
			}
		}
		if ok {
			break
		}
	}
	if !ok {
		return file, nil, false
	}
	for i := range entry.Model.Files {
		if f := entry.Model.Files[i]; f.Present && f.Role == catalog.RoleProjector {
			projector = &f
			break
		}
	}
	return file, projector, true
}

func isInstalled(installed []InstalledModel, modelID int64) bool {
	for _, in := range installed {
		if modelID != 0 && in.CatalogModelID == modelID {
			return true
		}
	}
	return false
}

// consider fits one catalogue size at the longest context its purposes want
// that still fits, and scores it. relaxed admits what the first pass leaves
// out: a model that only runs split or short.
func (e *Engine) consider(m estimate.Machine, pl estimate.Placement, entry Entry, file catalog.File, projector *catalog.File,
	purposes []catalog.Purpose, prefs Preferences, relaxed bool) (candidate, bool) {

	cfg := e.Config
	c := candidate{entry: entry, file: file, projector: projector, purposesAsked: purposes}
	model := estimate.Model{File: file, Projector: projector, Size: entry.Model.Size}

	// The context the purposes want, within what the model was trained for.
	for _, p := range purposes {
		if w := cfg.PurposeContext[p]; w > c.wanted {
			c.wanted = w
		}
	}
	if prefs.MinContext > c.wanted {
		c.wanted = prefs.MinContext
	}
	trained := file.Header.ContextLength
	if trained <= 0 {
		trained = entry.Model.Size.ContextLength
	}
	ladder := e.Estimator.Config.ContextLadder
	floor := e.Estimator.Config.MinUsefulContext

	// Longest first: the first context that fits wholly is the one to run at.
	found := false
	for i := len(ladder) - 1; i >= 0; i-- {
		ctx := ladder[i]
		if ctx > c.wanted || ctx < floor || (trained > 0 && ctx > trained) {
			continue
		}
		est := e.fit(m, pl, model, ctx)
		if est.Category.FitsOnDevice() {
			c.ctx, c.est, found = ctx, est, true
			break
		}
	}
	if !found {
		c.ctx = floor
		c.est = e.fit(m, pl, model, floor)
		switch c.est.Category {
		case estimate.NeedsCPUOffload:
			if prefs.GPUOnly || !(relaxed || prefs.AllowSplit) {
				return c, false
			}
		default:
			return c, false // not recommended, or unknown
		}
	}
	if prefs.MinContext > 0 && c.ctx < prefs.MinContext && !relaxed {
		return c, false
	}

	c.factors = Factors{
		Purpose: e.purposeFit(entry, projector != nil, purposes, c.ctx, c.wanted),
		Fit:     cfg.FitFactor[c.est.Category],
		Speed:   e.speedFactor(c.est),
		Size:    e.sizeFactor(entry.Model.Size, file.Header),
	}
	if c.factors.Purpose == 0 || c.factors.Fit == 0 {
		return c, false
	}
	w := cfg.Weights
	c.score = math.Pow(c.factors.Purpose, w.Purpose) * math.Pow(c.factors.Fit, w.Fit) *
		math.Pow(c.factors.Speed, w.Speed) * math.Pow(c.factors.Size, w.Size)
	return c, true
}

// fit is Estimator.FitPlaced, with a measurement of the same configuration
// replacing the estimate when one exists.
func (e *Engine) fit(m estimate.Machine, pl estimate.Placement, model estimate.Model, ctx int) estimate.Estimate {
	est := e.Estimator.FitPlaced(pl, m, model, estimate.Request{NumCtx: ctx, KVCacheType: estimate.KVF16})
	if meas, ok := e.Measurements[MeasurementKey{model.File.ID, ctx}]; ok && model.File.ID != 0 {
		est = est.WithMeasurement(meas)
	}
	return est
}

func (e *Engine) purposeFit(entry Entry, hasProjector bool, purposes []catalog.Purpose, ctx, wanted int) float64 {
	cfg := e.Config
	sum := 0.0
	for _, p := range purposes {
		rank := -1
		for i, q := range entry.Purposes {
			if q == p {
				rank = i
				break
			}
		}
		switch {
		case rank < 0:
		case p == catalog.PurposeLongContext && ctx < cfg.LongContextMin:
			// It is a long-context family, but not on this machine.
		case p == catalog.PurposeVision && !hasProjector:
			// The catalogue has no vision encoder for this size: it cannot look at images here.
		default:
			sum += math.Max(cfg.PurposeFloor, 1-cfg.PurposeRankStep*float64(rank))
		}
	}
	fit := sum / float64(len(purposes))
	if wanted > 0 && ctx < wanted {
		fit *= math.Sqrt(float64(ctx) / float64(wanted))
	}
	return fit
}

func (e *Engine) speedFactor(est estimate.Estimate) float64 {
	cfg := e.Config
	if !est.Speed.Known || est.Speed.Generation == nil {
		return cfg.UnknownSpeedFactor
	}
	g := est.Speed.Generation
	middle := g.Value
	if g.Low > 0 && g.High > g.Low {
		middle = math.Sqrt(g.Low * g.High)
	}
	return math.Max(cfg.SpeedFloor, math.Min(1, middle/cfg.ComfortableTPS))
}

// effectiveBillions is the parameter count a model is "worth" (Config.
// SizeReferenceBillions says why).
func effectiveBillions(size catalog.Size, h catalog.GGUFHeader) float64 {
	total, active := float64(size.Parameters)/1e9, float64(size.ActiveParameters)/1e9
	switch {
	case active <= 0 || active >= total:
		return total
	case h.ExpertCount > 0:
		return math.Sqrt(total * active)
	default:
		return active // per-layer embeddings: the model card's effective size
	}
}

func (e *Engine) sizeFactor(size catalog.Size, h catalog.GGUFHeader) float64 {
	b := effectiveBillions(size, h)
	if b <= 0 {
		return 0
	}
	return math.Min(1, math.Log2(1+b)/math.Log2(1+e.Config.SizeReferenceBillions))
}

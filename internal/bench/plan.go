package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"advisor/internal/backend"
	"advisor/internal/catalog"
	"advisor/internal/estimate"
	"advisor/internal/figure"
	"advisor/internal/hardware"
	"advisor/internal/store"
)

// Target is the machine and the runtime a run is planned for, as the daemon
// knows them at the moment of the request: the backend, the machine with
// the path its runtime was last seen taking, an estimator carrying this
// machine's calibration, and this start's hardware profile.
type Target struct {
	Backend     backend.Backend
	Machine     estimate.Machine
	Estimator   *estimate.Estimator
	ProfileID   int64
	Fingerprint string
}

// Error is a run the harness will not start, with the reason in words for
// the user; errors.Is matches its Kind.
type Error struct {
	Kind    error
	Message string
	// Plan is set when the refusal comes from the plan (ErrRefused,
	// ErrNothingFits), so the caller can show what it said.
	Plan *Plan
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Kind }

// The kinds of Error.
var (
	ErrBadRequest        = errors.New("bench: bad request")
	ErrBusy              = errors.New("bench: a benchmark is already running")
	ErrBackendNotRunning = errors.New("bench: the runtime is not running")
	ErrModelNotInstalled = errors.New("bench: the model is not installed")
	ErrNotTextModel      = errors.New("bench: the model does not generate text")
	ErrNothingFits       = errors.New("bench: no prompt fits the context")
	ErrRefused           = errors.New("bench: the configuration would spill onto the processor")
	ErrNotFound          = errors.New("bench: no such run")
)

func fail(kind error, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// maxContext bounds num_ctx: above any trained context there is, below
// anything that would overflow the arithmetic.
const maxContext = 1 << 22

// ModelFacts is what the harness knew about the model it measured, stored
// with the run (model_json) so a measurement can calibrate other estimates
// later (evidence.go): the header fields the memory and speed arithmetic
// read, and where they came from.
type ModelFacts struct {
	// Source is "catalogue" (a file of the curated list) or "runtime" (what
	// the runtime reports about the model it has).
	Source           string             `json:"source"`
	CatalogFileID    int64              `json:"catalog_file_id,omitempty"`
	Quant            string             `json:"quant,omitempty"`
	WeightsBytes     uint64             `json:"weights_bytes"`
	ProjectorBytes   uint64             `json:"projector_bytes,omitempty"`
	Parameters       uint64             `json:"parameters"`
	ActiveParameters uint64             `json:"active_parameters,omitempty"`
	ContextLength    int                `json:"context_length"`
	Header           catalog.GGUFHeader `json:"header"`
	KV               map[string]any     `json:"kv,omitempty"`
}

// Model rebuilds the estimator's view of the model, its layout derived from
// the header afresh (as the catalogue does on every read).
func (f ModelFacts) Model() estimate.Model {
	m := estimate.Model{
		File: catalog.File{ID: f.CatalogFileID, Role: catalog.RoleModel, Quant: f.Quant, Bytes: f.WeightsBytes, Present: true,
			Header: f.Header, Layout: catalog.NewLayout(f.Header, f.KV)},
		Size: catalog.Size{Parameters: f.Parameters, ActiveParameters: f.ActiveParameters, ContextLength: f.ContextLength},
	}
	if f.ProjectorBytes > 0 {
		m.Projector = &catalog.File{Role: catalog.RoleProjector, Bytes: f.ProjectorBytes, Present: true}
	}
	return m
}

// prepared is everything Plan found out, handed on to the run.
type prepared struct {
	plan      Plan
	target    Target
	status    backend.Status
	installed backend.Installed
	row       store.InstalledModelRow // zero when the store has no row for it
	info      backend.ModelInfo
	facts     ModelFacts
	placement estimate.Placement
	prompts   []PromptSpec // the prompts planned to run, shortest first
}

// Plan works out what a run of req would do on t, without doing any of it:
// the runtime is asked what is installed and about the model (Detect,
// Models, Show — all read-only), nothing is loaded.
func (h *Harness) Plan(ctx context.Context, t Target, req Request) (Plan, error) {
	p, err := h.prepare(ctx, t, req)
	if err != nil {
		return Plan{}, err
	}
	return p.plan, nil
}

func (h *Harness) prepare(ctx context.Context, t Target, req Request) (*prepared, error) {
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		return nil, fail(ErrBadRequest, "say which installed model to test")
	}
	if req.NumCtx < 0 || req.NumCtx > maxContext || (req.NumCtx > 0 && req.NumCtx < 256) {
		return nil, fail(ErrBadRequest, "the context must be a number of tokens, 256 or more")
	}
	for _, id := range req.Prompts {
		if _, ok := h.suite.Prompt(id); !ok {
			return nil, fail(ErrBadRequest, "the suite has no prompt %q; its prompts are %s", id, h.promptIDs())
		}
	}
	if t.Backend == nil || t.Estimator == nil {
		return nil, fail(ErrBackendNotRunning, "no runtime to test with")
	}

	status, err := t.Backend.Detect(ctx)
	if err != nil {
		return nil, fail(ErrBackendNotRunning, "%s could not be checked: %v", t.Backend.Name(), err)
	}
	if status.State != backend.StateRunning {
		return nil, fail(ErrBackendNotRunning, "%s is not running; start it first", backendTitle(t.Backend.Name()))
	}
	models, err := t.Backend.Models(ctx)
	if err != nil {
		return nil, fail(ErrBackendNotRunning, "%s did not list its models: %v", backendTitle(t.Backend.Name()), err)
	}
	var inst *backend.Installed
	for i := range models {
		if sameModel(models[i].Name, req.Model) {
			inst = &models[i]
			break
		}
	}
	if inst == nil {
		return nil, fail(ErrModelNotInstalled, "%s is not installed in %s; download it first", req.Model, backendTitle(t.Backend.Name()))
	}
	info, err := t.Backend.Show(ctx, inst.Name)
	if err != nil {
		return nil, fail(ErrModelNotInstalled, "%s could not be read: %v", inst.Name, err)
	}
	if len(info.Capabilities) > 0 && !contains(info.Capabilities, "completion") {
		return nil, fail(ErrNotTextModel, "%s does not write text (it is a %s model), so there is no answering speed to measure",
			inst.Name, strings.Join(info.Capabilities, " and "))
	}

	p := &prepared{target: t, status: status, installed: *inst, info: info}
	if h.store != nil {
		if rows, err := h.store.InstalledModels(ctx, t.Backend.Name()); err == nil {
			for _, r := range rows {
				if sameModel(r.Name, inst.Name) {
					p.row = r
				}
			}
		}
	}
	p.facts = h.modelFacts(ctx, p.row, *inst, info)

	m := t.Machine
	p.placement = t.Estimator.Place(m)
	numCtx, source := req.NumCtx, "requested"
	if numCtx == 0 {
		numCtx, source = estimate.OllamaDefaultContext(p.placement), "ollama_default"
	}
	model := p.facts.Model()
	est := t.Estimator.FitPlaced(p.placement, m, model, estimate.Request{NumCtx: numCtx, KVCacheType: estimate.KVF16})

	plan := Plan{
		Model: inst.Name, ModelSource: p.facts.Source, NumCtx: numCtx, NumCtxSource: source, Estimate: est,
		Suite: SuiteInfo{Version: h.suite.Version, Digest: h.suite.Digest(), CompletionTokens: h.suite.CompletionTokens,
			Warmups: h.suite.Warmups, Repeats: h.suite.Repeats, Temperature: h.suite.Temperature, Seed: h.suite.Seed},
	}
	if p.facts.Source == "runtime" {
		plan.Notes = append(plan.Notes, "the curated list does not know this model, so its estimate is built from what "+
			backendTitle(t.Backend.Name())+" reports about it")
	}
	p.refuse(&plan, est)

	// Which prompts fit the context the runtime will run (it clamps to the
	// model's trained context), with the answer and a margin beside them.
	effective := numCtx
	if trained := p.facts.ContextLength; trained > 0 && trained < effective {
		effective = trained
	}
	for _, spec := range h.selected(req.Prompts) {
		pp := PlannedPrompt{ID: spec.ID, Tokens: spec.Tokens}
		need := spec.Tokens + h.suite.CompletionTokens + h.cfg.ContextMargin
		if need > effective {
			pp.Skip = fmt.Sprintf("needs a context of at least %s tokens to hold the prompt and its answer", commas(need))
		} else {
			pp.Runs = h.suite.Repeats
			p.prompts = append(p.prompts, spec)
		}
		plan.Prompts = append(plan.Prompts, pp)
	}
	if len(p.prompts) == 0 {
		plan.Refusal, plan.RefusalCode = fmt.Sprintf("even the shortest prompt needs a longer context than %s tokens", commas(effective)), "nothing_fits"
		e := fail(ErrNothingFits, "%s", plan.Refusal)
		e.Plan = &plan
		return nil, e
	}
	plan.Requests = h.suite.Warmups + len(p.prompts)*h.suite.Repeats
	plan.Duration, plan.DurationUnknown = h.duration(est, p.prompts, p.facts)
	plan.Measured = h.lastMeasured(ctx, t, status, *inst, numCtx)
	p.plan = plan
	return p, nil
}

// lastMeasured finds the latest finished run of the configuration a plan is
// for: this machine, runtime and runtime version, model file, context and
// suite version. The runtime path, cache type and flash attention are known
// only after a load, so they are not compared here; a run that found them
// different from the one before says so in its comparison.
func (h *Harness) lastMeasured(ctx context.Context, t Target, status backend.Status, inst backend.Installed, numCtx int) *PlanMeasured {
	if h.store == nil || t.Fingerprint == "" {
		return nil
	}
	rows, err := h.store.BenchRuns(ctx, store.BenchRunFilter{HardwareFingerprint: t.Fingerprint, BackendName: t.Backend.Name(),
		ModelName: inst.Name, Status: string(StatusDone), Limit: 50})
	if err != nil {
		return nil
	}
	for _, r := range rows {
		if r.GenTPSMedian == nil || r.NumCtx != numCtx || r.SuiteVersion != h.suite.Version || r.BackendVersion != status.Version ||
			(inst.Digest != "" && r.ModelDigest != "" && r.ModelDigest != inst.Digest) {
			continue
		}
		at, _ := time.Parse(time.RFC3339, r.FinishedAt)
		return &PlanMeasured{RunID: r.ID, At: at, GenTPS: figure.MeasuredRate(*r.GenTPSMedian, "tok/s")}
	}
	return nil
}

// refuse fills in the plan's refusal when the estimator says this
// configuration would not run wholly where the plan puts it (step 6, item
// 5): a beginner should not wait ten minutes for a number that tells them
// what the estimate already said. A model that runs on the processor by
// design (no usable graphics) is not spilling, and a budget the advisor
// cannot read refuses nothing — measuring is exactly what it needs.
func (p *prepared) refuse(plan *Plan, est estimate.Estimate) {
	onDevice := est.BudgetKind != estimate.BudgetSystem
	switch est.Category {
	case estimate.NeedsCPUOffload:
		plan.Refusal, plan.RefusalCode = fmt.Sprintf(
			"At a context of %s tokens this model does not fit in the graphics memory: part of it would run on the processor, "+
				"several times slower, which the estimate already says (%s).", commas(plan.NumCtx), est.Threshold), "would_spill"
	case estimate.ReducedContextOnly:
		where := "the graphics memory"
		if !onDevice {
			where = "this computer's memory"
		}
		plan.SuggestedCtx = est.SuggestedCtx
		plan.Refusal, plan.RefusalCode = fmt.Sprintf(
			"At a context of %s tokens this model does not fit in %s; at %s it does. Test it at %s instead (%s).",
			commas(plan.NumCtx), where, commas(est.SuggestedCtx), commas(est.SuggestedCtx), est.Threshold), "would_spill"
	case estimate.NotRecommended:
		plan.Refusal, plan.RefusalCode = "This model does not fit this computer at this context ("+est.Threshold+").", "not_recommended"
	}
}

// selected is the suite's prompts, or the ones asked for, shortest first.
func (h *Harness) selected(ids []string) []PromptSpec {
	if len(ids) == 0 {
		return append([]PromptSpec(nil), h.suite.Prompts...)
	}
	var out []PromptSpec
	for _, spec := range h.suite.Prompts { // the suite's order is shortest first
		if contains(ids, spec.ID) {
			out = append(out, spec)
		}
	}
	return out
}

func (h *Harness) promptIDs() string {
	var ids []string
	for _, p := range h.suite.Prompts {
		ids = append(ids, `"`+p.ID+`"`)
	}
	return strings.Join(ids, ", ")
}

// modelFacts describes the installed model: the catalogue's file when the
// model maps onto one (step 4's matching), else what the runtime reports.
func (h *Harness) modelFacts(ctx context.Context, row store.InstalledModelRow, inst backend.Installed, info backend.ModelInfo) ModelFacts {
	if row.CatalogFileID != 0 && h.store != nil {
		if models, err := h.store.CatalogModels(ctx, true); err == nil {
			for _, cm := range models {
				for _, f := range cm.Model.Files {
					if f.ID != row.CatalogFileID {
						continue
					}
					facts := ModelFacts{Source: "catalogue", CatalogFileID: f.ID, Quant: f.Quant, WeightsBytes: f.Bytes,
						Parameters: cm.Model.Size.Parameters, ActiveParameters: cm.Model.Size.ActiveParameters,
						ContextLength: cm.Model.Size.ContextLength, Header: f.Header}
					if f.Header.ContextLength > 0 {
						facts.ContextLength = f.Header.ContextLength
					}
					if raw, err := h.store.CatalogFileHeaderJSON(ctx, f.ID); err == nil {
						var hj struct {
							KV map[string]any `json:"kv"`
						}
						if json.Unmarshal(raw, &hj) == nil {
							facts.KV = hj.KV
						}
					}
					// The vision encoder is loaded beside the weights when the
					// runtime's copy of the model carries one.
					if contains(info.Capabilities, "vision") {
						for _, pf := range cm.Model.Files {
							if pf.Present && pf.Role == catalog.RoleProjector {
								facts.ProjectorBytes = pf.Bytes
								break
							}
						}
					}
					return facts
				}
			}
		}
	}
	header, kv := catalog.HeaderFromRuntime(info.Details)
	facts := ModelFacts{Source: "runtime", Quant: inst.Quantization, WeightsBytes: inst.SizeBytes, Header: header, KV: kv,
		ContextLength: header.ContextLength}
	if n, ok := kv["general.parameter_count"].(uint64); ok && n > 0 {
		facts.Parameters = n
	} else if n, ok := catalog.ParseParameterSize(inst.ParameterSize); ok {
		facts.Parameters = n
	}
	return facts
}

// duration estimates how long the run takes, from the estimate's speed
// ranges: every request reads its prompt and writes its answer, the first
// also loads the model, and the harness pauses before the load. Absent when
// there is no speed estimate.
func (h *Harness) duration(est estimate.Estimate, prompts []PromptSpec, facts ModelFacts) (*figure.Rate, string) {
	sp := est.Speed
	if !sp.Known || sp.Generation == nil || sp.Prompt == nil || sp.Generation.Low <= 0 || sp.Prompt.Low <= 0 {
		return nil, "how long it takes depends on the speed the test measures: there is no estimate of it for this computer yet"
	}
	c := float64(h.suite.CompletionTokens)
	fast, slow := 0.0, 0.0
	request := func(tokens int) {
		fast += float64(tokens)/sp.Prompt.High + c/sp.Generation.High
		slow += float64(tokens)/sp.Prompt.Low + c/sp.Generation.Low
	}
	for i := 0; i < h.suite.Warmups; i++ {
		request(prompts[0].Tokens)
	}
	for _, p := range prompts {
		for i := 0; i < h.suite.Repeats; i++ {
			request(p.Tokens)
		}
	}
	gb := float64(facts.WeightsBytes+facts.ProjectorBytes) / 1e9
	fast += gb/h.cfg.LoadGBsHigh + h.cfg.Settle.Seconds()
	slow += gb/h.cfg.LoadGBsLow + h.cfg.Settle.Seconds()
	r := figure.EstimatedRange(math.Round(fast), math.Round(slow), "s")
	return &r, ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func commas(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// backendTitle is a runtime's name as people write it.
func backendTitle(name string) string {
	switch name {
	case "ollama":
		return "Ollama"
	case "llamacpp":
		return "llama.cpp"
	case "lmstudio":
		return "LM Studio"
	}
	return name
}

// pathOf is a runtime path, or "unknown".
func pathOf(p hardware.RuntimePath) hardware.RuntimePath {
	if p == "" {
		return hardware.PathUnknown
	}
	return p
}

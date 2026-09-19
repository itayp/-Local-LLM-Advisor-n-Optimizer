package estimate

import (
	"encoding/json"
	"strings"
	"testing"

	"advisor/internal/catalog"
	"advisor/internal/figure"
	"advisor/internal/hardware"
)

func relWidth(r *figure.Rate) float64 { return r.High / r.Low }

// The speed estimate is a range, labelled estimated, for generation and —
// separately — for reading the prompt.
func TestSpeedIsARangeAndSaysSo(t *testing.T) {
	e := mustEstimator(t)
	est := e.Fit(goldenMachine(t, "LinuxNVIDIA"), dense(5*gib), Request{NumCtx: 8192}) // RTX 3090
	s := est.Speed
	if !s.Known || s.Generation == nil || s.Prompt == nil {
		t.Fatalf("speed: %+v", s)
	}
	for name, r := range map[string]*figure.Rate{"generation": s.Generation, "prompt": s.Prompt} {
		if !r.IsRange() || r.Low <= 0 || r.Low >= r.High || r.Value < r.Low || r.Value > r.High {
			t.Errorf("%s is not a range: %+v", name, r)
		}
		if r.Source != figure.Estimated || r.Unit != "tok/s" {
			t.Errorf("%s: source %q unit %q", name, r.Source, r.Unit)
		}
	}
	if est.Basis.SpeedSource != SpeedEstimated {
		t.Errorf("speed source %q", est.Basis.SpeedSource)
	}
	// The public measurement this range has to contain: an RTX 3090 generates
	// 158 tok/s on a 3.82 GB model with an empty context (llama.cpp #15013);
	// scaled to 5.37 GB of weights that is about 112.
	if s.Generation.Low > 112 || s.Generation.High < 112 {
		t.Errorf("generation %v–%v tok/s does not contain the scaled public measurement (about 112)", s.Generation.Low, s.Generation.High)
	}
	if s.Prompt.Low <= s.Generation.High {
		t.Errorf("reading a prompt is much faster than answering on a graphics card: prompt %+v, generation %+v", s.Prompt, s.Generation)
	}
	if !strings.Contains(s.Basis, "936") || !strings.Contains(s.Basis, "cuda") {
		t.Errorf("the basis should name the bandwidth and the path: %q", s.Basis)
	}
}

// The width of the range follows the runtime path: tight for cuda and metal,
// wider for rocm, wider still for vulkan, widest for the processor.
func TestRangeWidthFollowsTheRuntimePath(t *testing.T) {
	e := mustEstimator(t)
	m := dense(4 * gib)
	gen := func(golden string, actual hardware.RuntimePath) float64 {
		mc := goldenMachine(t, golden)
		mc.ActualPath = actual
		est := e.Fit(mc, m, Request{NumCtx: 4096})
		if !est.Speed.Known {
			t.Fatalf("%s: no speed estimate: %s", golden, est.Speed.Unknown)
		}
		return relWidth(est.Speed.Generation)
	}
	cuda := gen("LinuxNVIDIA", "")
	metal := gen("AppleSiliconM1Pro", "")
	rocm := gen("LinuxAMDROCmFromKFD", "")
	vulkan := gen("WindowsAMDVulkan", "")
	cpu := gen("WindowsAMDVulkan", hardware.PathCPU)
	t.Logf("relative widths: cuda %.2f, metal %.2f, rocm %.2f, vulkan %.2f, cpu %.2f", cuda, metal, rocm, vulkan, cpu)
	if !(cuda < rocm && metal < rocm && rocm < vulkan && vulkan < cpu) {
		t.Errorf("widths out of order: cuda %.2f, metal %.2f, rocm %.2f, vulkan %.2f, cpu %.2f", cuda, metal, rocm, vulkan, cpu)
	}
}

// Unknown GPU → no speed estimate, and say so; never invent a number. The
// fit is still answered.
func TestUnknownGPUHasNoSpeedEstimate(t *testing.T) {
	e := mustEstimator(t)
	m := goldenMachine(t, "LinuxNVIDIA")
	m.Profile.GPUs[0].Name = "NVIDIA GeForce RTX 9999 Hyper"
	est := e.Fit(m, dense(5*gib), Request{NumCtx: 8192})
	if est.Category != FitsWithHeadroom {
		t.Errorf("the fit does not depend on knowing the card's speed: %q", est.Category)
	}
	s := est.Speed
	if s.Known || s.Generation != nil || s.Prompt != nil {
		t.Fatalf("an unknown card must have no speed numbers: %+v", s)
	}
	if !strings.Contains(s.Unknown, "RTX 9999") || !strings.Contains(s.Unknown, "not in the advisor's list") {
		t.Errorf("the sentence should name the card and say why: %q", s.Unknown)
	}
	if est.Basis.SpeedSource != SpeedUnknown {
		t.Errorf("speed source %q", est.Basis.SpeedSource)
	}
	b, err := json.Marshal(est)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"generation"`) || strings.Contains(string(b), `"prompt"`) {
		t.Errorf("no rate may be serialised for an unknown speed: %s", b)
	}
}

func TestProcessorSpeedUsesMemoryRangeAndAVX2(t *testing.T) {
	e := mustEstimator(t)
	m := dense(4 * gib)

	with := goldenMachine(t, "LinuxNVIDIAWithNoDriver") // Core i5-12400F, AVX2
	a := e.Fit(with, m, Request{NumCtx: 4096}).Speed
	if !a.Known {
		t.Fatalf("no estimate: %s", a.Unknown)
	}
	// 25.6–89.6 GB/s × 0.35–0.80 over 4.3–4.8 GB: a few tokens a second.
	if a.Generation.Low < 1 || a.Generation.High > 20 {
		t.Errorf("generation on a desktop processor: %+v", a.Generation)
	}

	without := with
	without.Profile.CPU.HasAVX2 = false
	b := e.Fit(without, m, Request{NumCtx: 4096}).Speed
	if b.Generation.High >= a.Generation.High || !strings.Contains(b.Basis, "AVX2") {
		t.Errorf("without AVX2 the estimate must drop and say why: %+v vs %+v (%s)", b.Generation, a.Generation, b.Basis)
	}

	unknownCPU := with
	unknownCPU.Profile.CPU.Model = "Some Future Processor 9000"
	if s := e.Fit(unknownCPU, m, Request{NumCtx: 4096}).Speed; s.Known || !strings.Contains(s.Unknown, "Some Future Processor 9000") {
		t.Errorf("an unknown processor has no speed estimate: %+v", s)
	}
}

func TestOffloadIsSlowerThanFittingAndMoEIsFasterThanItsSize(t *testing.T) {
	e := mustEstimator(t)
	card := goldenMachine(t, "WindowsNVIDIADesktop") // RTX 5070 Ti 16 GB, Ryzen 7 7800X3D
	fits := e.Fit(card, dense(10*gib), Request{NumCtx: 4096})
	split := e.Fit(card, dense(20*gib), Request{NumCtx: 4096})
	if fits.Category != FitsWithHeadroom || split.Category != NeedsCPUOffload {
		t.Fatalf("categories %q and %q", fits.Category, split.Category)
	}
	if !split.Speed.Known {
		t.Fatalf("split: %s", split.Speed.Unknown)
	}
	// Twice the bytes alone would halve the speed; spilling a third of it
	// onto the processor has to cost much more than that.
	if split.Speed.Generation.High > fits.Speed.Generation.High/4 {
		t.Errorf("a split model should be far slower: %+v against %+v", split.Speed.Generation, fits.Speed.Generation)
	}

	moe := dense(12 * gib)
	moe.File.Header.ExpertCount, moe.File.Header.ExpertUsedCount = 32, 4
	moe.Size = catalog.Size{Parameters: 21_000_000_000, ActiveParameters: 3_600_000_000}
	d := dense(12 * gib)
	d.Size.Parameters = 21_000_000_000
	sm, sd := e.Fit(card, moe, Request{NumCtx: 4096}).Speed, e.Fit(card, d, Request{NumCtx: 4096}).Speed
	if sm.Generation.Low <= sd.Generation.High {
		t.Errorf("a mixture-of-experts model reads a fraction of its weights per token: %+v against dense %+v", sm.Generation, sd.Generation)
	}
	// llama.cpp measured gpt-oss-20b at 189 tok/s on this card (#15396).
	if sm.Generation.Low > 189 || sm.Generation.High < 189 {
		t.Errorf("gpt-oss-20b-shaped model on an RTX 5070 Ti: %v–%v tok/s does not contain the public measurement (189)", sm.Generation.Low, sm.Generation.High)
	}
}

// The range narrows to a point the moment a measurement exists.
func TestAMeasurementReplacesTheEstimate(t *testing.T) {
	e := mustEstimator(t)
	est := e.Fit(goldenMachine(t, "LinuxNVIDIA"), dense(5*gib), Request{NumCtx: 8192})
	got := est.WithMeasurement(Measurement{GenerationTPS: 104.26, PromptTPS: 3121.4, PeakBytes: 6_400_000_000})
	g := got.Speed.Generation
	if g.IsRange() || g.Source != figure.Measured || g.Value != 104.3 {
		t.Errorf("generation after a measurement: %+v", g)
	}
	if got.Speed.Prompt.Source != figure.Measured || got.Basis.SpeedSource != SpeedMeasured {
		t.Errorf("prompt %+v, speed source %q", got.Speed.Prompt, got.Basis.SpeedSource)
	}
	if got.Memory.Total.Source != figure.Measured || got.Memory.Total.Value != 6_400_000_000 || got.Basis.MemoryModel != MemoryMeasured {
		t.Errorf("total after a measurement: %+v (%s)", got.Memory.Total, got.Basis.MemoryModel)
	}
	if got.Memory.Weights.Source != figure.Estimated {
		t.Error("the terms stay estimates; only what was measured flips")
	}
	if est.Speed.Generation.Source != figure.Estimated || !est.Speed.Generation.IsRange() {
		t.Error("WithMeasurement must not change the estimate it was called on")
	}

	// A machine whose card is unknown gets its speed from the measurement.
	m := goldenMachine(t, "LinuxNVIDIA")
	m.Profile.GPUs[0].Name = "NVIDIA GeForce RTX 9999 Hyper"
	unknown := e.Fit(m, dense(5*gib), Request{NumCtx: 8192}).WithMeasurement(Measurement{GenerationTPS: 88})
	if !unknown.Speed.Known || unknown.Speed.Unknown != "" || unknown.Speed.Generation.Value != 88 {
		t.Errorf("measured speed on an unknown card: %+v", unknown.Speed)
	}
}

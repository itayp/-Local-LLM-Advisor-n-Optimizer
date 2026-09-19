package estimate

import (
	"math"
	"strings"
	"testing"

	"advisor/internal/figure"
	"advisor/internal/hardware"
)

// A benchmark on this machine narrows the range for other models on the
// same path to a band around what the machine achieved (build-plan step 6,
// item 6), and never makes it wider than it was.
func TestCalibrationNarrowsTheRangeForSimilarModels(t *testing.T) {
	e := mustEstimator(t)
	card := goldenMachine(t, "LinuxNVIDIA") // RTX 3090, 936 GB/s
	measured := dense(5 * gib)
	before := e.Fit(card, measured, Request{NumCtx: 8192}).Speed.Generation

	// Ollama measured 100 tok/s at ~630 tokens of context on this card.
	cal := e.Calibrate([]Observation{{Label: "llama3.1:8b", Model: measured, Path: hardware.PathCUDA, Resident: true,
		ContextTokens: 630, KVCacheType: KVF16, GenerationTPS: 100, PromptTPS: 3000}})
	if cal == nil || len(cal.Points[hardware.PathCUDA]) != 1 {
		t.Fatalf("calibration: %+v", cal)
	}
	p := cal.Points[hardware.PathCUDA][0]
	// 5 GiB of weights plus 630 tokens × 131,072 bytes of cache, 100 times a second.
	if want := 100 * (5*gib + 630*131072.0) / 1e9; math.Abs(p.GenerationGBs-want) > 0.01 {
		t.Fatalf("achieved bandwidth %.2f GB/s, want %.2f", p.GenerationGBs, want)
	}
	if p.PromptRate != 3000*8e9 {
		t.Fatalf("prompt rate %v", p.PromptRate)
	}

	e.Calibration = cal
	same := e.Fit(card, measured, Request{NumCtx: 8192})
	g := same.Speed.Generation
	if g.Source != figure.Estimated || !g.IsRange() || !same.Speed.Calibrated || same.Speed.CalibratedFrom != "llama3.1:8b" || !same.Basis.SpeedCalibrated {
		t.Fatalf("a calibrated estimate is still an estimate, and says what it was calibrated on: %+v", same.Speed)
	}
	// Same size: within the 5% margin at the empty end (100 tok/s at 630
	// tokens is about 101.5 with an empty cache; +5% is 106.6, rounded).
	if g.High < 100 || g.High > 108 || relWidth(g) >= relWidth(before) {
		t.Errorf("same size: %+v (uncalibrated %+v)", g, before)
	}
	if same.Speed.Prompt == nil || same.Speed.Prompt.Low > 3000 || same.Speed.Prompt.High < 3000 {
		t.Errorf("prompt range should hold the measured rate: %+v", same.Speed.Prompt)
	}
	if !strings.Contains(same.Speed.Basis, "measured on this computer") {
		t.Errorf("basis: %q", same.Speed.Basis)
	}

	// A model an eighth of the size: the margin grows (5% + 3 × 6%) but is
	// capped at the uncalibrated range's own width.
	small := dense(640 << 20)
	un := mustEstimator(t).Fit(card, small, Request{NumCtx: 8192}).Speed.Generation
	cs := e.Fit(card, small, Request{NumCtx: 8192}).Speed.Generation
	if relWidth(cs) > relWidth(un)*1.001 {
		t.Errorf("calibration must never widen a range: %+v against %+v", cs, un)
	}

	// Another path is not calibrated by a cuda run.
	e2 := mustEstimator(t)
	e2.Calibration = cal
	m := goldenMachine(t, "AppleSiliconM1Pro")
	if s := e2.Fit(m, measured, Request{NumCtx: 4096}).Speed; s.Calibrated {
		t.Errorf("a cuda measurement calibrated metal: %+v", s)
	}
}

// A card that is not in gpus.yaml has no estimate — until a benchmark on
// this computer measures what it does.
func TestCalibrationGivesAnUnknownCardAnEstimate(t *testing.T) {
	e := mustEstimator(t)
	m := goldenMachine(t, "LinuxNVIDIA")
	m.Profile.GPUs[0].Name = "NVIDIA GeForce RTX 9999 Hyper"
	if s := e.Fit(m, dense(5*gib), Request{NumCtx: 8192}).Speed; s.Known {
		t.Fatalf("unknown card, no benchmark: %+v", s)
	}
	e.Calibration = e.Calibrate([]Observation{{Label: "qwen3:4b", Model: dense(2500 << 20), Path: hardware.PathCUDA,
		Resident: true, ContextTokens: 600, GenerationTPS: 240}})
	s := e.Fit(m, dense(5*gib), Request{NumCtx: 8192}).Speed
	if !s.Known || s.Generation == nil || !s.Calibrated {
		t.Fatalf("calibrated unknown card: %+v", s)
	}
	if s.Prompt != nil {
		t.Errorf("no prompt was measured and there is no population figure: no prompt estimate, got %+v", s.Prompt)
	}
	// Twice the size: about half the speed, within the 30% cap.
	if s.Generation.High > 240/2*1.3 || s.Generation.Low < 240/2*0.5 {
		t.Errorf("generation %+v", s.Generation)
	}
}

// Split runs and mixture-of-experts runs do not calibrate: they say
// nothing clean about the part's own speed.
func TestCalibrationIgnoresSplitAndMoERuns(t *testing.T) {
	e := mustEstimator(t)
	moe := dense(12 * gib)
	moe.File.Header.ExpertCount = 32
	obs := []Observation{
		{Label: "split", Model: dense(20 * gib), Path: hardware.PathCUDA, Resident: false, GenerationTPS: 12},
		{Label: "moe", Model: moe, Path: hardware.PathCUDA, Resident: true, GenerationTPS: 180},
		{Label: "none", Model: dense(5 * gib), Path: hardware.PathCUDA, Resident: true, GenerationTPS: 0},
	}
	if cal := e.Calibrate(obs); cal != nil {
		t.Fatalf("nothing here calibrates: %+v", cal)
	}
}

// On the processor the uncalibrated range is the widest of all (memory
// speed unknown, efficiency unknown); one run collapses it.
func TestCalibrationOnTheProcessor(t *testing.T) {
	e := mustEstimator(t)
	m := goldenMachine(t, "LinuxNVIDIAWithNoDriver") // runs on the Core i5-12400F
	model := dense(4 * gib)
	before := e.Fit(m, model, Request{NumCtx: 4096})
	if before.Request.RuntimePath != hardware.PathCPU {
		t.Fatalf("path %q", before.Request.RuntimePath)
	}
	e.Calibration = e.Calibrate([]Observation{{Label: "llama3.1:8b", Model: model, Path: hardware.PathCPU, Resident: true,
		ContextTokens: 600, GenerationTPS: 9.1, PromptTPS: 60}})
	after := e.Fit(m, model, Request{NumCtx: 4096})
	if relWidth(after.Speed.Generation) >= relWidth(before.Speed.Generation) || after.Speed.Generation.High < 9.1 {
		t.Errorf("processor: %+v → %+v", before.Speed.Generation, after.Speed.Generation)
	}
}

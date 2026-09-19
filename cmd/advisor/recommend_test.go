package main

import (
	"bytes"
	"strings"
	"testing"

	"advisor/internal/bench"
	"advisor/internal/catalog"
	"advisor/internal/estimate"
	"advisor/internal/figure"
	"advisor/internal/recommend"
)

func TestPrintRecommendationsKeepsEstimatesLookingLikeEstimates(t *testing.T) {
	speed := figure.EstimatedRange(79, 113, "tok/s")
	res := recommend.Result{
		Purposes: []catalog.Purpose{catalog.PurposeCoding}, RuntimePath: "cuda", PathSource: estimate.PathExpected,
		Current: &recommend.Current{Name: "llama3.1:8b", Verdict: "llama3.1:8b runs well on this computer."},
		Recommendations: []recommend.Recommendation{{
			DisplayName: "Qwen3.5 9B", PullName: "qwen3.5:9b", File: catalog.File{Quant: "Q4_K_M"}, NumCtx: 16384,
			Estimate: estimate.Estimate{Category: estimate.FitsWithHeadroom, Threshold: "7.4 GB needed of 15.9 GB of graphics memory (47%)",
				Memory: estimate.Memory{Total: figure.EstimatedBytes(7946088448)}},
			DownloadBytes: 7_100_000_000, Speed: &speed, Confidence: recommend.ConfidenceMedium, ConfidenceWhy: "Expected, not seen.",
			Reasons: []recommend.Reason{{Kind: "fit", Text: "Fits your 16 GB graphics card with room to spare."}},
		}},
	}
	var out bytes.Buffer
	printRecommendations(&out, res)
	got := out.String()
	for _, want := range []string{
		"For: coding — built for the cuda path (expected)", "You have llama3.1:8b.",
		"1. Qwen3.5 9B  —  qwen3.5:9b (Q4_K_M), context 16384, medium confidence",
		"≈ 79–113 tok/s (estimated)", "needs ≈ 7.4 GB", "download 7.1 GB", "- Fits your 16 GB graphics card with room to spare.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	measured := figure.MeasuredRate(51.2, "tok/s")
	res.Recommendations[0].Speed = &measured
	out.Reset()
	printRecommendations(&out, res)
	if !strings.Contains(out.String(), "51.2 tok/s (measured)") || strings.Contains(out.String(), "≈ 51") {
		t.Errorf("a measurement must not be dressed as an estimate:\n%s", out.String())
	}
}

func TestRecommendCommandIsRecognised(t *testing.T) {
	if !isRecommendCommand([]string{"advisor", "recommend", "-purposes", "chat"}) || isRecommendCommand([]string{"advisor", "-port", "1"}) {
		t.Error("isRecommendCommand")
	}
	var out, errOut bytes.Buffer
	if code := runRecommend([]string{"-port", "1"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "no daemon answered") {
		t.Errorf("no daemon: exit %d, %q", code, errOut.String())
	}
}

// The benchmark printer keeps measurements bare and estimates marked, and
// says what was not sampled and whether the model was unloaded.
func TestPrintRunReadsLikeTheGate(t *testing.T) {
	gen := figure.MeasuredRate(41.3, "tok/s")
	pr := figure.MeasuredRate(812.4, "tok/s")
	ttft := figure.MeasuredRate(612, "ms")
	est := figure.EstimatedRange(35, 48, "tok/s")
	yes := true
	run := bench.Run{ID: 7, Status: bench.StatusDone,
		Config: bench.RunConfig{Model: "llama3.2:3b", Quantization: "Q4_K_M", NumCtx: 4096, EffectiveCtx: 4096, Backend: "ollama",
			BackendVersion: "0.34.2", RuntimePath: "metal", KVCacheType: "f16", FlashAttentionKnown: true, FlashAttention: true, SuiteVersion: "1"},
		Results: []bench.PromptResult{{Prompt: "500", PromptTokens: 481, GenTokens: 256, PromptTPS: &pr, GenTPS: &gen, TTFT: &ttft, SpreadPct: 1.2}},
		GenTPS:  &gen, Resident: bench.ResidentGPU, Replaced: true, Unloaded: &yes,
		Estimate:    &estimate.Estimate{Speed: estimate.Speed{Known: true, Generation: &est}},
		Skipped:     []bench.Skipped{{Prompt: "8000", Why: "needs a context of at least 7,728 tokens"}},
		SamplerNote: "temperature and power are not read on a Mac",
	}
	var out bytes.Buffer
	printRun(&out, run)
	for _, want := range []string{"Run 7: llama3.2:3b (Q4_K_M) at a context of 4096 — done", "path metal · f16 cache · flash attention on",
		"500         481        812.4          41.3      256       612 ms", "estimated before: ≈ 35–48 tok/s (estimated) → measured 41.3 tok/s (measured); estimate replaced: yes",
		"skipped 8000", "not sampled: temperature", "unloaded afterwards: yes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
	if !isBenchCommand([]string{"advisor", "bench"}) || isBenchCommand([]string{"advisor"}) {
		t.Error("isBenchCommand")
	}
	var o, e bytes.Buffer
	if code := runBench([]string{"-port", "1", "-model", "m"}, &o, &e); code != 1 || !strings.Contains(e.String(), "no daemon answered") {
		t.Errorf("no daemon: %d %q", code, e.String())
	}
}

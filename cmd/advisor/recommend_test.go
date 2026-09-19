package main

import (
	"bytes"
	"strings"
	"testing"

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

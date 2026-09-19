package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/rtx4090-public.json is llama.cpp's published RTX 4090 row
// (discussion #15013: Llama 2 7B Q4_0, pp512 11992.70, tg128 186.21) put in
// the shape `llama-bench -o json` writes — chosen, not captured, so the
// arithmetic can be checked against a number anyone can look up.
func TestCalibrateDerivesTheFactors(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run(&out, "public-rtx4090", "", 0, 1, dir, []string{filepath.Join("testdata", "rtx4090-public.json")}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "public-rtx4090-*.json"))
	if len(files) != 1 {
		t.Fatalf("results files: %v", files)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var results []Result
	if err := json.Unmarshal(b, &results); err != nil || len(results) != 1 {
		t.Fatalf("results: %v %s", err, b)
	}
	r := results[0]
	// 186.21 tok/s × 3.825 GB ÷ 1008 GB/s = 0.707
	if r.Path != "cuda" || math.Abs(r.Efficiency[0]-0.707) > 0.002 || r.Efficiency[0] != r.Efficiency[1] {
		t.Errorf("path %q, efficiency %v, want cuda and 0.707", r.Path, r.Efficiency)
	}
	// The reference model is this one, so the ratio is simply 11992.70 ÷ 186.21 = 64.4.
	if math.Abs(r.PromptRatio-64.4) > 0.3 {
		t.Errorf("prompt ratio %.2f, want 64.4", r.PromptRatio)
	}
	if !strings.HasPrefix(r.Verdict, "inside the configured range") {
		t.Errorf("verdict %q", r.Verdict)
	}
	if !strings.Contains(out.String(), "efficiency 0.71") {
		t.Errorf("printed report:\n%s", out.String())
	}
}

func TestCalibrateRefusesWhatItCannotScore(t *testing.T) {
	var out bytes.Buffer
	in := []string{filepath.Join("testdata", "rtx4090-public.json")}
	if err := run(&out, "", "", 0, 1, "", in); err == nil {
		t.Error("a label is required")
	}
	if err := run(&out, "x", "", 0, 1.5, "", in); err == nil {
		t.Error("an active share above 1 is not a share")
	}
	if err := run(&out, "x", "", 0, 1, "", nil); err == nil {
		t.Error("no input files")
	}
}

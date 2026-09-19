package bench

import (
	"strings"
	"testing"
)

func TestMedianAndSpread(t *testing.T) {
	if m := median([]float64{3, 1, 2}); m != 2 {
		t.Errorf("median %v", m)
	}
	if m := median([]float64{4, 1, 3, 2}); m != 2.5 {
		t.Errorf("median of four %v", m)
	}
	if s := spread([]float64{100, 104, 98}); s != 0.06 {
		t.Errorf("spread %v", s)
	}
	if s := spread([]float64{5}); s != 0 {
		t.Errorf("one value has no spread: %v", s)
	}
}

// A result says what makes it less certain: runs that disagreed, an answer
// that stopped early, a prompt partly reused from the runtime's cache — and
// it never reports a rate the runtime did not time.
func TestSummariseNotesWhatMakesAResultLessCertain(t *testing.T) {
	cfg := DefaultConfig()
	steady := []Timing{
		{PromptTokens: 480, CachedTokens: 1, CachedKnown: true, PromptMs: 159.667, GenTokens: 256, GenMs: 2133.3, TTFTMs: 175},
		{PromptTokens: 480, CachedTokens: 1, CachedKnown: true, PromptMs: 159.667, GenTokens: 256, GenMs: 2150, TTFTMs: 180},
		{PromptTokens: 480, CachedTokens: 1, CachedKnown: true, PromptMs: 159.667, GenTokens: 256, GenMs: 2120, TTFTMs: 170},
	}
	r := summarise("500", steady, 256, cfg)
	if len(r.Notes) != 0 || r.GenTPS.Value != 120 || r.PromptTPS.Value < 2999.9 || r.PromptTPS.Value > 3000.1 || r.TTFT.Value != 175 || r.PromptTokens != 480 {
		t.Fatalf("steady: %+v", r)
	}

	noisy := append([]Timing(nil), steady...)
	noisy[1].GenMs = 3000
	noisy[2].GenTokens, noisy[1].GenTokens, noisy[0].GenTokens = 100, 100, 100
	noisy[0].CachedTokens = 300
	noisy[0].PromptMs = 0
	r = summarise("500", noisy, 256, cfg)
	for _, want := range []string{"disagreed", "stopped after 100", "reused up to 300"} {
		if !hasNote(r.Notes, want) {
			t.Errorf("missing note %q in %v", want, r.Notes)
		}
	}

	// Answers shorter than MinAnswerTokens time the reading, not the
	// answering: left out of the median when others are long enough,
	// and the answering speed absent, with why, when none is.
	mixed := append([]Timing(nil), steady...)
	mixed[1].GenTokens, mixed[1].GenMs = 12, 50
	r = summarise("500", mixed, 256, cfg)
	if r.GenTPS == nil || r.GenTPS.Value < 119 || r.GenTPS.Value > 122 || !hasNote(r.Notes, "1 of the 3 answers stopped before 64") {
		t.Errorf("mixed: %+v %v", r.GenTPS, r.Notes)
	}
	allShort := append([]Timing(nil), steady...)
	for i := range allShort {
		allShort[i].GenTokens, allShort[i].GenMs = 40, 300
	}
	r = summarise("2000", allShort, 256, cfg)
	if r.GenTPS != nil || !strings.Contains(r.GenUnknown, "after 40 tokens") || r.PromptTPS == nil || r.TTFT == nil || hasNote(r.Notes, "stopped after") {
		t.Errorf("all short: %+v", r)
	}

	none := []Timing{{PromptTokens: 480, GenTokens: 256, GenMs: 2000}}
	if r := summarise("500", none, 256, cfg); r.PromptTPS != nil || !strings.Contains(strings.Join(r.Notes, " "), "no time for reading") {
		t.Errorf("a prompt the runtime gave no time for has no rate: %+v", r)
	}
}

package recommend

import (
	"encoding/json"
	"math"
	"testing"

	"advisor/internal/catalog"
)

// publicSignals rates every size of the fleet catalogue on one chat metric,
// in the order given (first = best), as a crowd source would.
func publicSignals(cat []Entry, order []string) map[catalog.Purpose][]catalog.PublicMetric {
	vals := map[int64]float64{}
	for i, tag := range order {
		for _, e := range cat {
			if e.Model.Size.OllamaTag == tag {
				vals[e.Model.ID] = float64(2000 - 10*i)
			}
		}
	}
	return map[catalog.Purpose][]catalog.PublicMetric{
		catalog.PurposeChat: {{Source: "arena", Metric: "arena:text/overall", HigherIsBetter: true, Values: vals}},
	}
}

func everyTag(cat []Entry) []string {
	var out []string
	for _, e := range cat {
		out = append(out, e.Model.Size.OllamaTag)
	}
	return out
}

func reversed(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

// ExternalWeight = 0 must reproduce step 5's outcomes exactly, public data
// or not (research/EXTERNAL_SOURCES.md: "recommend_test.go unchanged").
func TestAZeroExternalWeightChangesNothing(t *testing.T) {
	for _, golden := range []string{"LinuxNVIDIA", "AppleSiliconM1Pro", "WindowsNVIDIADesktop"} {
		m := goldenMachine(t, golden)
		plain := mustEngine(t)
		with := mustEngine(t)
		with.Public = publicSignals(with.Catalogue, reversed(everyTag(with.Catalogue)))
		with.Config.ExternalWeight = 0
		for _, p := range everyPurpose {
			a, _ := json.Marshal(plain.Recommend(m, []catalog.Purpose{p}, nil, Preferences{}))
			b, _ := json.Marshal(with.Recommend(m, []catalog.Purpose{p}, nil, Preferences{}))
			if string(a) != string(b) {
				t.Errorf("%s, %s: a zero weight changed the result", golden, p)
			}
		}
	}
}

// Public data reaches the purpose-fit term and nothing else: removing it
// changes no fit category, speed range, memory figure or confidence — and
// the purpose term moves by at most ±ExternalWeight.
func TestPublicSignalsTouchOnlyThePurposeTerm(t *testing.T) {
	m := goldenMachine(t, "LinuxNVIDIA")
	plain := mustEngine(t)
	plain.Config.MaxRecommendations = 100
	plain.Config.OnePerFamily = false
	with := mustEngine(t)
	with.Config = plain.Config
	with.Public = publicSignals(with.Catalogue, reversed(everyTag(with.Catalogue)))

	a := plain.Recommend(m, []catalog.Purpose{pChat}, nil, Preferences{})
	b := with.Recommend(m, []catalog.Purpose{pChat}, nil, Preferences{})
	byModel := map[int64]Recommendation{}
	for _, r := range a.Recommendations {
		byModel[r.Model.ID] = r
	}
	if len(a.Recommendations) != len(b.Recommendations) || len(a.Recommendations) < 3 {
		t.Fatalf("candidates %d → %d", len(a.Recommendations), len(b.Recommendations))
	}
	moved := false
	for _, r := range b.Recommendations {
		was := byModel[r.Model.ID]
		ea, _ := json.Marshal(was.Estimate)
		eb, _ := json.Marshal(r.Estimate)
		if string(ea) != string(eb) || was.Confidence != r.Confidence || was.ConfidenceWhy != r.ConfidenceWhy ||
			was.Factors.Fit != r.Factors.Fit || was.Factors.Speed != r.Factors.Speed || was.Factors.Size != r.Factors.Size {
			t.Errorf("%s: public data moved something other than the purpose term", r.DisplayName)
		}
		ratio := r.Factors.Purpose / was.Factors.Purpose
		if ratio < 1-with.Config.ExternalWeight-0.002 || ratio > 1+with.Config.ExternalWeight+0.002 {
			t.Errorf("%s: purpose term moved ×%.3f, beyond ±%.2f", r.DisplayName, ratio, with.Config.ExternalWeight)
		}
		if math.Abs(ratio-r.Factors.Public) > 0.002 {
			t.Errorf("%s: Factors.Public %.3f does not say what moved (×%.3f)", r.DisplayName, r.Factors.Public, ratio)
		}
		moved = moved || ratio != 1
	}
	if !moved {
		t.Error("a signal that covers every size moved nothing")
	}
}

// A pair that scores fewer sizes than ExternalMinCovered does not count, and
// a size a pair does not score is not adjusted by it — never given a
// sibling's or an average score.
func TestPublicSignalsNeedCoverageAndNeverCrossSizes(t *testing.T) {
	e := mustEngine(t)
	cat := e.Catalogue
	few := everyTag(cat)[:e.Config.ExternalMinCovered-1]
	e.Public = publicSignals(cat, few)
	for _, tag := range few {
		if adj := e.publicAdjustment(entryByTag(t, cat, tag).Model.ID, pChat); adj != 1 {
			t.Errorf("%s: a pair covering %d sizes adjusted it ×%.3f", tag, len(few), adj)
		}
	}
	all := everyTag(cat)
	e.Public = publicSignals(cat, all[1:]) // the first size is not scored
	if adj := e.publicAdjustment(entryByTag(t, cat, all[0]).Model.ID, pChat); adj != 1 {
		t.Errorf("an unscored size was adjusted ×%.3f", adj)
	}
	if adj := e.publicAdjustment(entryByTag(t, cat, all[1]).Model.ID, pChat); math.Abs(adj-(1+e.Config.ExternalWeight)) > 1e-9 {
		t.Errorf("the best-rated size: ×%.3f, want ×%.3f", adj, 1+e.Config.ExternalWeight)
	}
	if adj := e.publicAdjustment(entryByTag(t, cat, all[len(all)-1]).Model.ID, pChat); math.Abs(adj-(1-e.Config.ExternalWeight)) > 1e-9 {
		t.Errorf("the worst-rated size: ×%.3f", adj)
	}
	if adj := e.publicAdjustment(entryByTag(t, cat, all[1]).Model.ID, pCode); adj != 1 {
		t.Errorf("a chat signal adjusted coding ×%.3f", adj)
	}
}

// The quant a size's tag pulls, when the curator has read it from the Ollama
// library, is the file the engine recommends.
func TestOllamaQuantPicksTheFile(t *testing.T) {
	e := mustEngine(t)
	entry := entryByTag(t, e.Catalogue, "llama3.1:8b")
	if f, _, ok := e.defaultFile(entry); !ok || f.Quant != "Q4_K_M" {
		t.Fatalf("default file %+v", f)
	}
	entry.Model.Size.OllamaQuant = "Q8_0"
	if f, _, ok := e.defaultFile(entry); !ok || f.Quant != "Q8_0" {
		t.Errorf("with ollama_quant Q8_0: %+v", f)
	}
}

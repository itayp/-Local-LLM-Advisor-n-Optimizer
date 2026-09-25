package recommend

import (
	"strings"
	"testing"

	"advisor/internal/catalog"
	"advisor/internal/figure"
)

func shippedSpeedNeeds(t *testing.T) *SpeedNeeds {
	t.Helper()
	sn, err := DefaultSpeedNeeds()
	if err != nil {
		t.Fatal(err)
	}
	return sn
}

func est(lo, hi float64) *figure.Rate {
	r := figure.EstimatedRange(lo, hi, "tok/s")
	return &r
}

func meas(v float64) *figure.Rate {
	r := figure.MeasuredRate(v, "tok/s")
	return &r
}

// The shipped bars (speed-needs.yaml, all still `chosen` or cited, none
// tuned here): stream usable 3.56, good 5.29, excellent 10.0 tok/s; chat,
// writing, coding, vision wait 1/4/10 s; reasoning, long documents and an
// agent's step 10/30/120 s.
func TestGradeSpeed(t *testing.T) {
	sn := shippedSpeedNeeds(t)
	cases := []struct {
		name        string
		p           catalog.Purpose
		gen, prompt *figure.Rate
		known       bool
		low, high   Grade
		limit       Limit
		waitKnown   bool
		wait        float64 // at the slower end
		src         figure.Source
	}{
		{"measured point, chat", pChat, meas(20), meas(1000), true, GradeExcellent, GradeExcellent, LimitWait, true, 0.07, figure.Measured},
		{"estimated range, both ends graded", pChat, est(4, 12), est(500, 1000), true, GradeUsable, GradeExcellent, LimitAnswerSpeed, true, 0.14, figure.Estimated},
		{"coding, the wait limits", pCode, meas(30), meas(400), true, GradeUsable, GradeUsable, LimitWait, true, 5, figure.Measured},
		{"coding range, slow end past usable", pCode, est(30, 40), est(181, 472), true, GradeTooSlow, GradeUsable, LimitWait, true, 2000.0 / 181, figure.Estimated},
		{"no prompt speed: answer speed alone", pChat, meas(6), nil, true, GradeGood, GradeGood, LimitAnswerSpeed, false, 0, figure.Measured},
		{"agent step with no prompt speed: unknown", catalog.PurposeAgentic, meas(50), nil, false, "", "", "", false, 0, ""},
		{"agent step", catalog.PurposeAgentic, meas(100), meas(500), true, GradeExcellent, GradeExcellent, LimitWait, true, 3.5, figure.Measured},
		{"reasoning thinks at answer speed", catalog.PurposeReasoning, meas(20), meas(1000), true, GradeUsable, GradeUsable, LimitWait, true, 0.07 + 50, figure.Measured},
		{"long document", pLong, meas(20), meas(1000), true, GradeGood, GradeGood, LimitWait, true, 20, figure.Measured},
		{"writing", catalog.PurposeWriting, meas(20), meas(100), true, GradeGood, GradeGood, LimitWait, true, 3, figure.Measured},
		{"vision, the stream limits", catalog.PurposeVision, meas(8), meas(2000), true, GradeGood, GradeGood, LimitAnswerSpeed, true, 0.5, figure.Measured},
		{"a measured answer with an estimated prompt is estimated", pChat, meas(20), est(500, 1000), true, GradeExcellent, GradeExcellent, LimitWait, true, 0.14, figure.Estimated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := sn.GradeSpeed(*c.gen, c.prompt, c.p)
			if g.Known != c.known || g.Low != c.low || g.High != c.high || g.Limit != c.limit || g.WaitKnown != c.waitKnown {
				t.Fatalf("got %+v", g)
			}
			if c.waitKnown && (g.WaitSeconds < c.wait-0.01 || g.WaitSeconds > c.wait+0.01) {
				t.Errorf("wait %v, want %v", g.WaitSeconds, c.wait)
			}
			if c.known && g.Source != c.src {
				t.Errorf("source %q, want %q", g.Source, c.src)
			}
		})
	}
}

// Every purpose grades, from the shipped file, for a fast measured model.
func TestGradeSpeedEveryPurpose(t *testing.T) {
	sn := shippedSpeedNeeds(t)
	for _, p := range catalog.Purposes {
		g := sn.GradeSpeed(*meas(60), meas(3000), p)
		if !g.Known || g.Low == "" || g.Source != figure.Measured {
			t.Errorf("%s: %+v", p, g)
		}
	}
}

func TestMeasuredVerdictsUseEachPurposesPromptSize(t *testing.T) {
	sn := shippedSpeedNeeds(t)
	purposes := []catalog.Purpose{pChat, pCode, pLong, catalog.PurposeAgentic}
	suite := []PromptMeasure{{500, meas(1000)}, {2000, meas(400)}, {8000, nil}}
	vs := sn.MeasuredVerdicts(meas(30), suite, purposes)
	by := map[catalog.Purpose]SpeedVerdict{}
	for _, v := range vs {
		by[v.Purpose] = v
		if v.Source != figure.Measured {
			t.Errorf("%s: a benchmark's verdict is measured: %+v", v.Purpose, v)
		}
	}
	if v := by[pChat]; v.Text != "excellent for everyday chat" || v.WaitText != "" || v.Note != "" {
		t.Errorf("chat reads at the 500 prompt: %+v", v)
	}
	if v := by[pCode]; v.Text != "usable for coding" || v.WaitText != "about 5 seconds to read a pasted file" || v.Wait == nil || v.Wait.Value != 5 {
		t.Errorf("coding reads at the 2000 prompt: %+v", v)
	}
	if v := by[pLong]; v.Text != "excellent for long documents" || !strings.Contains(v.Note, "answer speed alone") || !strings.Contains(v.Note, "no prompt as long as a long document") || v.Wait != nil {
		t.Errorf("a long document has no suite prompt: graded on answer speed, and says so: %+v", v)
	}
	if v := by[catalog.PurposeAgentic]; !v.Known || v.Low != GradeExcellent {
		t.Errorf("an agent step reads at the 2000 prompt: %+v", v)
	}

	// The 2000 prompt skipped: coding is never read from the 500 prompt.
	skipped := []PromptMeasure{{500, meas(1000)}, {2000, nil}, {8000, nil}}
	vs = sn.MeasuredVerdicts(meas(30), skipped, purposes)
	by = map[catalog.Purpose]SpeedVerdict{}
	for _, v := range vs {
		by[v.Purpose] = v
	}
	if v := by[pCode]; v.Text != "excellent for coding" || !strings.Contains(v.Note, "did not time a prompt the size of a pasted file") {
		t.Errorf("coding unmeasured: %+v", v)
	}
	if v := by[catalog.PurposeAgentic]; v.Known || !strings.HasPrefix(v.Note, "Not graded:") || v.Source != figure.Measured {
		t.Errorf("an agent step with nothing to grade: %+v", v)
	}
}

func TestVerdictsFollowTheSameGradeAsTheReason(t *testing.T) {
	e := mustEngine(t)
	m := goldenMachine(t, "AppleSiliconM1Pro")
	res := e.Recommend(m, []catalog.Purpose{pCode, pChat}, nil, Preferences{})
	if len(res.Recommendations) == 0 {
		t.Fatal("nothing recommended")
	}
	for _, r := range res.Recommendations {
		if len(r.Verdicts) != 2 {
			t.Fatalf("%s: one verdict per purpose asked: %+v", r.DisplayName, r.Verdicts)
		}
		for _, v := range r.Verdicts {
			found := false
			for _, reason := range r.Reasons {
				if reason.Kind == "speed" && strings.Contains(reason.Text, v.Text) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: verdict %q is not what the speed reason says: %+v", r.DisplayName, v.Text, r.Reasons)
			}
			if v.Source != r.Speed.Source {
				t.Errorf("%s: verdict source %q, speed %q", r.DisplayName, v.Source, r.Speed.Source)
			}
		}
	}
}

// The copy rule holds for every verdict's words.
func TestVerdictCopyRule(t *testing.T) {
	sn := shippedSpeedNeeds(t)
	var all []SpeedVerdict
	for _, gen := range []*figure.Rate{meas(2), meas(8), est(3, 30)} {
		for _, prompt := range []*figure.Rate{nil, meas(50), est(100, 5000)} {
			all = append(all, sn.Verdicts(gen, prompt, catalog.Purposes)...)
		}
		all = append(all, sn.MeasuredVerdicts(gen, []PromptMeasure{{500, nil}, {2000, meas(300)}}, catalog.Purposes)...)
	}
	for _, v := range all {
		for _, text := range []string{v.Text, v.WaitText, v.Note} {
			for _, term := range []string{"VRAM", "quantiz", "GGUF", "KV cache", "context window", "tokens/sec", "tok/s", "offload"} {
				if strings.Contains(strings.ToLower(text), strings.ToLower(term)) {
					t.Errorf("verdict uses %q: %q", term, text)
				}
			}
		}
		if v.Text == "" || !v.Source.Valid() {
			t.Errorf("a verdict has words and a source: %+v", v)
		}
		if v.Note != "" && !strings.HasSuffix(v.Note, ".") {
			t.Errorf("a note is a sentence: %q", v.Note)
		}
	}
	if sn.Verdicts(nil, meas(100), catalog.Purposes) != nil {
		t.Error("no speed, no verdict")
	}
}

func TestVerdictLine(t *testing.T) {
	sn := shippedSpeedNeeds(t)
	vs := sn.MeasuredVerdicts(meas(30), []PromptMeasure{{500, meas(1000)}, {2000, meas(400)}}, []catalog.Purpose{pChat, pCode})
	got := VerdictLine(vs)
	want := "Measured: excellent for everyday chat · usable for coding — about 5 seconds to read a pasted file."
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

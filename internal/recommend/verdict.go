package recommend

import (
	"math"
	"strings"

	"advisor/internal/catalog"
	"advisor/internal/figure"
)

// The speed verdict (backlog (j)): GradeSpeed's answer, as the API serves
// it beside every speed a person sees — Recommend's cards, a benchmark run
// and its history, a model's detail page. A sibling of reasons.go: the
// words are templated here from the same pieces (gradeAdjective,
// purposeWords, waitPhrase, waitAction), so a verdict and a card's speed
// reason never say different things. The UI adds labels ("Measured:"), not
// claims.

// SpeedVerdict is how a speed on this computer suits one purpose. It is
// derived from a local number, so it inherits that number's Source
// (product rule 4): measured only when every rate it used was measured.
// It never sits in a struct beside a figure.Public.
type SpeedVerdict struct {
	Purpose catalog.Purpose `json:"purpose"`
	// Known is false when there is nothing to grade with (an agent's step
	// with no prompt speed); Text and Note then say so in words.
	Known bool `json:"known"`
	// Low and High are the grades at the slower and faster ends of the
	// range; equal for a measurement. Codes for the UI's logic.
	Low  Grade `json:"low,omitempty"`
	High Grade `json:"high,omitempty"`
	// Limit is which bar decided the grade: the answer speed or the wait.
	Limit Limit `json:"limit,omitempty"`
	// Wait is the seconds before the first word for this purpose's typical
	// prompt (speed-needs.yaml), with the same source as the verdict;
	// absent when it could not be worked out.
	Wait   *figure.Rate  `json:"wait,omitempty"`
	Source figure.Source `json:"source"`
	// Text is the verdict in words: "excellent for everyday chat", "usable
	// to good for coding".
	Text string `json:"text"`
	// WaitText is the wait in words ("about 4 seconds to read a pasted
	// file"), set only when the wait is what holds the grade below
	// excellent — the thing a person would want to know.
	WaitText string `json:"wait_text,omitempty"`
	// Note says what the verdict rests on when it is less than both bars:
	// "Graded on answer speed alone: this test did not time a prompt the
	// size of a long document."
	Note string `json:"note,omitempty"`
}

// promptWhat names a purpose's typical prompt, for the notes.
var promptWhat = map[catalog.Purpose]string{
	catalog.PurposeChat:        "a chat message",
	catalog.PurposeWriting:     "a writing brief",
	catalog.PurposeCoding:      "a pasted file",
	catalog.PurposeVision:      "an image",
	catalog.PurposeReasoning:   "a question",
	catalog.PurposeLongContext: "a long document",
	catalog.PurposeAgentic:     "one step of an agent's work",
}

// DefaultVerdictPurposes are the purposes a verdict is for when the user has
// saved none: chat, the purpose onboarding starts from.
var DefaultVerdictPurposes = []catalog.Purpose{catalog.PurposeChat}

// Verdicts grades gen (and prompt, which may be nil) for each purpose. nil
// when gen is nil: no speed, no verdict — the screen already says why.
func (sn *SpeedNeeds) Verdicts(gen, prompt *figure.Rate, purposes []catalog.Purpose) []SpeedVerdict {
	if gen == nil || sn == nil {
		return nil
	}
	out := make([]SpeedVerdict, 0, len(purposes))
	for _, p := range purposes {
		why := ""
		if prompt == nil {
			why = "how long it takes to read " + promptWhat[p] + " is not known"
		}
		out = append(out, sn.verdict(sn.GradeSpeed(*gen, prompt, p), *gen, why))
	}
	return out
}

// PromptMeasure is one prompt of the benchmark suite: its nominal size in
// tokens (the suite's id: 500, 2000, 8000) and the prompt speed a run
// measured at it — nil when the run skipped it, did not ask for it, or the
// runtime gave no time.
type PromptMeasure struct {
	Tokens int
	Rate   *figure.Rate
}

// MeasuredVerdicts grades a benchmark run: its answer speed gen, and for
// each purpose the prompt speed measured at the suite prompt that stands for
// that purpose's typical prompt.
//
// Which prompt: the shortest suite prompt whose nominal size is at least
// the purpose's typical prompt in speed-needs.yaml (prompt_tokens, or
// step_prompt_tokens for an agent's step) — prompt speed falls as a prompt
// grows, so never a shorter one. With the shipped suite (500, 2000, 8000):
// chat and reasoning (70 tokens) and writing (300) read at 500; coding
// (2,000 — the suite's long prompt is the same size), vision (1,000) and an
// agent's step (1,000) at 2000; a long document (20,000) has no suite prompt
// long enough. When that prompt was not measured, or does not exist, the
// purpose is graded on answer speed alone and the note says so; it is never
// taken from another prompt size.
func (sn *SpeedNeeds) MeasuredVerdicts(gen *figure.Rate, suite []PromptMeasure, purposes []catalog.Purpose) []SpeedVerdict {
	if gen == nil || sn == nil {
		return nil
	}
	out := make([]SpeedVerdict, 0, len(purposes))
	for _, p := range purposes {
		need, ok := sn.Purpose(p)
		if !ok {
			continue
		}
		typical := need.typicalPromptTokens()
		var chosen *PromptMeasure
		for i := range suite {
			if float64(suite[i].Tokens) >= typical && (chosen == nil || suite[i].Tokens < chosen.Tokens) {
				chosen = &suite[i]
			}
		}
		var prompt *figure.Rate
		why := ""
		switch {
		case chosen == nil:
			why = "the test has no prompt as long as " + promptWhat[p]
		case chosen.Rate == nil:
			why = "this test did not time a prompt the size of " + promptWhat[p]
		default:
			prompt = chosen.Rate
		}
		out = append(out, sn.verdict(sn.GradeSpeed(*gen, prompt, p), *gen, why))
	}
	return out
}

// typicalPromptTokens is the new prompt a purpose reads before answering
// (per_step: one step's tool output).
func (p PurposeSpeedNeed) typicalPromptTokens() float64 {
	if p.Mode == ModePerStep && p.StepPromptTokens != nil {
		return p.StepPromptTokens.Value
	}
	if p.PromptTokens != nil {
		return p.PromptTokens.Value
	}
	return 0
}

// verdict turns a grade into what the API serves. why, when set, is why the
// wait could not be graded (a clause: "this test did not time …").
func (sn *SpeedNeeds) verdict(pg PurposeGrade, gen figure.Rate, why string) SpeedVerdict {
	label := purposeWords[pg.Purpose]
	v := SpeedVerdict{Purpose: pg.Purpose, Known: pg.Known, Source: pg.Source}
	if !pg.Known {
		v.Source = gen.Source
		if v.Source != figure.Measured {
			v.Source = figure.Estimated
		}
		v.Text = "not graded for " + label
		if why == "" {
			why = "there is nothing to grade it with"
		}
		v.Note = "Not graded: " + why + "."
		return v
	}
	v.Low, v.High, v.Limit = pg.Low, pg.High, pg.Limit
	adj := gradeAdjective(pg.Low)
	if pg.Low != pg.High {
		adj += " to " + gradeAdjective(pg.High)
	}
	v.Text = adj + " for " + label
	if pg.WaitKnown {
		slow, fast := round1s(pg.WaitSeconds), round1s(pg.WaitSecondsFast)
		var w figure.Rate
		if pg.Source == figure.Measured {
			w = figure.MeasuredRate(slow, "s")
		} else {
			w = figure.EstimatedRange(fast, slow, "s")
		}
		v.Wait = &w
		if pg.Limit == LimitWait && pg.Low != GradeExcellent {
			if action, ok := waitAction[pg.Purpose]; ok {
				v.WaitText = waitPhrase(pg.WaitSeconds) + " " + action
			}
		}
	} else if why != "" {
		v.Note = "Graded on answer speed alone: " + why + "."
	}
	return v
}

func round1s(v float64) float64 { return math.Round(v*10) / 10 }

// VerdictLine is the verdicts as one line of text, for `advisor bench` and
// `advisor recommend`: "Measured: excellent for everyday chat · good for
// coding — about 4 seconds to read a pasted file." Verdicts of each source
// get their own line, estimated after measured. The notes are not in it;
// the caller prints each verdict's Note beneath.
func VerdictLine(vs []SpeedVerdict) []string {
	var lines []string
	for _, src := range []figure.Source{figure.Measured, figure.Estimated} {
		var texts, waits []string
		for _, v := range vs {
			if v.Source != src {
				continue
			}
			texts = append(texts, v.Text)
			if v.WaitText != "" {
				waits = append(waits, v.WaitText)
			}
		}
		if len(texts) == 0 {
			continue
		}
		label := "Measured"
		if src == figure.Estimated {
			label = "Estimated"
		}
		line := label + ": " + strings.Join(texts, " · ")
		if len(waits) > 0 {
			line += " — " + strings.Join(waits, "; ")
		}
		lines = append(lines, line+".")
	}
	return lines
}

package recommend

import (
	"advisor/internal/catalog"
	"advisor/internal/estimate"
	"advisor/internal/figure"
)

// Grade is how well a model's speed suits a purpose (ARCHITECTURE.md D-58),
// best first.
type Grade string

const (
	GradeExcellent Grade = "excellent"
	GradeGood      Grade = "good"
	GradeUsable    Grade = "usable"
	GradeTooSlow   Grade = "too_slow" // below the usable bar
)

// gradeRank orders Grade worst-last, for "the worse of the two" (D-58).
var gradeRank = map[Grade]int{GradeExcellent: 0, GradeGood: 1, GradeUsable: 2, GradeTooSlow: 3}

// worseGrade is whichever of a, b a person would notice first.
func worseGrade(a, b Grade) Grade {
	if gradeRank[a] >= gradeRank[b] {
		return a
	}
	return b
}

// PurposeGrade is how a model's speed suits one purpose, at both ends of an
// estimated range (D-58's "grade at the range's low and high ends"). Low and
// High are equal for a measurement (a point, not a range) or when both ends
// land on the same grade. Known is false only when there is no speed
// estimate at all for this model — never a made-up grade (CLAUDE.md,
// "unknown is unknown").
type PurposeGrade struct {
	Purpose catalog.Purpose
	Known   bool
	Low     Grade
	High    Grade
	// WaitKnown is false when Speed.Prompt is nil: there is nothing to grade
	// the wait bar with, so the grade (when Known) rests on the stream bar
	// alone — or, for a per_step purpose with no stream bar to fall back on,
	// Known itself is false.
	WaitKnown bool
	// WaitSeconds is the wait at the low (slower, more cautious) end, for
	// the reason to quote when WaitLimits says the wait is what limits the
	// grade rather than the stream.
	WaitSeconds float64
	WaitLimits  bool
	// Source is estimated unless every rate the grade used was measured
	// (product rule 4: the grade inherits estimated/measured from its inputs).
	Source figure.Source
}

// gradePurpose grades one candidate's speed for one purpose, D-58's
// arithmetic: wait = prompt_tokens ÷ prompt tok/s + thinking_tokens ÷ answer
// tok/s (per_step: step_prompt_tokens ÷ prompt tok/s + step_output_tokens ÷
// answer tok/s); stream (read_along only) compares answer tok/s with the
// reading anchors. The purpose's grade is the worse of the two.
func (e *Engine) gradePurpose(est estimate.Estimate, p catalog.Purpose) PurposeGrade {
	pg := PurposeGrade{Purpose: p}
	sn := e.SpeedNeeds
	if sn == nil || !est.Speed.Known || est.Speed.Generation == nil {
		return pg
	}
	need, ok := sn.Purpose(p)
	if !ok {
		return pg
	}
	g := est.Speed.Generation
	promptKnown := est.Speed.Prompt != nil
	var pLow, pHigh float64
	if promptKnown {
		pLow, pHigh = est.Speed.Prompt.Low, est.Speed.Prompt.High
	}

	lowGrade, lowWait, lowWaitOK, lowLimits, lowOK := sn.gradeOneEnd(need, g.Low, pLow, promptKnown)
	if !lowOK {
		return pg
	}
	highGrade, _, _, highLimits, _ := sn.gradeOneEnd(need, g.High, pHigh, promptKnown)

	pg.Known = true
	pg.Low, pg.High = lowGrade, highGrade
	pg.WaitKnown = lowWaitOK
	pg.WaitLimits = lowWaitOK && (lowLimits || highLimits)
	pg.WaitSeconds = lowWait
	pg.Source = figure.Measured
	if g.Source != figure.Measured {
		pg.Source = figure.Estimated
	}
	if promptKnown && est.Speed.Prompt.Source != figure.Measured {
		pg.Source = figure.Estimated
	}
	return pg
}

// gradeOneEnd grades one end of the range (generation tok/s genTPS, prompt
// tok/s promptTPS): the overall grade, the wait it computed (when it could),
// whether the wait bar is what decided the worse grade (limits), and ok,
// which is false only when there is nothing to grade at all (a per_step
// purpose with no prompt-speed estimate has no stream bar to fall back on).
func (sn *SpeedNeeds) gradeOneEnd(need PurposeSpeedNeed, genTPS, promptTPS float64, promptKnown bool) (grade Grade, wait float64, waitOK, limits, ok bool) {
	hasStream := need.Mode == ModeReadAlong
	var streamGrade Grade
	if hasStream {
		streamGrade = sn.streamGrade(genTPS)
	}
	if promptKnown {
		if w, wok := need.waitSeconds(promptTPS, genTPS); wok {
			waitGrade := need.waitGrade(w)
			if !hasStream {
				return waitGrade, w, true, true, true
			}
			return worseGrade(streamGrade, waitGrade), w, true, gradeRank[waitGrade] >= gradeRank[streamGrade], true
		}
	}
	if hasStream {
		// The wait bar cannot be graded (no prompt-speed estimate, or the
		// mode's own tokens are missing); the grade rests on the stream
		// bar alone, and says so (WaitKnown false, set by the caller).
		return streamGrade, 0, false, false, true
	}
	return "", 0, false, false, false
}

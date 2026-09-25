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

// Limit is which of a purpose's two bars decided its grade (D-58): the
// answer speed (the stream bar) or the wait before the first word.
type Limit string

const (
	LimitAnswerSpeed Limit = "answer_speed"
	LimitWait        Limit = "wait"
)

// PurposeGrade is how a model's speed suits one purpose, at both ends of an
// estimated range (D-58's "grade at the range's low and high ends"). Low and
// High are equal for a measurement (a point, not a range) or when both ends
// land on the same grade. Known is false only when there is nothing to grade
// with — never a made-up grade (CLAUDE.md, "unknown is unknown").
type PurposeGrade struct {
	Purpose catalog.Purpose
	Known   bool
	Low     Grade
	High    Grade
	// WaitKnown is false when there is no prompt speed: there is nothing to
	// grade the wait bar with, so the grade (when Known) rests on the stream
	// bar alone — or, for a per_step purpose with no stream bar to fall back
	// on, Known itself is false.
	WaitKnown bool
	// WaitSeconds is the wait at the low (slower, more cautious) end, for
	// the reason to quote when WaitLimits says the wait is what limits the
	// grade rather than the stream; WaitSecondsFast is the wait at the high
	// end (equal for a measurement).
	WaitSeconds     float64
	WaitSecondsFast float64
	WaitLimits      bool
	// Limit is which bar decided Low, the grade a verdict leads with:
	// LimitWait when the wait did (always, for a per_step purpose; also on a
	// tie), LimitAnswerSpeed otherwise. Empty when Known is false.
	// WaitLimits, the reasons' older rule, also counts the high end.
	Limit Limit
	// Source is estimated unless every rate the grade used was measured
	// (product rule 4: the grade inherits estimated/measured from its inputs).
	Source figure.Source
}

// GradeSpeed grades a speed for one purpose — the one grading function
// every screen uses (the engine through gradePurpose, the benchmark and the
// model detail through the verdicts in verdict.go). gen is the answer
// (generation) speed and prompt the prompt-reading speed, each a low–high
// range in tokens a second; a measurement is a range of one point
// (figure.MeasuredRate). prompt may be nil: the wait is then unknown, never
// zero, and the grade rests on the stream bar alone.
//
// D-58's arithmetic: wait = prompt_tokens ÷ prompt tok/s + thinking_tokens ÷
// answer tok/s (per_step: step_prompt_tokens ÷ prompt tok/s +
// step_output_tokens ÷ answer tok/s); stream (read_along only) compares
// answer tok/s with the reading anchors. The purpose's grade is the worse of
// the two, computed at both ends of the range.
func (sn *SpeedNeeds) GradeSpeed(gen figure.Rate, prompt *figure.Rate, p catalog.Purpose) PurposeGrade {
	pg := PurposeGrade{Purpose: p}
	if sn == nil {
		return pg
	}
	need, ok := sn.Purpose(p)
	if !ok {
		return pg
	}
	promptKnown := prompt != nil
	var pLow, pHigh float64
	if promptKnown {
		pLow, pHigh = prompt.Low, prompt.High
	}

	lowGrade, lowWait, lowWaitOK, lowLimits, lowOK := sn.gradeOneEnd(need, gen.Low, pLow, promptKnown)
	if !lowOK {
		return pg
	}
	highGrade, highWait, _, highLimits, _ := sn.gradeOneEnd(need, gen.High, pHigh, promptKnown)

	pg.Known = true
	pg.Low, pg.High = lowGrade, highGrade
	pg.WaitKnown = lowWaitOK
	pg.WaitLimits = lowWaitOK && (lowLimits || highLimits)
	pg.WaitSeconds = lowWait
	pg.WaitSecondsFast = highWait
	pg.Limit = LimitAnswerSpeed
	if lowWaitOK && lowLimits {
		pg.Limit = LimitWait
	}
	pg.Source = figure.Measured
	if gen.Source != figure.Measured {
		pg.Source = figure.Estimated
	}
	if promptKnown && prompt.Source != figure.Measured {
		pg.Source = figure.Estimated
	}
	return pg
}

// gradePurpose grades one candidate's estimate for one purpose, through
// GradeSpeed. No speed estimate at all is an unknown grade.
func (e *Engine) gradePurpose(est estimate.Estimate, p catalog.Purpose) PurposeGrade {
	if !est.Speed.Known || est.Speed.Generation == nil {
		return PurposeGrade{Purpose: p}
	}
	return e.SpeedNeeds.GradeSpeed(*est.Speed.Generation, est.Speed.Prompt, p)
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

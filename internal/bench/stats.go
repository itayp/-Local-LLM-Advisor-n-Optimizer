package bench

import (
	"fmt"
	"math"
	"sort"

	"advisor/internal/figure"
)

// median of a non-empty slice; the middle value for an odd count, the mean
// of the two middle values for an even one. It does not reorder vs.
func median(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	s := append([]float64(nil), vs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// spread is (max − min) ÷ median: how far apart the runs were, relative to
// the value reported. 0 for fewer than two values or a zero median.
func spread(vs []float64) float64 {
	if len(vs) < 2 {
		return 0
	}
	lo, hi := vs[0], vs[0]
	for _, v := range vs[1:] {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	m := median(vs)
	if m == 0 {
		return 0
	}
	return (hi - lo) / m
}

// promptRate is the prompt tokens the runtime processed (not those it
// reused from its cache) over the time it spent, per second. 0 when the
// runtime reported no time.
func (t Timing) promptRate() float64 {
	n := t.PromptTokens
	if t.CachedKnown {
		n -= t.CachedTokens
	}
	if t.PromptMs <= 0 || n <= 0 {
		return 0
	}
	return float64(n) / t.PromptMs * 1000
}

// genRate is answer tokens per second of answering.
func (t Timing) genRate() float64 {
	if t.GenMs <= 0 || t.GenTokens <= 0 {
		return 0
	}
	return float64(t.GenTokens) / t.GenMs * 1000
}

// summarise turns a prompt's timed requests into its result: medians, the
// spread, and what makes the result less certain.
func summarise(prompt string, timings []Timing, completion int, cfg Config) PromptResult {
	r := PromptResult{Prompt: prompt, Runs: len(timings), Timings: timings}
	var gen, pr, ttft, ptoks, gtoks []float64
	cachedMax := 0
	for _, t := range timings {
		if v := t.genRate(); v > 0 {
			gen = append(gen, v)
		}
		if v := t.promptRate(); v > 0 {
			pr = append(pr, v)
		}
		if t.TTFTMs > 0 {
			ttft = append(ttft, t.TTFTMs)
		}
		ptoks = append(ptoks, float64(t.PromptTokens))
		gtoks = append(gtoks, float64(t.GenTokens))
		if t.CachedKnown && t.CachedTokens > cachedMax {
			cachedMax = t.CachedTokens
		}
	}
	r.PromptTokens = int(math.Round(median(ptoks)))
	r.GenTokens = int(math.Round(median(gtoks)))
	r.GenTPS = figure.MeasuredRate(round2(median(gen)), "tok/s")
	if len(pr) > 0 {
		p := figure.MeasuredRate(round2(median(pr)), "tok/s")
		r.PromptTPS = &p
	}
	if len(ttft) > 0 {
		t := figure.MeasuredRate(math.Round(median(ttft)), "ms")
		r.TTFT = &t
	}
	r.SpreadPct = round1(100 * spread(gen))
	r.PromptSpreadPct = round1(100 * spread(pr))

	if len(gen) > 1 && spread(gen) > cfg.SpreadNote {
		r.Notes = append(r.Notes, fmt.Sprintf("the %d timed runs disagreed by %.0f%% on the answering speed; something else may have been using the computer",
			len(gen), 100*spread(gen)))
	}
	if completion > 0 && float64(r.GenTokens) < cfg.ShortAnswerShare*float64(completion) {
		r.Notes = append(r.Notes, fmt.Sprintf("the model stopped after %d of the %d tokens it was given, so the answering speed rests on fewer tokens than planned",
			r.GenTokens, completion))
	}
	if r.PromptTokens > 0 && float64(cachedMax) > cfg.MaxCachedShare*float64(r.PromptTokens) {
		r.Notes = append(r.Notes, fmt.Sprintf("the runtime reused up to %d of the prompt's %d tokens from its cache; the reading speed counts only the rest", cachedMax, r.PromptTokens))
	}
	if len(pr) == 0 {
		r.Notes = append(r.Notes, "the runtime reported no time for reading the prompt")
	}
	return r
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

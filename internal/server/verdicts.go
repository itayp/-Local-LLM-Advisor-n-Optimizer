package server

import (
	"context"
	"strconv"
	"sync"

	"advisor/internal/bench"
	"advisor/internal/catalog"
	"advisor/internal/estimate"
	"advisor/internal/recommend"
)

// The speed verdict beside every speed (backlog (j), ARCHITECTURE.md D-58):
// graded by recommend.SpeedNeeds, for the purposes saved in the daemon's
// settings — the same ones Recommend uses — or chat when none are saved.

// verdictPurposes is the purposes a verdict is for.
func (s *Server) verdictPurposes(ctx context.Context) []catalog.Purpose {
	if p := s.purposesSetting(ctx); len(p) > 0 {
		return p
	}
	return recommend.DefaultVerdictPurposes
}

// estimateVerdicts grades an estimate (or the measurement that replaced
// it) for the purposes; nil when it has no speed.
func estimateVerdicts(est estimate.Estimate, purposes []catalog.Purpose) []recommend.SpeedVerdict {
	sn, err := recommend.DefaultSpeedNeeds()
	if err != nil || !est.Speed.Known {
		return nil
	}
	return sn.Verdicts(est.Speed.Generation, est.Speed.Prompt, purposes)
}

var (
	suitePromptsOnce sync.Once
	suitePromptSizes []int // the suite's prompts' nominal sizes, from their ids
)

func benchSuiteSizes() []int {
	suitePromptsOnce.Do(func() {
		suite, err := bench.DefaultSuite()
		if err != nil {
			return
		}
		for _, p := range suite.Prompts {
			if n, err := strconv.Atoi(p.ID); err == nil {
				suitePromptSizes = append(suitePromptSizes, n)
			}
		}
	})
	return suitePromptSizes
}

// withVerdicts adds a run's MEASURED verdicts: its headline answer speed,
// and for each purpose the prompt speed measured at the suite prompt that
// stands for that purpose's typical prompt (recommend.MeasuredVerdicts says
// which). A run with no answer speed gets none.
func withVerdicts(run *bench.Run, purposes []catalog.Purpose) {
	if run.GenTPS == nil {
		return
	}
	sn, err := recommend.DefaultSpeedNeeds()
	if err != nil {
		return
	}
	var suite []recommend.PromptMeasure
	for _, size := range benchSuiteSizes() {
		pm := recommend.PromptMeasure{Tokens: size}
		id := strconv.Itoa(size)
		for _, r := range run.Results {
			if r.Prompt == id && r.PromptTPS != nil {
				pm.Rate = r.PromptTPS
			}
		}
		suite = append(suite, pm)
	}
	run.Verdicts = sn.MeasuredVerdicts(run.GenTPS, suite, purposes)
}

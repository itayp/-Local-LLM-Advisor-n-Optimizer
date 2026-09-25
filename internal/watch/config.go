package watch

import "time"

// Config holds every constant the watch scheduler uses. Nothing here can be
// measured with an instrument: these are judgement calls, each CHOSEN and
// said why (CLAUDE.md: "constants live in configs").
type Config struct {
	// DefaultInterval: "default daily" (BUILD_PLAN.md step 10, item 1).
	// CHOSEN: often enough that a new size lands within a day of the
	// catalogue shipping it, rarely enough that a laptop that sleeps most of
	// the day still catches it on the days it is open.
	DefaultInterval time.Duration
	// Jitter spreads the daily check over this fraction of DefaultInterval
	// (0..1), picked once per daemon start, so every install does not read
	// Hugging Face and the public sources at the same minute. CHOSEN.
	Jitter float64
	// MaintainerRepoLimit bounds how many of a maintainer's most recent
	// repos (the Hub answers newest first) one run reads before giving up on
	// finding anything new: a maintainer with hundreds of repos does not
	// turn one check into hundreds of comparisons. CHOSEN: comfortably above
	// how many repos any curated maintainer has published in a year.
	MaintainerRepoLimit int
}

// DefaultConfig is what a fresh install runs with.
func DefaultConfig() Config {
	return Config{
		DefaultInterval:     24 * time.Hour,
		Jitter:              0.1,
		MaintainerRepoLimit: 100,
	}
}

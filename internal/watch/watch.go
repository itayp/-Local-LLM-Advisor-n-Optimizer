// Package watch is the new-model watch (PRD §12): a scheduler that refreshes
// the catalogue, runs every new candidate through Fit and Recommend, and
// notifies the user once per model, ever, only when it fits and beats the
// current model on a purpose they selected. Step 1: types only. Step 10
// implements it. It never pulls and never switches (product rule 5).
package watch

import (
	"time"

	"advisor/internal/recommend"
)

// Outcome of checking one candidate.
type Outcome string

const (
	OutcomeNotified    Outcome = "notified"
	OutcomeSuppressed  Outcome = "suppressed" // did not qualify; Reason says why
	OutcomeAlreadySeen Outcome = "already_seen"
	OutcomeFlagged     Outcome = "flagged_for_curator" // a new repo from a known maintainer; never recommended automatically
	OutcomeError       Outcome = "error"
)

// State is one watch_state row: what the watch knows about one candidate.
type State struct {
	Key           string     `json:"key"` // catalogue file id, or a repo for curator flags
	LastCheckedAt time.Time  `json:"last_checked_at"`
	NotifiedAt    *time.Time `json:"notified_at,omitempty"`
	Outcome       Outcome    `json:"outcome"`
	Reason        string     `json:"reason,omitempty"`
}

// Notification is what the desktop notification and the watch log carry.
// The body states the reasons exactly as PRD §12's example shows.
type Notification struct {
	Recommendation recommend.Recommendation `json:"recommendation"`
	Title          string                   `json:"title"`
	Body           string                   `json:"body"`
	CreatedAt      time.Time                `json:"created_at"`
}

// LogEntry is one line of the watch log in the UI: what was checked, when,
// what was found, what was suppressed and why.
type LogEntry struct {
	At      time.Time `json:"at"`
	Key     string    `json:"key"`
	Outcome Outcome   `json:"outcome"`
	Detail  string    `json:"detail"`
}

// Settings for the watch, kept in the settings table.
type Settings struct {
	Enabled  bool          `json:"enabled"`
	Interval time.Duration `json:"interval" source:"n/a"` // default daily, jittered; configuration
	Quiet    bool          `json:"quiet"`                 // log only, no desktop notification
}

// Package watch is the new-model watch (PRD §12): a scheduler that refreshes
// the catalogue, runs every curated size not yet notified about through Fit
// and Recommend, and notifies the user once per model, ever, only when it
// fits and beats the current model on a purpose they selected. A new repo
// from a curated family's own maintainer is flagged for the curator instead
// — it is not in the catalogue, so nothing can score it (build-plan step 10;
// ARCHITECTURE.md D-57). It never pulls and never switches (product rule 5).
//
// The files:
//
//	watch.go        the types the API serves, and Config
//	run.go          Run: the whole check, once
//	reasons.go      the notification's title and body, from the same
//	                templated sentences recommend.Recommendation already
//	                carries (product rule 3) — no prose of its own
//	maintainers.go  the curator-flag half: new repos from known maintainers
//	notify.go       the Notifier interface and the per-OS desktop notice
package watch

import (
	"time"

	"advisor/internal/recommend"
)

// Outcome of checking one candidate.
type Outcome string

const (
	OutcomeNotified    Outcome = "notified"
	OutcomeSuppressed  Outcome = "suppressed" // did not qualify, or notifications are off; Reason says why
	OutcomeAlreadySeen Outcome = "already_seen"
	OutcomeFlagged     Outcome = "flagged_for_curator" // a new repo from a known maintainer; never recommended automatically
	OutcomeError       Outcome = "error"
)

// State is one watch_state row: what the watch knows about one candidate —
// a curated size ("model:<catalog_models.id>") or a maintainer's repo
// ("repo:<owner>/<name>").
type State struct {
	Key           string     `json:"key"`
	LastCheckedAt time.Time  `json:"last_checked_at"`
	NotifiedAt    *time.Time `json:"notified_at,omitempty"` // once set, never notified again for this key
	Outcome       Outcome    `json:"outcome"`
	Reason        string     `json:"reason,omitempty"`
}

// Notification is what the desktop notification and the watch log carry.
// The body states the reasons exactly as PRD §12's example shows: "Why it
// may matter to you:" and a bullet per reason, in the same words the
// Recommend screen would show for this card (reasons.go).
type Notification struct {
	Recommendation recommend.Recommendation `json:"recommendation"`
	Title          string                   `json:"title"`
	Body           string                   `json:"body"`
	// URL is where clicking the notification should take the user — that
	// model's card, with "Run benchmark" the next thing to click (never
	// pulls or switches anything itself: product rule 5). A packaged
	// desktop app (build-plan step 11) is what lets the OS route an actual
	// click there; until then every OS notifier still shows it, in words, so
	// nothing here depends on that step arriving first.
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// LogEntry is one line of the watch log in the UI: what was checked, when,
// what was found, what was suppressed and why.
type LogEntry struct {
	At      time.Time `json:"at"`
	Key     string    `json:"key"`
	Name    string    `json:"name"` // the model's display name, or the repo id — what the log shows, never the raw key
	Outcome Outcome   `json:"outcome"`
	Detail  string    `json:"detail"`
}

// NotifyMode governs whether a qualifying candidate becomes a desktop
// notification popup. The check itself — refreshing, running Fit and
// Recommend, writing the log — happens the same in every mode; Settings.
// Enabled is the master switch that turns that off. Product rule 5: this
// setting only ever adds or removes a notice, never pulls or switches
// anything itself.
type NotifyMode string

const (
	NotifyOn    NotifyMode = "on"    // a real desktop notification, once per model, ever
	NotifyQuiet NotifyMode = "quiet" // recorded and logged as notified, no popup shown
	NotifyNever NotifyMode = "never" // not evaluated for notification at all (still refreshed, checked and logged); turning this back on gives every model a fair first look
)

// Valid reports whether m is one of the three modes above.
func (m NotifyMode) Valid() bool { return m == NotifyOn || m == NotifyQuiet || m == NotifyNever }

// Settings for the watch, kept in the settings table (server/settings.go).
type Settings struct {
	// Enabled is the master switch: false runs no refresh, no check, no log
	// line at all — a full pause, not just quieter notifications.
	Enabled bool `json:"enabled"`
	// Mode governs the desktop notification (BUILD_PLAN.md step 10, item
	// 3's "quiet setting and a 'never' setting").
	Mode NotifyMode `json:"mode"`
	// Interval overrides Config.DefaultInterval; 0 uses the default.
	Interval time.Duration `json:"interval" source:"n/a"` // configuration
}

// DefaultSettings is what a fresh install starts with: on, and notifying.
func DefaultSettings() Settings { return Settings{Enabled: true, Mode: NotifyOn} }

// Report is what one watch run did — the whole check, once — for the CLI,
// the API and watch_runs.
type Report struct {
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Trigger    string `json:"trigger"` // "scheduler" | "api" | "cli"

	Checked     int `json:"checked" source:"n/a"` // curated sizes evaluated this run (excludes already_seen)
	Notified    int `json:"notified" source:"n/a"`
	Suppressed  int `json:"suppressed" source:"n/a"`
	AlreadySeen int `json:"already_seen" source:"n/a"`
	Flagged     int `json:"flagged" source:"n/a"` // new maintainer repos found this run
	Errors      int `json:"errors" source:"n/a"`

	// RefreshError is the catalogue refresh's own error, in words, when it
	// could not run at all; the check still runs against whatever the
	// catalogue already held.
	RefreshError string `json:"refresh_error,omitempty"`
	// MaintainerError is the same, for the maintainer scan.
	MaintainerError string `json:"maintainer_error,omitempty"`

	Entries []LogEntry `json:"entries"`
}

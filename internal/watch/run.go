package watch

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/catalog/hf"
	"advisor/internal/estimate"
	"advisor/internal/recommend"
	"advisor/internal/store"
)

// Options is what one watch run needs. The catalogue refresh itself
// (BUILD_PLAN.md step 10, item 1's first half) is not run here: the caller
// runs it first (internal/server/watch.go calls the same RefreshCatalog
// POST /api/catalog/refresh does, with Trigger "watch") and hands Run the
// result — Catalogue and Engine are read fresh, after that refresh, so a
// size resolved for the first time today is already a candidate this call
// can see.
type Options struct {
	Store *store.Store

	// Catalogue is families.yaml as loaded for this refresh — read for the
	// maintainer scan (maintainers.go): every curated size's hf_base_repo
	// and its owner.
	Catalogue *catalog.Catalogue
	// HF reads a maintainer's repos (hf.Client.ListByAuthor). Nil skips the
	// maintainer scan; the model check still runs.
	HF *hf.Client

	// Engine is configured for this machine: Estimator, Config, Catalogue
	// (every present curated size), Measurements and Public already set —
	// built the same way GET /api/recommend builds one.
	Engine    *recommend.Engine
	Machine   estimate.Machine
	Purposes  []catalog.Purpose
	Installed []recommend.InstalledModel

	// PublicLine answers the same question GET /api/recommend answers with
	// external.View.Line: a candidate's one public-data line for these
	// purposes, or nil when it has none. It is a closure, not a dependency
	// on internal/catalog/external directly, so this package stays as free
	// of that import as internal/recommend itself is — the server supplies
	// it (internal/server/watch.go), built over the same View GET
	// /api/recommend reads. Nil: notifications and the log carry no public
	// bullet, same as a size with nothing public.
	PublicLine func(modelID int64, purposes []catalog.Purpose) *catalog.PublicEntry

	// Notifier shows the desktop notification for a candidate that
	// qualifies. Nil, or Settings.Mode other than NotifyOn: the check and
	// the log still happen, no popup does.
	Notifier Notifier
	Settings Settings
	Config   Config

	// URL builds the deep link a notification's click should open — that
	// candidate's card, "Run benchmark" the next thing to click (product
	// rule 5). Nil: notifications carry no URL.
	URL func(catalogModelID int64) string

	Trigger string // "scheduler" | "api" | "cli"
	// RefreshError is set by the caller when the catalogue refresh it ran
	// before calling Run (item 1's first half) failed outright: Run still
	// checks whatever the catalogue already held, and the report says the
	// refresh itself did not finish.
	RefreshError string
	Log          *slog.Logger
	// Now is time.Now; tests replace it.
	Now func() time.Time
}

func (o *Options) log() *slog.Logger {
	if o.Log != nil {
		return o.Log
	}
	return slog.Default()
}

func (o *Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Run is the whole check, once: every curated size not yet notified about
// goes through Fit and Recommend against the stored profile and the user's
// purposes (item 2), qualifying for a notification only when it fits and
// beats the current model on a purpose asked for — which is exactly what
// appearing in Engine.Recommend's own cards for those purposes already
// means (ARCHITECTURE.md D-57), so Run asks the engine once for the whole
// catalogue and checks each candidate's place in that answer rather than
// re-deriving the rule. A repo new to a curated maintainer is flagged for
// the curator instead (maintainers.go) — never scored, since nothing
// outside families.yaml has an Entry to score.
func Run(ctx context.Context, o Options) (Report, error) {
	now := o.now()
	rep := Report{StartedAt: now.UTC().Format(time.RFC3339), Trigger: o.Trigger, RefreshError: o.RefreshError, Entries: []LogEntry{}}

	if !o.Settings.Enabled {
		rep.FinishedAt = o.now().UTC().Format(time.RFC3339)
		return rep, nil // the master switch: no refresh call was even made for this
	}
	if o.Store == nil {
		return rep, errors.New("watch: no database to check against")
	}

	existing, err := o.Store.WatchStates(ctx)
	if err != nil {
		return rep, err
	}

	// Item 1, second half: new repos from curated maintainers. Flagged only
	// — never fed to Fit or Recommend.
	flagged, mErr := checkMaintainers(ctx, o, toWatchStates(existing), now)
	if mErr != nil {
		rep.MaintainerError = mErr.Error()
		o.log().Warn("watch: the maintainer scan did not finish", "err", mErr)
	}
	for _, e := range flagged {
		rep.Entries = append(rep.Entries, e)
		rep.Flagged++
		if err := o.Store.UpsertWatchState(ctx, store.WatchStateRow{
			Key: e.Key, LastCheckedAt: rfc3339(now), Outcome: string(e.Outcome), Reason: e.Detail,
		}); err != nil {
			return rep, err
		}
	}

	// Item 2: every curated size, Fit and Recommend, once per run.
	var result recommend.Result
	haveResult := false
	if o.Settings.Mode != NotifyNever && o.Engine != nil {
		result = o.Engine.Recommend(o.Machine, o.Purposes, o.Installed, recommend.Preferences{})
		haveResult = true
	}

	if o.Engine != nil {
		for _, entry := range o.Engine.Catalogue {
			if !entry.Model.Present {
				continue // left families.yaml; nothing to notify about
			}
			key := "model:" + strconv.FormatInt(entry.Model.ID, 10)
			if st, ok := existing[key]; ok && st.NotifiedAt != "" {
				rep.AlreadySeen++
				rep.Entries = append(rep.Entries, LogEntry{
					At: now, Key: key, Name: entry.DisplayName, Outcome: OutcomeAlreadySeen,
					Detail: "already notified on " + notifiedDay(st.NotifiedAt),
				})
				continue
			}

			rep.Checked++
			le := checkOne(o, entry, result, haveResult, now)
			rep.Entries = append(rep.Entries, le)
			switch le.Outcome {
			case OutcomeNotified:
				rep.Notified++
			case OutcomeSuppressed:
				rep.Suppressed++
			case OutcomeError:
				rep.Errors++
			}

			row := store.WatchStateRow{Key: key, LastCheckedAt: rfc3339(now), Outcome: string(le.Outcome), Reason: le.Detail}
			if le.Outcome == OutcomeNotified {
				row.NotifiedAt = rfc3339(now)
			}
			if err := o.Store.UpsertWatchState(ctx, row); err != nil {
				return rep, err
			}
		}
	}

	rep.FinishedAt = o.now().UTC().Format(time.RFC3339)
	if err := o.Store.RecordWatchRun(ctx, store.WatchRunRow{
		CreatedAt: rep.StartedAt, FinishedAt: rep.FinishedAt, Trigger: rep.Trigger,
		Checked: rep.Checked, Notified: rep.Notified, Suppressed: rep.Suppressed, Flagged: rep.Flagged,
		ReportJSON: mustJSON(rep),
	}); err != nil {
		return rep, err
	}
	return rep, nil
}

// checkOne evaluates one curated size against this run's Recommend answer
// (or, in NotifyNever mode, against nothing) and returns its log line,
// having already sent the desktop notification when it qualifies and
// Settings.Mode is NotifyOn.
func checkOne(o Options, entry recommend.Entry, result recommend.Result, haveResult bool, now time.Time) LogEntry {
	key := "model:" + strconv.FormatInt(entry.Model.ID, 10)
	name := entry.DisplayName

	if o.Settings.Mode == NotifyNever {
		return LogEntry{At: now, Key: key, Name: name, Outcome: OutcomeSuppressed, Detail: "notifications are turned off"}
	}
	if !haveResult {
		return LogEntry{At: now, Key: key, Name: name, Outcome: OutcomeSuppressed, Detail: "nothing to compare against yet"}
	}
	if _, _, ok := o.Engine.DefaultFile(entry); !ok {
		return LogEntry{At: now, Key: key, Name: name, Outcome: OutcomeSuppressed, Detail: "not resolved against Hugging Face yet"}
	}

	for _, rec := range result.Recommendations {
		if rec.Model.ID != entry.Model.ID {
			continue
		}
		// It made the cut for the purposes asked: fits, and — when there is
		// a current model — a real change from it (recommend.Engine.
		// Recommend already dropped anything that changes nothing).
		if o.PublicLine != nil {
			rec.Public = o.PublicLine(rec.Model.ID, o.Purposes)
		}
		url := ""
		if o.URL != nil {
			url = o.URL(entry.Model.ID)
		}
		note := notificationFor(rec, result.Current, url, now)
		if o.Notifier != nil && o.Settings.Mode == NotifyOn {
			if err := o.Notifier.Notify(context.Background(), note); err != nil {
				o.log().Warn("watch: showing the desktop notification", "model", name, "err", err)
			}
		}
		detail := "notified"
		if len(note.Recommendation.Reasons) > 0 {
			detail = note.Recommendation.Reasons[0].Text
		}
		return LogEntry{At: now, Key: key, Name: name, Outcome: OutcomeNotified, Detail: detail}
	}

	return LogEntry{At: now, Key: key, Name: name, Outcome: OutcomeSuppressed, Detail: suppressedReason(result, result.Current)}
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// toWatchStates adapts the store's plain rows to the State shape
// maintainers.go reads (existing[key] presence is all it needs).
func toWatchStates(rows map[string]store.WatchStateRow) map[string]State {
	out := make(map[string]State, len(rows))
	for k, r := range rows {
		out[k] = State{Key: r.Key, Outcome: Outcome(r.Outcome), Reason: r.Reason}
	}
	return out
}

func mustJSON(rep Report) string {
	b, err := json.Marshal(rep)
	if err != nil {
		// Report is a plain struct of strings, ints and a slice of the
		// same, plus recommend.Result values that already marshal for the
		// API; this cannot fail. If it ever does, an empty report is stored
		// rather than losing the run's summary columns.
		return "{}"
	}
	return string(b)
}

// notifiedDay reads the day out of an RFC 3339 timestamp, for the "already
// notified on 2026-09-25" log line; the raw string's first 10 characters
// when it does not parse, rather than nothing at all.
func notifiedDay(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Format("2006-01-02")
	}
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

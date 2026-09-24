// Package external reads public benchmark data about the catalogue's models
// from the sources step 9a approved (research/EXTERNAL_SOURCES.md,
// ARCHITECTURE.md D-53) and stores it in catalog_external — never in the
// tables or columns that hold this machine's own estimates and measurements.
//
// The pieces:
//
//	config.go   external.yaml (the approved sources, the excluded publishers,
//	            the metric → purpose map) and aliases.yaml (each source's names
//	            for catalogue sizes), loaded strictly and validated
//	fetch.go    the one polite HTTP client all three sources go through,
//	            limited to the hosts the enabled sources name
//	hfevals.go  E-1: Hugging Face Eval Results, read from each size's original
//	            repo (hf_base_repo)
//	arena.go    E-2: Arena's leaderboard dataset, through Hugging Face's
//	            Dataset Viewer API
//	epoch.go    E-3: Epoch AI's benchmark ZIP, Epoch's own runs only
//	run.go      Run: every enabled source at most once per its cadence, the
//	            report (what each source hit and missed across the curated
//	            sizes), failures kept in words
//	public.go   the read side: what a size's public block shows (a position
//	            in words, the origin beneath) and what the recommendation
//	            engine may score
//
// A source that fails keeps the values it stored last time; a failure never
// blocks a recommendation. A value that leaves its source is marked absent,
// never deleted.
package external

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/store"
	"advisor/internal/version"
)

// Options is what a run needs.
type Options struct {
	Catalogue *catalog.Catalogue
	Config    *Config
	Aliases   Aliases
	Store     *store.Store
	// Fetcher, when nil, is NewFetcher over the enabled sources' hosts.
	Fetcher *Fetcher
	Log     *slog.Logger
	Trigger string // cli | api | watch
	// Force reads every enabled source now, whatever its cadence.
	Force bool
	// Only, when not empty, limits the Hugging Face read to these families'
	// sizes (Arena and Epoch are one download each and are read whole).
	Only []string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Progress, when set, is told before each enabled source is read (done
	// sources so far, how many there are, the source's name); it must not
	// block.
	Progress func(done, total int, name string)
	// SourceDeadline bounds one source's read; past it the source fails in
	// words, keeps what it stored, and is due again at the next refresh.
	// 0 is DefaultSourceDeadline.
	SourceDeadline time.Duration
}

// DefaultSourceDeadline is how long one source may take in a refresh: a
// source that cannot answer in this time is not waited for (Itay,
// 2026-09-24: the Arena read took a quarter of an hour on both machines).
const DefaultSourceDeadline = 3 * time.Minute

// Report is what a run did, for the CLI, the API and catalog_refreshes.
type Report struct {
	StartedAt  string         `json:"started_at"`
	FinishedAt string         `json:"finished_at"`
	Trigger    string         `json:"trigger"`
	Sources    []SourceReport `json:"sources"`
	Coverage   []SizeCoverage `json:"coverage"` // every present catalogue size, and how many public values each source has for it
	Warnings   []string       `json:"warnings"` // for the curator: repos whose cards disagree with families.yaml, ...
}

// Source statuses.
const (
	StatusRead      = "read"      // the source was read this run
	StatusUnchanged = "unchanged" // the source said nothing changed (304, or the same leaderboard date)
	StatusSkipped   = "skipped"   // read recently; its cadence has not come round
	StatusFailed    = "failed"    // could not be read; what it stored before is kept
	StatusDisabled  = "disabled"  // switched off in external.yaml
)

// SourceReport is one source's part of a run.
type SourceReport struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Summary  string   `json:"summary"`         // one sentence
	Error    string   `json:"error,omitempty"` // why the source failed, in words
	Stats    Stats    `json:"stats"`
	Stored   int      `json:"stored" source:"n/a"`        // values written (upserted) this run; a count
	Absent   int      `json:"marked_absent" source:"n/a"` // values that left the source this run; a count
	Hits     []string `json:"hits"`                       // curated sizes with at least one value from this source
	Misses   []string `json:"misses"`                     // curated sizes with none
	Failures []string `json:"failures"`                   // per size or per metric, in words
	// For the curator:
	AliasesNotSeen []string    `json:"aliases_not_seen,omitempty"` // aliases.yaml names the source did not answer with
	Candidates     []Candidate `json:"candidates,omitempty"`       // source names nothing maps, worth an alias
	UnknownMetrics []string    `json:"unknown_metrics,omitempty"`  // metrics the source answered with that the map does not list
	Pending        int         `json:"pending" source:"n/a"`       // Hugging Face results in open pull requests, not ingested; a count
	Dropped        []string    `json:"dropped,omitempty"`          // results whose stated source is an excluded publisher
	Unreadable     []string    `json:"unreadable,omitempty"`       // entries whose shape or value could not be read
	Subsets        []string    `json:"subsets,omitempty"`          // Arena: the subsets the dataset has
}

// Candidate is a source's model name that no alias maps.
type Candidate struct {
	Name    string `json:"name"`
	Licence string `json:"licence,omitempty"`
	// Suggest is the catalogue family whose id the name looks like; a hint
	// for the curator, never written anywhere by the advisor.
	Suggest string `json:"suggest,omitempty"`
}

// SizeCoverage is one catalogue size's public values, per source.
type SizeCoverage struct {
	Size     string         `json:"size"` // its Ollama tag
	FamilyID string         `json:"family_id"`
	Values   map[string]int `json:"values" source:"n/a"` // source id → values shown for this size; a count
}

// sizeRef is a present catalogue size with its family.
type sizeRef struct {
	ID     int64
	Family catalog.Family
	Size   catalog.Size
}

func (s sizeRef) label() string { return s.Size.OllamaTag }

type runner struct {
	o      Options
	now    time.Time
	sizes  []sizeRef
	byKey  map[store.CatalogKey]sizeRef
	report *Report
	// part tells the progress what the current source is reading now.
	part func(what string)
}

// Run reads every enabled source that is due and returns the report. It
// returns an error only when it could not work at all (the store failed, the
// context was cancelled); a source that fails is recorded in its report and
// in external_state, and the others are read.
func Run(ctx context.Context, o Options) (Report, error) {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Fetcher == nil {
		o.Fetcher = NewFetcher(version.UserAgent(), o.Config.EnabledHosts())
		o.Fetcher.Log = o.Log
	}
	if o.SourceDeadline <= 0 {
		o.SourceDeadline = DefaultSourceDeadline
	}
	r := &runner{o: o, now: o.Now().UTC(), byKey: map[store.CatalogKey]sizeRef{}, part: func(string) {}}
	rep := Report{StartedAt: r.now.Format(time.RFC3339), Trigger: o.Trigger, Sources: []SourceReport{}, Coverage: []SizeCoverage{}, Warnings: []string{}}
	r.report = &rep

	rows, err := o.Store.CatalogModels(ctx, false)
	if err != nil {
		return rep, err
	}
	ids := map[store.CatalogKey]int64{}
	for _, row := range rows {
		ids[store.CatalogKey{FamilyID: row.Model.FamilyID, Parameters: row.Model.Size.Parameters}] = row.Model.ID
	}
	for _, fam := range o.Catalogue.Families {
		for _, sz := range fam.Sizes {
			k := store.CatalogKey{FamilyID: fam.ID, Parameters: sz.Parameters}
			id, ok := ids[k]
			if !ok {
				continue // not synced: the refresh syncs the YAML before calling Run
			}
			ref := sizeRef{ID: id, Family: fam, Size: sz}
			r.sizes = append(r.sizes, ref)
			r.byKey[k] = ref
		}
	}

	enabled := 0
	for _, src := range o.Config.Sources {
		if src.Enabled {
			enabled++
		}
	}
	read := 0
	for _, src := range o.Config.Sources {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if src.Enabled && o.Progress != nil {
			done, name := read, src.Name
			o.Progress(done, enabled, name)
			r.part = func(what string) { o.Progress(done, enabled, name+" ("+what+")") }
		}
		if src.Enabled {
			read++
		}
		sr := SourceReport{ID: src.ID, Name: src.Name, Hits: []string{}, Misses: []string{}, Failures: []string{}}
		if !src.Enabled {
			sr.Status, sr.Summary = StatusDisabled, src.Name+" is switched off in external.yaml; nothing was requested, and what it stored before is neither shown nor scored"
			rep.Sources = append(rep.Sources, sr)
			continue
		}
		state, _, err := o.Store.GetExternalState(ctx, src.ID, "")
		if err != nil {
			return rep, err
		}
		dig := r.digest(src)
		sameConfig := state.ConfigDigest == dig
		// A source read only in part last time (a board or a repo whose
		// answer failed) is due again at once: waiting out its cadence
		// would leave the gap for a day or a week.
		partsFailed, err := o.Store.ExternalPartsFailed(ctx, src.ID)
		if err != nil {
			return rep, err
		}
		if !o.Force && sameConfig && state.OKAt != "" && state.Error == "" && partsFailed == 0 {
			if ok, err := time.Parse(time.RFC3339, state.OKAt); err == nil && r.now.Sub(ok) < time.Duration(src.CadenceHours)*time.Hour {
				next := ok.Add(time.Duration(src.CadenceHours) * time.Hour)
				sr.Status = StatusSkipped
				sr.Summary = fmt.Sprintf("read %s; not due again until %s", ok.Format("2 Jan 2006 15:04 MST"), next.Format("2 Jan 2006 15:04 MST"))
				rep.Sources = append(rep.Sources, sr)
				continue
			}
		}
		before := o.Fetcher.Stats()
		var runErr error
		srcCtx, cancel := context.WithTimeout(ctx, o.SourceDeadline)
		switch src.ID {
		case SourceHFEvals:
			runErr = r.hfEvals(srcCtx, src, &sr, sameConfig)
		case SourceArena:
			runErr = r.arena(srcCtx, src, &sr, sameConfig)
		case SourceEpoch:
			runErr = r.epoch(srcCtx, src, &sr, sameConfig)
		}
		timedOut := srcCtx.Err() != nil
		cancel()
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		after := o.Fetcher.Stats()
		sr.Stats = Stats{
			Requests: after.Requests - before.Requests, NotModified: after.NotModified - before.NotModified,
			Retries: after.Retries - before.Retries, BytesRead: after.BytesRead - before.BytesRead,
			WaitedSeconds: after.WaitedSeconds - before.WaitedSeconds,
		}
		state.Source, state.Key, state.AttemptedAt = src.ID, "", r.now.Format(time.RFC3339)
		if timedOut {
			// Whatever it was doing when time ran out (a request, or storing
			// one), the source stops here; what it stored stays.
			runErr = fmt.Errorf("%s took longer than %s to answer, so the advisor stopped waiting; what it stored before is kept, and the next refresh tries again",
				src.Name, minutesWords(o.SourceDeadline))
		}
		if runErr != nil {
			var se *storeError
			if errors.As(runErr, &se) && !timedOut {
				return rep, se.err
			}
			sr.Status, sr.Error = StatusFailed, runErr.Error()
			sr.Summary = "could not be read: " + sr.Error
			state.Error = sr.Error
			o.Log.Warn("external source failed", "source", src.ID, "err", sr.Error)
		} else {
			if sr.Status == "" {
				sr.Status = StatusRead
			}
			state.OKAt, state.Error, state.ConfigDigest = r.now.Format(time.RFC3339), "", dig
		}
		if err := o.Store.PutExternalState(ctx, state); err != nil {
			return rep, err
		}
		rep.Sources = append(rep.Sources, sr)
	}

	if err := r.coverage(ctx); err != nil {
		return rep, err
	}
	rep.FinishedAt = o.Now().UTC().Format(time.RFC3339)
	return rep, nil
}

// storeError marks a store failure inside a client: it stops the run.
type storeError struct{ err error }

func (e *storeError) Error() string { return e.err.Error() }
func (e *storeError) Unwrap() error { return e.err }

// replace stores one read's values, counting them on sr.
func (r *runner) replace(ctx context.Context, sr *SourceReport, scope store.ExternalScope, rows []catalog.External) error {
	gone, err := r.o.Store.ReplaceExternal(ctx, scope, rows)
	if err != nil {
		return &storeError{err}
	}
	sr.Stored += len(rows)
	sr.Absent += gone
	return nil
}

// state and putState wrap external_state for one request or metric.
func (r *runner) state(ctx context.Context, source, key string) (store.ExternalState, error) {
	st, _, err := r.o.Store.GetExternalState(ctx, source, key)
	if err != nil {
		return st, &storeError{err}
	}
	return st, nil
}

func (r *runner) putState(ctx context.Context, st store.ExternalState) error {
	st.AttemptedAt = r.now.Format(time.RFC3339)
	if st.Error == "" {
		st.OKAt = st.AttemptedAt
	}
	if err := r.o.Store.PutExternalState(ctx, st); err != nil {
		return &storeError{err}
	}
	return nil
}

// digest is what src's stored rows depend on besides the answers.
func (r *runner) digest(src Source) string {
	var repos []string
	for _, s := range r.sizes {
		repos = append(repos, s.Size.HFBaseRepo+"="+s.Family.Maintainer)
	}
	var als []string
	for _, a := range r.o.Aliases[src.ID] {
		als = append(als, fmt.Sprintf("%s=%s/%d", a.Name, a.Family, a.Parameters))
	}
	var metrics []string
	for _, m := range r.o.Config.MetricsOf(src.ID) {
		metrics = append(metrics, m.Metric+"|"+m.File)
	}
	return digest(src.URL, src.Columns, src.Dataset, src.Split, src.Attribution, src.Licence, r.o.Config.ExcludedHosts, repos, als, metrics)
}

// alias resolves a source's model name to a catalogue size, exactly.
func (r *runner) alias(source, name string) (sizeRef, bool) {
	for _, a := range r.o.Aliases[source] {
		if a.Name == name {
			ref, ok := r.byKey[store.CatalogKey{FamilyID: a.Family, Parameters: a.Parameters}]
			return ref, ok
		}
	}
	return sizeRef{}, false
}

// suggest is the catalogue family a source's name looks like: its id with
// the punctuation gone is inside the name with the punctuation gone. A hint
// for the curator's report only.
func (r *runner) suggest(name string) string {
	n := squash(name)
	best := ""
	for _, f := range r.o.Catalogue.Families {
		id := squash(f.ID)
		if id != "" && strings.Contains(n, id) && len(id) > len(best) {
			best = f.ID
		}
	}
	return best
}

func squash(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// coverage fills the report's per-size table and each source's hits and misses.
func (r *runner) coverage(ctx context.Context) error {
	values, err := r.o.Store.ExternalValues(ctx, false)
	if err != nil {
		return err
	}
	count := map[int64]map[string]int{}
	for _, v := range values {
		if _, ok := r.o.Config.Metric(v.Source, v.Metric); !ok {
			continue
		}
		if count[v.ModelID] == nil {
			count[v.ModelID] = map[string]int{}
		}
		count[v.ModelID][v.Source]++
	}
	for _, s := range r.sizes {
		c := count[s.ID]
		if c == nil {
			c = map[string]int{}
		}
		r.report.Coverage = append(r.report.Coverage, SizeCoverage{Size: s.label(), FamilyID: s.Family.ID, Values: c})
	}
	for i := range r.report.Sources {
		sr := &r.report.Sources[i]
		if sr.Status == StatusDisabled {
			continue
		}
		for _, s := range r.sizes {
			if count[s.ID][sr.ID] > 0 {
				sr.Hits = append(sr.Hits, s.label())
			} else {
				sr.Misses = append(sr.Misses, s.label())
			}
		}
		switch sr.Status {
		case StatusSkipped:
		case StatusFailed:
			sr.Summary = fmt.Sprintf("could not be read — %s; public values for %d of the %d curated sizes, as stored before",
				sr.Error, len(sr.Hits), len(r.sizes))
		default:
			sr.Summary = fmt.Sprintf("%s; public values for %d of the %d curated sizes", statusWords(sr.Status), len(sr.Hits), len(r.sizes))
		}
		sort.Strings(sr.AliasesNotSeen)
		sort.Slice(sr.Candidates, func(a, b int) bool { return sr.Candidates[a].Name < sr.Candidates[b].Name })
	}
	return nil
}

func statusWords(s string) string {
	switch s {
	case StatusRead:
		return "read"
	case StatusUnchanged:
		return "unchanged since the last read"
	}
	return s
}

// EnabledHosts is every host the enabled sources name.
func (c *Config) EnabledHosts() []string {
	var out []string
	for _, s := range c.Sources {
		if s.Enabled {
			out = append(out, s.Hosts...)
		}
	}
	return out
}

// day turns a source's timestamp into YYYY-MM-DD; "" when it cannot be read.
func day(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04:05.999999", time.DateOnly, time.RFC1123, "January 2, 2006", "Jan 2, 2006", "2006/01/02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.DateOnly)
		}
	}
	if len(s) >= 10 {
		if t, err := time.Parse(time.DateOnly, s[:10]); err == nil {
			return t.Format(time.DateOnly)
		}
	}
	return ""
}

// fill replaces {key} placeholders in an attribution template.
func fill(tmpl string, vals map[string]string) string {
	for k, v := range vals {
		tmpl = strings.ReplaceAll(tmpl, "{"+k+"}", v)
	}
	return tmpl
}

// addCandidate appends a candidate once.
func addCandidate(sr *SourceReport, seen map[string]bool, c Candidate) {
	if seen[c.Name] {
		return
	}
	seen[c.Name] = true
	sr.Candidates = append(sr.Candidates, c)
}

// notSeen lists the source's aliases no answer this run contained.
func (r *runner) notSeen(source string, seen map[string]bool) []string {
	var out []string
	for _, a := range r.o.Aliases[source] {
		if !seen[a.Name] {
			out = append(out, a.Name)
		}
	}
	return out
}

// Stored is the coverage report of what is already stored, without the
// network: each source's last read and its hits and misses across the
// curated sizes (`advisor catalog external -report`).
func Stored(ctx context.Context, st *store.Store, cat *catalog.Catalogue, cfg *Config) (Report, error) {
	now := time.Now().UTC()
	rep := Report{StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339), Trigger: "report",
		Sources: []SourceReport{}, Coverage: []SizeCoverage{}, Warnings: []string{}}
	r := &runner{o: Options{Catalogue: cat, Config: cfg, Store: st}, now: now, byKey: map[store.CatalogKey]sizeRef{}, report: &rep}
	rows, err := st.CatalogModels(ctx, false)
	if err != nil {
		return rep, err
	}
	ids := map[store.CatalogKey]int64{}
	for _, row := range rows {
		ids[store.CatalogKey{FamilyID: row.Model.FamilyID, Parameters: row.Model.Size.Parameters}] = row.Model.ID
	}
	for _, fam := range cat.Families {
		for _, sz := range fam.Sizes {
			if id, ok := ids[store.CatalogKey{FamilyID: fam.ID, Parameters: sz.Parameters}]; ok {
				r.sizes = append(r.sizes, sizeRef{ID: id, Family: fam, Size: sz})
			}
		}
	}
	states, err := st.ExternalStates(ctx)
	if err != nil {
		return rep, err
	}
	for _, src := range cfg.Sources {
		sr := SourceReport{ID: src.ID, Name: src.Name, Hits: []string{}, Misses: []string{}, Failures: []string{}}
		s, ok := states[src.ID]
		switch {
		case !src.Enabled:
			sr.Status, sr.Summary = StatusDisabled, src.Name+" is switched off in external.yaml"
		case !ok || s.OKAt == "":
			sr.Status, sr.Error = StatusFailed, "never read successfully"
			if s.Error != "" {
				sr.Error = s.Error
			}
		case s.Error != "":
			sr.Status, sr.Error = StatusFailed, s.Error
		default:
			sr.Status = StatusRead
		}
		rep.Sources = append(rep.Sources, sr)
	}
	if err := r.coverage(ctx); err != nil {
		return rep, err
	}
	for i := range rep.Sources {
		if s, ok := states[rep.Sources[i].ID]; ok && s.OKAt != "" && rep.Sources[i].Status != StatusDisabled {
			rep.Sources[i].Summary += " (last read " + s.OKAt + ")"
		}
	}
	return rep, nil
}

// minutesWords is a duration in whole minutes, in words.
func minutesWords(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	switch {
	case m < 1:
		return fmt.Sprintf("%d seconds", int(d/time.Second))
	case m == 1:
		return "a minute"
	}
	return fmt.Sprintf("%d minutes", m)
}

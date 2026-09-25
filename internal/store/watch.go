package store

import (
	"context"
	"database/sql"
	"fmt"
)

// WatchStateRow is one watch_state row (build-plan step 10, schema v0):
// what the watch knows about one candidate — a curated size
// ("model:<catalog_models.id>") or a maintainer's repo
// ("repo:<owner>/<name>"). Store never imports internal/watch (D-11);
// watch.State is the same shape, one level up — internal/watch converts.
type WatchStateRow struct {
	Key           string
	LastCheckedAt string // RFC 3339 UTC
	NotifiedAt    string // RFC 3339 UTC; "" until notified — once per key, ever
	Outcome       string // watch.Outcome
	Reason        string
}

// UpsertWatchState writes one watch_state row, creating it or updating it in
// place — the settings table's shape (settings.go): no history, only the
// latest word on each candidate. NotifiedAt, once set by any call, is never
// cleared or replaced by a later one with an empty NotifiedAt: "once per
// model, ever" holds here too, not only in the caller that decided it.
func (s *Store) UpsertWatchState(ctx context.Context, st WatchStateRow) error {
	var notifiedAt sql.NullString
	if st.NotifiedAt != "" {
		notifiedAt = sql.NullString{String: st.NotifiedAt, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO watch_state (created_at, key, last_checked_at, notified_at, outcome, reason)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET
			last_checked_at = excluded.last_checked_at,
			notified_at     = COALESCE(watch_state.notified_at, excluded.notified_at),
			outcome         = excluded.outcome,
			reason          = excluded.reason
	`, Now(), st.Key, st.LastCheckedAt, notifiedAt, st.Outcome, st.Reason)
	if err != nil {
		return fmt.Errorf("store: writing watch state %q: %w", st.Key, err)
	}
	return nil
}

// WatchStates returns every watch_state row, keyed by key — a watch run
// reads this once, to skip what has already been notified or flagged.
func (s *Store) WatchStates(ctx context.Context) (map[string]WatchStateRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, last_checked_at, notified_at, outcome, reason FROM watch_state`)
	if err != nil {
		return nil, fmt.Errorf("store: reading watch state: %w", err)
	}
	defer rows.Close()
	out := map[string]WatchStateRow{}
	for rows.Next() {
		var st WatchStateRow
		var notifiedAt sql.NullString
		if err := rows.Scan(&st.Key, &st.LastCheckedAt, &notifiedAt, &st.Outcome, &st.Reason); err != nil {
			return nil, fmt.Errorf("store: reading watch state: %w", err)
		}
		st.NotifiedAt = notifiedAt.String
		out[st.Key] = st
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading watch state: %w", err)
	}
	return out, nil
}

// WatchRunRow is one watch_runs row: a run's summary in plain columns
// (queried by the log screen) beside its full report as JSON — the same
// shape catalog_refreshes uses for refresh.Report.
type WatchRunRow struct {
	ID                                     int64
	CreatedAt, FinishedAt, Trigger         string
	Checked, Notified, Suppressed, Flagged int
	ReportJSON                             string
}

// RecordWatchRun stores one watch run's report.
func (s *Store) RecordWatchRun(ctx context.Context, row WatchRunRow) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO watch_runs (created_at, finished_at, trigger, checked, notified, suppressed, flagged, report_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, row.CreatedAt, row.FinishedAt, row.Trigger, row.Checked, row.Notified, row.Suppressed, row.Flagged, row.ReportJSON)
	if err != nil {
		return fmt.Errorf("store: recording a watch run: %w", err)
	}
	return nil
}

// RecentWatchRuns returns the most recent watch runs, newest first, limit
// at most (20 when limit <= 0) — the watch log's source (build-plan step
// 10, item 4).
func (s *Store) RecentWatchRuns(ctx context.Context, limit int) ([]WatchRunRow, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, created_at, finished_at, trigger, checked, notified, suppressed, flagged, report_json
		FROM watch_runs ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: reading watch runs: %w", err)
	}
	defer rows.Close()
	var out []WatchRunRow
	for rows.Next() {
		var row WatchRunRow
		if err := rows.Scan(&row.ID, &row.CreatedAt, &row.FinishedAt, &row.Trigger,
			&row.Checked, &row.Notified, &row.Suppressed, &row.Flagged, &row.ReportJSON); err != nil {
			return nil, fmt.Errorf("store: reading watch runs: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading watch runs: %w", err)
	}
	return out, nil
}

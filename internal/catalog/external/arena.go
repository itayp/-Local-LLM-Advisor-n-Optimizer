package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/figure"
	"advisor/internal/store"
)

// arenaPageRows is the Dataset Viewer's largest page, and arenaMaxPages
// bounds one board's read: a board larger than that is not what the metric
// map expects, and the metric fails in words rather than read on.
const (
	arenaPageRows = 100
	arenaMaxPages = 60
)

// arena is E-2: Arena's own CC BY 4.0 leaderboard dataset on Hugging Face,
// read through the Dataset Viewer API (JSON, no Parquet reader):
//
//	GET /splits?dataset=…                       which subsets exist
//	GET /filter?dataset=…&config=<subset>&split=latest&where="category"='<category>'&offset=…&length=100
//
// once per (subset, category) the metric map names. When a board's newest
// publish date is the one stored and nothing it depends on changed, the
// rest of it is not read. Rows whose model name is in aliases.yaml are
// stored; unmapped open-licence names go to the curator's report.
func (r *runner) arena(ctx context.Context, src Source, sr *SourceReport, conditional bool) error {
	base := strings.TrimRight(src.URL, "/")
	q := url.Values{"dataset": {src.Dataset}}
	resp, err := r.o.Fetcher.Get(ctx, base+"/splits?"+q.Encode(), Validators{})
	if err != nil {
		return errors.New(describe(err, "Hugging Face's dataset viewer"))
	}
	var splits struct {
		Splits []struct {
			Config string `json:"config"`
			Split  string `json:"split"`
		} `json:"splits"`
	}
	if err := json.Unmarshal(resp.Body, &splits); err != nil || len(splits.Splits) == 0 {
		return fmt.Errorf("the dataset viewer's list of %s's subsets was not the shape the advisor reads", src.Dataset)
	}
	subsets := map[string]bool{}
	for _, s := range splits.Splits {
		if s.Split == src.Split {
			subsets[s.Config] = true
			sr.Subsets = append(sr.Subsets, s.Config)
		}
	}
	sort.Strings(sr.Subsets)

	col := src.Columns
	seenAlias := map[string]bool{}
	seenCand := map[string]bool{}
	readAll, unchanged := true, 0
	for _, m := range r.o.Config.MetricsOf(src.ID) {
		if err := ctx.Err(); err != nil {
			return err
		}
		subset, category, _ := strings.Cut(strings.TrimPrefix(m.Metric, "arena:"), "/")
		if !subsets[subset] {
			sr.Failures = append(sr.Failures, fmt.Sprintf("%s: the dataset has no %q subset with a %q split (it has: %s) — check the metric map",
				m.Metric, subset, src.Split, strings.Join(sr.Subsets, ", ")))
			readAll = false
			continue
		}
		st, err := r.state(ctx, src.ID, m.Metric)
		if err != nil {
			return err
		}
		r.part(fmt.Sprintf("the %s board", strings.ReplaceAll(category, "_", " ")))
		board, boardRows, newest, fail, err := r.arenaBoard(ctx, base, src, subset, category, conditional, st.LastModified)
		if err != nil {
			return err // unreachable, rate-limited: the whole source waits
		}
		if fail != "" {
			sr.Failures = append(sr.Failures, m.Metric+": "+fail)
			st.Error = fail
			readAll = false
			if err := r.putState(ctx, st); err != nil {
				return err
			}
			continue
		}
		st.Error = ""
		if board == nil { // the same board as last time
			unchanged++
			readAll = false
			if err := r.putState(ctx, st); err != nil {
				return err
			}
			continue
		}
		var rows []catalog.External
		mapped := map[int64]bool{}
		for _, row := range board {
			name, _ := row[col["model"]].(string)
			if name == "" {
				continue
			}
			ref, ok := r.alias(src.ID, name)
			if !ok {
				lic, _ := row[col["licence"]].(string)
				if col["licence"] == "" || !strings.Contains(strings.ToLower(lic), "proprietary") {
					addCandidate(sr, seenCand, Candidate{Name: name, Licence: lic, Suggest: r.suggest(name)})
				}
				continue
			}
			seenAlias[name] = true
			if mapped[ref.ID] {
				continue // two rows for one size on one board: the first (the board's order) stands
			}
			value, ok := number(row[col["value"]])
			if !ok {
				sr.Unreadable = append(sr.Unreadable, fmt.Sprintf("%s: %s's rating %v is not a number", m.Metric, name, row[col["value"]]))
				continue
			}
			date := day(row[col["date"]])
			if date == "" {
				sr.Unreadable = append(sr.Unreadable, fmt.Sprintf("%s: %s's row has no readable publish date", m.Metric, name))
				continue
			}
			mapped[ref.ID] = true
			detail := map[string]any{"board_rows": boardRows, "category": category, "subset": subset}
			for _, k := range []string{"lower", "upper", "votes", "rank"} {
				if c := col[k]; c != "" {
					if v, ok := number(row[c]); ok {
						detail[k] = v
					}
				}
			}
			rows = append(rows, catalog.External{
				Source: src.ID, SourceModel: name, ModelID: ref.ID, Metric: m.Metric, Value: value,
				ValueText: strconv.FormatFloat(value, 'f', -1, 64), SourceDate: date,
				SourceURL:  "https://huggingface.co/datasets/" + src.Dataset,
				Provenance: string(figure.ProvenanceCrowd), Attribution: fill(src.Attribution, map[string]string{"date": date}),
				License: src.Licence, Detail: detail, FetchedAt: r.now.Format(time.RFC3339),
			})
		}
		if err := r.replace(ctx, sr, store.ExternalScope{Source: src.ID, Metric: m.Metric}, rows); err != nil {
			return err
		}
		st.LastModified = newest
		if err := r.putState(ctx, st); err != nil {
			return err
		}
	}
	if readAll {
		sr.AliasesNotSeen = r.notSeen(src.ID, seenAlias)
	}
	if n := len(r.o.Config.MetricsOf(src.ID)); n > 0 && unchanged == n {
		sr.Status = StatusUnchanged
	}
	return nil
}

// arenaFilterPage is one /filter answer, as far as E-2 reads it.
type arenaFilterPage struct {
	Features []struct {
		Name string `json:"name"`
	} `json:"features"`
	Rows []struct {
		Row map[string]any `json:"row"`
	} `json:"rows"`
	NumRowsTotal int `json:"num_rows_total"`
}

// arenaBoard reads one (subset, category) board. It returns the rows read,
// the board's size and its newest publish date; nil rows when the board is
// the one stored (same newest date as stored, and conditional); fail is a
// sentence when the board could not be used; err when the source could not
// be reached.
//
// The daemon reads a board in two small requests, not page by page: one
// row of the whole board (it exists, has the columns, how many rows, how
// recent), then only the rows whose model name aliases.yaml lists — the
// only rows it can store. The first coverage report read every board page
// by page, some 25 requests, and the dataset viewer's "index is loading"
// answers and Hugging Face's rate limit stretched that to a quarter of an
// hour (2026-09-24). Options.WholeBoards reads every row, for the curator's
// list of candidate names.
func (r *runner) arenaBoard(ctx context.Context, base string, src Source, subset, category string, conditional bool, storedDate string) (rows []map[string]any, total int, newest, fail string, err error) {
	col := src.Columns
	board := fmt.Sprintf(`"%s"='%s'`, col["category"], sqlQuote(category))
	get := func(where string, offset, length int) (*arenaFilterPage, string, error) {
		q := url.Values{
			"dataset": {src.Dataset}, "config": {subset}, "split": {src.Split}, "where": {where},
			"offset": {strconv.Itoa(offset)}, "length": {strconv.Itoa(length)},
		}
		resp, gerr := r.o.Fetcher.Get(ctx, base+"/filter?"+q.Encode(), Validators{})
		if gerr != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			if errors.Is(gerr, ErrUnreachable) || errors.Is(gerr, ErrRateLimited) || errors.Is(gerr, ErrHost) {
				return nil, "", errors.New(describe(gerr, "Hugging Face's dataset viewer"))
			}
			return nil, describe(gerr, "Hugging Face's dataset viewer"), nil
		}
		var ans arenaFilterPage
		if err := json.Unmarshal(resp.Body, &ans); err != nil {
			return nil, "the dataset viewer's answer was not the shape the advisor reads", nil
		}
		return &ans, "", nil
	}
	// all reads every row matching where, a page at a time.
	all := func(where string, first *arenaFilterPage) ([]map[string]any, string, error) {
		var out []map[string]any
		page, n := first, 0
		for {
			if page == nil {
				if n >= arenaMaxPages {
					return nil, fmt.Sprintf("the board has more than %d rows; the advisor does not read one that large", arenaMaxPages*arenaPageRows), nil
				}
				var f string
				var err error
				if page, f, err = get(where, n*arenaPageRows, arenaPageRows); err != nil || f != "" {
					return nil, f, err
				}
			}
			for _, rw := range page.Rows {
				out = append(out, rw.Row)
			}
			n++
			if len(page.Rows) == 0 || n*arenaPageRows >= page.NumRowsTotal {
				return out, "", nil
			}
			page = nil
		}
	}

	length := 1
	if r.o.WholeBoards {
		length = arenaPageRows
	}
	probe, fail, err := get(board, 0, length)
	if err != nil || fail != "" {
		return nil, 0, "", fail, err
	}
	have := map[string]bool{}
	for _, f := range probe.Features {
		have[f.Name] = true
	}
	var missing []string
	for _, k := range requiredColumns[SourceArena] {
		if !have[col[k]] {
			missing = append(missing, col[k])
		}
	}
	if len(missing) > 0 {
		return nil, 0, "", fmt.Sprintf("the board has no column %s — check columns in external.yaml", strings.Join(missing, ", ")), nil
	}
	if probe.NumRowsTotal == 0 || len(probe.Rows) == 0 {
		return nil, 0, "", fmt.Sprintf("the board has no rows for category %q — check the metric map", category), nil
	}
	total = probe.NumRowsTotal
	for _, rw := range probe.Rows {
		if d := day(rw.Row[col["date"]]); d > newest {
			newest = d
		}
	}
	if conditional && !r.o.WholeBoards && newest != "" && newest == storedDate {
		return nil, total, newest, "", nil
	}

	if r.o.WholeBoards {
		rows, fail, err = all(board, probe)
	} else if names := r.aliasNames(src.ID); len(names) > 0 {
		parts := make([]string, len(names))
		for i, n := range names {
			parts[i] = fmt.Sprintf(`"%s"='%s'`, col["model"], sqlQuote(n))
		}
		rows, fail, err = all(board+" AND ("+strings.Join(parts, " OR ")+")", nil)
		if err == nil && fail != "" {
			// The viewer refused the narrower question: ask the plain one,
			// page by page — slower, within the same deadline.
			r.o.Log.Warn("the dataset viewer refused a filter by model name; reading the whole board", "category", category, "answer", fail)
			rows, fail, err = all(board, nil)
		}
	}
	if err != nil || fail != "" {
		return nil, 0, "", fail, err
	}
	for _, rw := range rows {
		if d := day(rw[col["date"]]); d > newest {
			newest = d
		}
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, total, newest, "", nil
}

// sqlQuote doubles single quotes for the dataset viewer's where clause.
func sqlQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }

// aliasNames is the source's aliased model names, sorted: the requests stay
// a function of aliases.yaml alone.
func (r *runner) aliasNames(source string) []string {
	var out []string
	for _, a := range r.o.Aliases[source] {
		out = append(out, a.Name)
	}
	sort.Strings(out)
	return out
}

// number reads a JSON or CSV number.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(x), "%"), 64)
		return f, err == nil
	}
	return 0, false
}

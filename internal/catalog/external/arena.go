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
		board, newest, fail, err := r.arenaBoard(ctx, base, src, subset, category, conditional, st.LastModified)
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
			detail := map[string]any{"board_rows": len(board), "category": category, "subset": subset}
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

// arenaBoard reads one (subset, category) board. It returns the rows and the
// board's newest publish date; nil rows when the board is the one stored
// (same newest date as stored, and conditional); fail is a sentence when
// the board could not be used; err when the source could not be reached.
func (r *runner) arenaBoard(ctx context.Context, base string, src Source, subset, category string, conditional bool, storedDate string) (rows []map[string]any, newest, fail string, err error) {
	col := src.Columns
	where := fmt.Sprintf(`"%s"='%s'`, col["category"], strings.ReplaceAll(category, "'", "''"))
	total := -1
	for page := 0; total < 0 || page*arenaPageRows < total; page++ {
		if page >= arenaMaxPages {
			return nil, "", fmt.Sprintf("the board has more than %d rows; the advisor does not read one that large", arenaMaxPages*arenaPageRows), nil
		}
		q := url.Values{
			"dataset": {src.Dataset}, "config": {subset}, "split": {src.Split}, "where": {where},
			"offset": {strconv.Itoa(page * arenaPageRows)}, "length": {strconv.Itoa(arenaPageRows)},
		}
		resp, gerr := r.o.Fetcher.Get(ctx, base+"/filter?"+q.Encode(), Validators{})
		if gerr != nil {
			if errors.Is(gerr, ErrUnreachable) || errors.Is(gerr, ErrRateLimited) || errors.Is(gerr, ErrHost) {
				return nil, "", "", errors.New(describe(gerr, "Hugging Face's dataset viewer"))
			}
			return nil, "", describe(gerr, "Hugging Face's dataset viewer"), nil
		}
		var ans struct {
			Features []struct {
				Name string `json:"name"`
			} `json:"features"`
			Rows []struct {
				Row map[string]any `json:"row"`
			} `json:"rows"`
			NumRowsTotal int `json:"num_rows_total"`
		}
		if err := json.Unmarshal(resp.Body, &ans); err != nil {
			return nil, "", "the dataset viewer's answer was not the shape the advisor reads", nil
		}
		if page == 0 {
			have := map[string]bool{}
			for _, f := range ans.Features {
				have[f.Name] = true
			}
			var missing []string
			for _, k := range requiredColumns[SourceArena] {
				if !have[col[k]] {
					missing = append(missing, col[k])
				}
			}
			if len(missing) > 0 {
				return nil, "", fmt.Sprintf("the board has no column %s — check columns in external.yaml", strings.Join(missing, ", ")), nil
			}
			if ans.NumRowsTotal == 0 || len(ans.Rows) == 0 {
				return nil, "", fmt.Sprintf("the board has no rows for category %q — check the metric map", category), nil
			}
			total = ans.NumRowsTotal
			for _, rw := range ans.Rows {
				if d := day(rw.Row[col["date"]]); d > newest {
					newest = d
				}
			}
			if conditional && newest != "" && newest == storedDate {
				return nil, newest, "", nil
			}
		}
		for _, rw := range ans.Rows {
			rows = append(rows, rw.Row)
		}
		if len(ans.Rows) == 0 {
			break
		}
	}
	for _, rw := range rows {
		if d := day(rw[col["date"]]); d > newest {
			newest = d
		}
	}
	return rows, newest, "", nil
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

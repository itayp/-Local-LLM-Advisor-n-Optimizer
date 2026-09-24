package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/catalog/parquet"
	"advisor/internal/figure"
	"advisor/internal/store"
)

// arena is E-2: Arena's own CC BY 4.0 leaderboard dataset on Hugging Face,
// read as the files Arena publishes (ARCHITECTURE.md D-56):
//
//	GET /api/datasets/{dataset}/tree/main/{subset}          the subset's files, with their content hashes
//	GET /datasets/{dataset}/resolve/main/{subset}/{split}-…parquet   each of the split's files (a redirect to Hugging Face's CDN)
//
// once per subset the metric map names — the whole leaderboard of that
// subset, every category, in one file of well under a megabyte, read with
// internal/catalog/parquet. A subset whose files have the content hashes
// stored last time is not downloaded again. Rows whose model name is in
// aliases.yaml are stored, per category the map names; unmapped
// open-licence names go to the curator's report.
//
// It replaced reading the same data through the Dataset Viewer's /filter,
// which answered "the dataset index is loading" for minutes at a time
// (2026-09-24, on both fleet machines).
func (r *runner) arena(ctx context.Context, src Source, sr *SourceReport, conditional bool) error {
	base := strings.TrimRight(src.URL, "/")
	bySubset := map[string][]Metric{}
	var subsets []string
	for _, m := range r.o.Config.MetricsOf(src.ID) {
		subset, _, _ := strings.Cut(strings.TrimPrefix(m.Metric, "arena:"), "/")
		if bySubset[subset] == nil {
			subsets = append(subsets, subset)
		}
		bySubset[subset] = append(bySubset[subset], m)
	}
	sr.Subsets = append([]string(nil), subsets...)
	sort.Strings(sr.Subsets)

	seenAlias := map[string]bool{}
	seenCand := map[string]bool{}
	readAll, unchanged := true, 0
	for _, subset := range subsets {
		if err := ctx.Err(); err != nil {
			return err
		}
		metrics := bySubset[subset]
		failAll := func(fail string) error {
			readAll = false
			for _, m := range metrics {
				sr.Failures = append(sr.Failures, m.Metric+": "+fail)
				st, err := r.state(ctx, src.ID, m.Metric)
				if err != nil {
					return err
				}
				st.Error = fail
				if err := r.putState(ctx, st); err != nil {
					return err
				}
			}
			return nil
		}
		r.part(fmt.Sprintf("the %s leaderboard", strings.ReplaceAll(subset, "_", " ")))
		files, version, fail, err := r.arenaFiles(ctx, base, src, subset)
		if err != nil {
			return err
		}
		if fail != "" {
			if err := failAll(fail); err != nil {
				return err
			}
			continue
		}
		fileState, err := r.state(ctx, src.ID, "files:"+subset)
		if err != nil {
			return err
		}
		if conditional && fileState.ETag == version {
			same := true
			for _, m := range metrics {
				st, err := r.state(ctx, src.ID, m.Metric)
				if err != nil {
					return err
				}
				same = same && st.Error == "" && st.OKAt != ""
			}
			if same { // the files stored last time, every board read from them then
				unchanged += len(metrics)
				readAll = false
				if err := r.putState(ctx, fileState); err != nil {
					return err
				}
				continue
			}
		}
		rows, fail, err := r.arenaRows(ctx, base, src, files)
		if err != nil {
			return err
		}
		if fail != "" {
			if err := failAll(fail); err != nil {
				return err
			}
			continue
		}
		boards := map[string][]map[string]any{}
		for _, row := range rows {
			if c, ok := row[src.Columns["category"]].(string); ok {
				boards[c] = append(boards[c], row)
			}
		}
		for _, m := range metrics {
			_, category, _ := strings.Cut(strings.TrimPrefix(m.Metric, "arena:"), "/")
			st, err := r.state(ctx, src.ID, m.Metric)
			if err != nil {
				return err
			}
			board := boards[category]
			if len(board) == 0 {
				fail := fmt.Sprintf("the %s leaderboard has no rows for category %q — check the metric map", subset, category)
				sr.Failures = append(sr.Failures, m.Metric+": "+fail)
				readAll = false
				st.Error = fail
				if err := r.putState(ctx, st); err != nil {
					return err
				}
				continue
			}
			stored := r.arenaBoard(src, sr, m, subset, category, board, seenAlias, seenCand)
			if err := r.replace(ctx, sr, store.ExternalScope{Source: src.ID, Metric: m.Metric}, stored); err != nil {
				return err
			}
			st.Error = ""
			if err := r.putState(ctx, st); err != nil {
				return err
			}
		}
		fileState.ETag, fileState.Error = version, ""
		if err := r.putState(ctx, fileState); err != nil {
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

// arenaFile is one file of a subset, as the Hub's tree listing gives it.
type arenaFile struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	OID  string `json:"oid"`
	LFS  *struct {
		OID string `json:"oid"`
	} `json:"lfs"`
}

// arenaFiles lists the subset's files of the configured split and a
// version string of their content hashes. fail is a sentence when the
// subset or its files are not what the metric map expects; err when the Hub
// could not be reached.
func (r *runner) arenaFiles(ctx context.Context, base string, src Source, subset string) (files []arenaFile, version, fail string, err error) {
	u := base + "/api/datasets/" + escapeRepo(src.Dataset) + "/tree/main/" + url.PathEscape(subset)
	resp, gerr := r.o.Fetcher.Get(ctx, u, Validators{})
	if gerr != nil {
		if ctx.Err() != nil {
			return nil, "", "", ctx.Err()
		}
		if errors.Is(gerr, ErrNotFound) {
			return nil, "", fmt.Sprintf("the dataset has no %q subset — check the metric map", subset), nil
		}
		if errors.Is(gerr, ErrUnreachable) || errors.Is(gerr, ErrRateLimited) || errors.Is(gerr, ErrHost) {
			return nil, "", "", errors.New(describe(gerr, "Hugging Face"))
		}
		return nil, "", describe(gerr, "Hugging Face"), nil
	}
	var all []arenaFile
	if err := json.Unmarshal(resp.Body, &all); err != nil {
		return nil, "", fmt.Sprintf("the list of the %s subset's files was not the shape the advisor reads", subset), nil
	}
	var parts []string
	for _, f := range all {
		name := path.Base(f.Path)
		if f.Type == "file" && strings.HasPrefix(name, src.Split+"-") && strings.HasSuffix(name, ".parquet") {
			oid := f.OID
			if f.LFS != nil && f.LFS.OID != "" {
				oid = f.LFS.OID
			}
			files = append(files, f)
			parts = append(parts, f.Path+"@"+oid)
		}
	}
	if len(files) == 0 {
		return nil, "", fmt.Sprintf("the %s subset has no %q file — check split in external.yaml", subset, src.Split), nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(parts)
	return files, strings.Join(parts, " "), "", nil
}

// arenaRows downloads a subset's files and reads the columns external.yaml
// maps from them.
func (r *runner) arenaRows(ctx context.Context, base string, src Source, files []arenaFile) (rows []map[string]any, fail string, err error) {
	for _, f := range files {
		if r.o.Fetcher.MaxBytes > 0 && f.Size > r.o.Fetcher.MaxBytes {
			return nil, fmt.Sprintf("%s is %d MB, more than the advisor reads", f.Path, f.Size>>20), nil
		}
		var segs []string
		for _, s := range strings.Split(f.Path, "/") {
			segs = append(segs, url.PathEscape(s))
		}
		u := base + "/datasets/" + escapeRepo(src.Dataset) + "/resolve/main/" + strings.Join(segs, "/")
		resp, gerr := r.o.Fetcher.Get(ctx, u, Validators{})
		if gerr != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			if errors.Is(gerr, ErrUnreachable) || errors.Is(gerr, ErrRateLimited) || errors.Is(gerr, ErrHost) {
				return nil, "", errors.New(describe(gerr, "Hugging Face"))
			}
			return nil, describe(gerr, "Hugging Face"), nil
		}
		pf, perr := parquet.Open(resp.Body)
		if perr != nil {
			return nil, fmt.Sprintf("%s could not be read (%v); nothing from it was used", f.Path, perr), nil
		}
		have := map[string]bool{}
		for _, c := range pf.Columns {
			have[c.Name] = true
		}
		var missing, want []string
		for _, k := range requiredColumns[SourceArena] {
			if !have[src.Columns[k]] {
				missing = append(missing, src.Columns[k])
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Sprintf("%s has no column %s — check columns in external.yaml", f.Path, strings.Join(missing, ", ")), nil
		}
		for _, c := range src.Columns {
			if have[c] {
				want = append(want, c)
			}
		}
		sort.Strings(want)
		got, perr := pf.Rows(slices.Compact(want)...)
		if perr != nil {
			return nil, fmt.Sprintf("%s could not be read (%v); nothing from it was used", f.Path, perr), nil
		}
		rows = append(rows, got...)
	}
	return rows, "", nil
}

// arenaBoard turns one category's rows into stored values, best rank first.
func (r *runner) arenaBoard(src Source, sr *SourceReport, m Metric, subset, category string, board []map[string]any, seenAlias, seenCand map[string]bool) []catalog.External {
	col := src.Columns
	if rc := col["rank"]; rc != "" {
		sort.SliceStable(board, func(i, j int) bool {
			a, okA := number(board[i][rc])
			b, okB := number(board[j][rc])
			return okA && (!okB || a < b)
		})
	}
	rows := []catalog.External{}
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
			continue // two rows for one size on one board: the better-ranked stands
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
	return rows
}

// number reads a JSON, CSV or Parquet number.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
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

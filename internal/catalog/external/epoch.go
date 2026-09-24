package external

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/figure"
	"advisor/internal/store"
)

// epoch is E-3: Epoch AI's benchmark data, one ZIP of CSVs
// (https://epoch.ai/data/benchmark_data.zip), read conditionally
// (If-None-Match / If-Modified-Since) and at most once per its cadence
// (weekly). Only Epoch's own runs are read: files whose names end in the
// configured external suffix are results Epoch collected from other
// projects, under those projects' terms, and are never opened. If the ZIP
// stops marking which files those are, the whole file is refused in words —
// the advisor would no longer know whose numbers it was reading.
func (r *runner) epoch(ctx context.Context, src Source, sr *SourceReport, conditional bool) error {
	st, err := r.state(ctx, src.ID, src.URL)
	if err != nil {
		return err
	}
	v := Validators{}
	if conditional {
		v = Validators{ETag: st.ETag, LastModified: st.LastModified}
	}
	r.part("downloading its data")
	resp, err := r.o.Fetcher.Get(ctx, src.URL, v)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New(describe(err, "Epoch AI"))
	}
	if resp.NotModified {
		sr.Status = StatusUnchanged
		return r.putState(ctx, st)
	}
	zr, err := zip.NewReader(bytes.NewReader(resp.Body), int64(len(resp.Body)))
	if err != nil {
		return fmt.Errorf("Epoch AI's download is not a ZIP file (%v); nothing from it was used", err)
	}
	files := map[string]*zip.File{}
	var own, external []string
	for _, f := range zr.File {
		name := path.Base(f.Name)
		if f.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(name), ".csv") {
			continue
		}
		files[name] = f
		if strings.HasSuffix(name, src.ExternalSuffix) {
			external = append(external, name)
		} else {
			own = append(own, name)
		}
	}
	sort.Strings(own)
	if len(external) == 0 {
		return fmt.Errorf("Epoch AI's ZIP no longer marks which results are other projects' (no file ends in %s), so none of it was read — check external_suffix in external.yaml against the file", src.ExternalSuffix)
	}
	// The dataset's own date: when Epoch last published the file. A run
	// that states its own date uses that instead.
	published := ""
	if t, err := http.ParseTime(resp.LastModified); err == nil {
		published = t.UTC().Format(time.DateOnly)
	}

	col := src.Columns
	seenAlias := map[string]bool{}
	seenCand := map[string]bool{}
	for _, m := range r.o.Config.MetricsOf(src.ID) {
		f, ok := files[m.File]
		if !ok {
			sr.Failures = append(sr.Failures, fmt.Sprintf("%s: the ZIP has no %s (Epoch's own files in it: %s) — check the metric map",
				m.Metric, m.File, strings.Join(firstN(own, 12), ", ")))
			continue
		}
		recs, fail := readCSV(f)
		if fail != "" {
			sr.Failures = append(sr.Failures, m.Metric+": "+fail)
			continue
		}
		idx := map[string]int{}
		for i, h := range recs[0] {
			idx[strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF"))] = i
		}
		var missing []string
		for _, k := range requiredColumns[SourceEpoch] {
			if _, ok := idx[col[k]]; !ok {
				missing = append(missing, fmt.Sprintf("%q", col[k]))
			}
		}
		if len(missing) > 0 {
			sr.Failures = append(sr.Failures, fmt.Sprintf("%s: %s has no column %s — check columns in external.yaml", m.Metric, m.File, strings.Join(missing, ", ")))
			continue
		}
		cell := func(rec []string, k string) string {
			i, ok := idx[col[k]]
			if !ok || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		best := map[int64]catalog.External{}
		var order []int64
		for _, rec := range recs[1:] {
			name := cell(rec, "model")
			if name == "" {
				continue
			}
			ref, ok := r.alias(src.ID, name)
			if !ok {
				if s := r.suggest(name); s != "" {
					addCandidate(sr, seenCand, Candidate{Name: name, Suggest: s})
				}
				continue
			}
			seenAlias[name] = true
			raw := cell(rec, "value")
			value, ok := number(raw)
			if !ok {
				sr.Unreadable = append(sr.Unreadable, fmt.Sprintf("%s: %s's score %q is not a number", m.Metric, name, raw))
				continue
			}
			date, basis := day(cell(rec, "date")), "the run's own date"
			if date == "" {
				date, basis = published, "the date Epoch last published its data (the run states none)"
			}
			if date == "" {
				sr.Unreadable = append(sr.Unreadable, fmt.Sprintf("%s: %s's run has no readable date, and the download states none", m.Metric, name))
				continue
			}
			row := catalog.External{
				Source: src.ID, SourceModel: name, ModelID: ref.ID, Metric: m.Metric, Value: value, ValueText: raw,
				SourceDate: date, SourceURL: "https://epoch.ai/benchmarks", Provenance: string(figure.ProvenanceIndependent),
				Attribution: fill(src.Attribution, map[string]string{"accessed": r.now.Format("2 January 2006"), "date": date}),
				License:     src.Licence, Detail: map[string]any{"file": m.File, "date_basis": basis}, FetchedAt: r.now.Format(time.RFC3339),
			}
			prev, seen := best[ref.ID]
			switch {
			case !seen:
				order = append(order, ref.ID)
				best[ref.ID] = row
			case row.SourceDate >= prev.SourceDate:
				best[ref.ID] = row // several runs of one model: the latest stands
			}
		}
		rows := make([]catalog.External, 0, len(order))
		for _, id := range order {
			rows = append(rows, best[id])
		}
		if err := r.replace(ctx, sr, store.ExternalScope{Source: src.ID, Metric: m.Metric}, rows); err != nil {
			return err
		}
	}
	sr.AliasesNotSeen = r.notSeen(src.ID, seenAlias)
	st.ETag, st.LastModified, st.Error = resp.ETag, resp.LastModified, ""
	return r.putState(ctx, st)
}

// readCSV reads one CSV from the ZIP: a header row and at least one record.
func readCSV(f *zip.File) ([][]string, string) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Sprintf("%s could not be opened (%v)", f.Name, err)
	}
	defer rc.Close()
	cr := csv.NewReader(io.LimitReader(rc, 32<<20))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	recs, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Sprintf("%s is not a CSV the advisor can read (%v)", f.Name, err)
	}
	if len(recs) < 2 {
		return nil, fmt.Sprintf("%s has no rows", f.Name)
	}
	return recs, ""
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string{}, s[:n]...), "and "+strconv.Itoa(len(s)-n)+" more")
}

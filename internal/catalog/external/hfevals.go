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

// hfEvals is E-1: for each curated size, the original model's repo
// (hf_base_repo) with its eval results, card and creation date —
//
//	GET /api/models/{owner}/{name}?expand=evalResults&expand=cardData&expand=createdAt&expand=lastModified
//
// the query huggingface_hub 1.32.0's model_info(expand=[...]) sends (a
// repeated "expand", not "expand[]"), conditional on the last ETag. It keeps
// results merged into the repo (the maker's, labelled so) and verified ones;
// skips results in open pull requests (counted: the curator sees what is
// pending); drops any whose stated source is an excluded publisher; and
// stores only the metrics the map lists — per size, never copied to another.
func (r *runner) hfEvals(ctx context.Context, src Source, sr *SourceReport, conditional bool) error {
	only := map[string]bool{}
	for _, f := range r.o.Only {
		only[f] = true
	}
	unknown := map[string]bool{}
	read := 0
	for _, s := range r.sizes {
		if len(only) > 0 && !only[s.Family.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		r.checkBaseModel(ctx, s)
		u := strings.TrimRight(src.URL, "/") + "/api/models/" + escapeRepo(s.Size.HFBaseRepo) +
			"?expand=evalResults&expand=cardData&expand=createdAt&expand=lastModified"
		st, err := r.state(ctx, src.ID, u)
		if err != nil {
			return err
		}
		v := Validators{}
		if conditional {
			v.ETag = st.ETag
		}
		resp, err := r.o.Fetcher.Get(ctx, u, v)
		if err != nil {
			if errors.Is(err, ErrUnreachable) || errors.Is(err, ErrHost) {
				return errors.New(describe(err, "Hugging Face"))
			}
			msg := fmt.Sprintf("%s: %s", s.label(), describe(err, "Hugging Face"))
			if errors.Is(err, ErrNotFound) {
				msg = fmt.Sprintf("%s: Hugging Face has no repo named %s — check hf_base_repo in families.yaml", s.label(), s.Size.HFBaseRepo)
			}
			sr.Failures = append(sr.Failures, msg)
			st.Error = msg
			if err := r.putState(ctx, st); err != nil {
				return err
			}
			continue
		}
		st.Error = ""
		if resp.NotModified {
			if err := r.putState(ctx, st); err != nil {
				return err
			}
			continue
		}
		read++
		rows, released, err := r.parseHFModel(src, s, resp.Body, sr, unknown)
		if err != nil {
			msg := fmt.Sprintf("%s: the answer for %s was not the shape the advisor reads (%v); what was stored before is kept", s.label(), s.Size.HFBaseRepo, err)
			sr.Failures = append(sr.Failures, msg)
			st.Error = msg
			if err := r.putState(ctx, st); err != nil {
				return err
			}
			continue
		}
		if err := r.replace(ctx, sr, store.ExternalScope{Source: src.ID, SourceModel: s.Size.HFBaseRepo}, rows); err != nil {
			return err
		}
		if released != "" {
			if err := r.o.Store.SetReleasedAt(ctx, s.ID, released); err != nil {
				return &storeError{err}
			}
		}
		st.ETag = resp.ETag
		if err := r.putState(ctx, st); err != nil {
			return err
		}
	}
	for m := range unknown {
		sr.UnknownMetrics = append(sr.UnknownMetrics, m)
	}
	sort.Strings(sr.UnknownMetrics)
	if read == 0 && len(sr.Failures) == 0 {
		sr.Status = StatusUnchanged
	}
	return nil
}

// hfModel is the part of the Hub's model-info answer E-1 reads.
type hfModel struct {
	ID           string            `json:"id"`
	CreatedAt    string            `json:"createdAt"`
	LastModified string            `json:"lastModified"`
	CardData     map[string]any    `json:"cardData"`
	EvalResults  []json.RawMessage `json:"evalResults"`
}

// parseHFModel turns one repo's answer into stored values. An entry the
// parser cannot read is counted as unreadable, never guessed at; the answer
// as a whole fails only when it is not the model-info JSON at all.
func (r *runner) parseHFModel(src Source, s sizeRef, body []byte, sr *SourceReport, unknown map[string]bool) ([]catalog.External, string, error) {
	var m hfModel
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, "", err
	}
	if m.ID == "" {
		return nil, "", errors.New("no model id in the answer")
	}
	repo := s.Size.HFBaseRepo
	licence := "HF-ToS"
	if l := cardLicence(m.CardData); l != "" {
		licence += "; repo:" + l
	}
	best := map[string]catalog.External{}
	var order []string
	for i, raw := range m.EvalResults {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			sr.Unreadable = append(sr.Unreadable, fmt.Sprintf("%s: eval result %d is not an object", repo, i))
			continue
		}
		e, ok, why := hfEntry(item)
		if !ok {
			sr.Unreadable = append(sr.Unreadable, fmt.Sprintf("%s: eval result %d: %s", repo, i, why))
			continue
		}
		if e.pending {
			sr.Pending++
			continue
		}
		if e.sourceURL != "" {
			if u, err := url.Parse(e.sourceURL); err == nil && r.o.Config.Excluded(u.Hostname()) {
				sr.Dropped = append(sr.Dropped, fmt.Sprintf("%s: %s (its source is %s, an excluded publisher)", repo, e.metric, u.Hostname()))
				continue
			}
		}
		if _, ok := r.o.Config.Metric(src.ID, e.metric); !ok {
			unknown[e.metric] = true
			continue
		}
		prov, attribution := figure.ProvenanceMaker, fill(src.Attribution, map[string]string{"maker": s.Family.Maintainer})
		if e.verified {
			prov, attribution = figure.ProvenanceVerified, "Verified on Hugging Face"
		}
		date, basis := day(e.date), "the result's own date"
		if date == "" {
			date, basis = day(m.LastModified), "the repo's last change (the result states no date)"
		}
		if date == "" {
			sr.Unreadable = append(sr.Unreadable, fmt.Sprintf("%s: %s states no date and the repo's last change is unreadable", repo, e.metric))
			continue
		}
		sourceURL := e.sourceURL
		if sourceURL == "" {
			sourceURL = "https://huggingface.co/" + repo
		}
		detail := map[string]any{"dataset": e.dataset, "task": e.task, "date_basis": basis, "maker": s.Family.Maintainer}
		if e.notes != "" {
			detail["notes"] = e.notes
		}
		if e.sourceName != "" {
			detail["source_name"] = e.sourceName
		}
		if e.filename != "" {
			detail["file"] = e.filename
		}
		row := catalog.External{
			Source: src.ID, SourceModel: repo, ModelID: s.ID, Metric: e.metric, Value: e.value, ValueText: e.valueText,
			SourceDate: date, SourceURL: sourceURL, Provenance: string(prov), Attribution: attribution,
			License: licence, Detail: detail, FetchedAt: r.now.Format(time.RFC3339),
		}
		// One value per metric per size: a verified run over the maker's own
		// report, then the later date.
		if prev, ok := best[e.metric]; ok {
			n := 1
			if c, ok := prev.Detail["entries"].(int); ok {
				n = c
			}
			keep := prev
			if (row.Provenance == string(figure.ProvenanceVerified)) != (prev.Provenance == string(figure.ProvenanceVerified)) {
				if row.Provenance == string(figure.ProvenanceVerified) {
					keep = row
				}
			} else if row.SourceDate > prev.SourceDate {
				keep = row
			}
			keep.Detail["entries"] = n + 1
			best[e.metric] = keep
			continue
		}
		best[e.metric] = row
		order = append(order, e.metric)
	}
	out := make([]catalog.External, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out, day(m.CreatedAt), nil
}

// hfEntryFields is one eval result, as far as E-1 reads it.
type hfEntryFields struct {
	metric, dataset, task string
	value                 float64
	valueText             string
	date                  any
	sourceURL, sourceName string
	notes, filename       string
	verified, pending     bool
}

// hfEntry reads one item of evalResults. The Hub serves either the YAML
// entry itself ({"dataset": {"id", "task_id"}, "value", "date", "source":
// {"url", ...}, "verifyToken", "notes"}) or that entry under "data" beside
// the Hub's own fields ("filename", "verified", "pullRequest") — the two
// shapes huggingface_hub 1.32.0's parse_eval_result_entries accepts.
func hfEntry(item map[string]any) (hfEntryFields, bool, string) {
	var e hfEntryFields
	d := item
	if inner, ok := item["data"].(map[string]any); ok {
		d = inner
	}
	e.filename, _ = item["filename"].(string)
	e.verified, _ = item["verified"].(bool)
	switch pr := item["pullRequest"].(type) {
	case nil:
	case bool:
		e.pending = pr
	case float64:
		e.pending = pr > 0
	case string:
		e.pending = pr != ""
	default:
		e.pending = true
	}
	ds, _ := d["dataset"].(map[string]any)
	e.dataset, _ = ds["id"].(string)
	e.task, _ = ds["task_id"].(string)
	if e.dataset == "" || e.task == "" {
		return e, false, "no dataset id or task id"
	}
	e.metric = "hf:" + e.dataset + "/" + e.task
	switch v := d["value"].(type) {
	case float64:
		e.value, e.valueText = v, strconv.FormatFloat(v, 'f', -1, 64)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(v), "%"), 64)
		if err != nil {
			return e, false, fmt.Sprintf("%s: the value %q is not a number", e.metric, v)
		}
		e.value, e.valueText = f, v
	default:
		return e, false, fmt.Sprintf("%s: no numeric value", e.metric)
	}
	e.date = d["date"]
	if s, ok := d["source"].(map[string]any); ok {
		e.sourceURL, _ = s["url"].(string)
		e.sourceName, _ = s["name"].(string)
	}
	e.notes, _ = d["notes"].(string)
	return e, true, ""
}

// cardLicence reads a model card's licence: a string or a list of them.
func cardLicence(card map[string]any) string {
	switch l := card["license"].(type) {
	case string:
		return l
	case []any:
		var parts []string
		for _, x := range l {
			if s, ok := x.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

// checkBaseModel compares the GGUF repo's own card (from the listing the
// catalogue refresh cached) with hf_base_repo, and warns when they disagree:
// the scores would be another model's.
func (r *runner) checkBaseModel(ctx context.Context, s sizeRef) {
	_, body, ok, err := r.o.Store.CachedListing(ctx, s.Size.HFRepo)
	if err != nil || !ok {
		return
	}
	var listing struct {
		CardData map[string]any `json:"cardData"`
	}
	if json.Unmarshal(body, &listing) != nil {
		return
	}
	var bases []string
	switch b := listing.CardData["base_model"].(type) {
	case string:
		bases = []string{b}
	case []any:
		for _, x := range b {
			if s, ok := x.(string); ok {
				bases = append(bases, s)
			}
		}
	}
	if len(bases) == 0 {
		return
	}
	for _, b := range bases {
		if strings.EqualFold(b, s.Size.HFBaseRepo) {
			return
		}
	}
	r.report.Warnings = append(r.report.Warnings, fmt.Sprintf("%s: the GGUF repo %s names base_model %s, but families.yaml's hf_base_repo is %s — the public scores would be another model's",
		s.label(), s.Size.HFRepo, strings.Join(bases, ", "), s.Size.HFBaseRepo))
}

// escapeRepo escapes each path element of "owner/name".
func escapeRepo(repo string) string {
	owner, name, _ := strings.Cut(repo, "/")
	return url.PathEscape(owner) + "/" + url.PathEscape(name)
}

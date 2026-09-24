package external

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/figure"
	"advisor/internal/store"
)

// View is the read side of the stored public values: what a size's public
// block shows, the one line a recommendation card carries, and what the
// recommendation engine may score. Only values that are present, from an
// enabled source, on a metric the map lists, about a size still in the
// catalogue, are in it.
type View struct {
	cfg      *Config
	states   map[string]store.ExternalState
	byModel  map[int64][]catalog.External
	byMetric map[string][]catalog.External // "source|metric"
}

// NewView builds the view over values (store.ExternalValues), the present
// catalogue sizes' ids, and the sources' run states.
func NewView(cfg *Config, values []catalog.External, present map[int64]bool, states map[string]store.ExternalState) *View {
	v := &View{cfg: cfg, states: states, byModel: map[int64][]catalog.External{}, byMetric: map[string][]catalog.External{}}
	for _, e := range values {
		src, ok := cfg.Source(e.Source)
		if !ok || !src.Enabled || !e.Present || !present[e.ModelID] {
			continue
		}
		if _, ok := cfg.Metric(e.Source, e.Metric); !ok {
			continue
		}
		v.byModel[e.ModelID] = append(v.byModel[e.ModelID], e)
		k := e.Source + "|" + e.Metric
		v.byMetric[k] = append(v.byMetric[k], e)
	}
	return v
}

// comparable puts a value on one scale for positions: a percent metric
// published as a fraction (0.817) reads as 81.7.
func comparable(m Metric, v float64) float64 {
	if m.Scale == ScalePercent && v <= 1 {
		return v * 100
	}
	return v
}

// Scoring is what the recommendation engine may read: per purpose, every
// (source, metric) pair the map assigns to it, with the scorable values
// (verified, independent, crowd — never the maker's own) of the present
// sizes. The engine decides which pairs cover enough sizes to count.
func (v *View) Scoring() map[catalog.Purpose][]catalog.PublicMetric {
	out := map[catalog.Purpose][]catalog.PublicMetric{}
	for _, m := range v.cfg.Metrics {
		vals := map[int64]float64{}
		for _, e := range v.byMetric[m.Source+"|"+m.Metric] {
			if figure.Provenance(e.Provenance).Scorable() {
				vals[e.ModelID] = comparable(m, e.Value)
			}
		}
		if len(vals) == 0 {
			continue
		}
		pm := catalog.PublicMetric{Source: m.Source, Metric: m.Metric, HigherIsBetter: m.HigherIsBetter, Values: vals}
		for _, p := range m.Purposes {
			out[p] = append(out[p], pm)
		}
	}
	return out
}

// Entries is a size's public block, in the metric map's order.
func (v *View) Entries(modelID int64) []catalog.PublicEntry {
	var out []catalog.PublicEntry
	for _, m := range v.cfg.Metrics {
		for _, e := range v.byModel[modelID] {
			if e.Source == m.Source && e.Metric == m.Metric {
				out = append(out, v.entry(m, e))
			}
		}
	}
	return out
}

// Line is the one public line a recommendation card carries (P-3): the
// value that speaks to the first purpose asked for that it can, scorable
// before the maker's own, then the one most sizes are compared on. Nil when
// the size has no public value for any purpose asked — the card then says
// nothing public (P-7).
func (v *View) Line(modelID int64, purposes []catalog.Purpose) *catalog.PublicEntry {
	entries := v.Entries(modelID)
	for _, p := range purposes {
		var best *catalog.PublicEntry
		for i := range entries {
			e := &entries[i]
			if !hasPurpose(e.Purposes, p) {
				continue
			}
			if best == nil || (e.Scored && !best.Scored) || (e.Scored == best.Scored && e.Rated > best.Rated) {
				best = e
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

// entry builds one PublicEntry.
func (v *View) entry(m Metric, e catalog.External) catalog.PublicEntry {
	src, _ := v.cfg.Source(e.Source)
	peers := v.byMetric[m.Source+"|"+m.Metric]
	mine := comparable(m, e.Value)
	rank := 1
	for _, p := range peers {
		o := comparable(m, p.Value)
		if (m.HigherIsBetter && o > mine) || (!m.HigherIsBetter && o < mine) {
			rank++
		}
	}
	prov := figure.Provenance(e.Provenance)
	out := catalog.PublicEntry{
		SourceID: src.ID, SourceName: src.Name, Metric: m.Metric, Tests: m.Tests, Purposes: m.Purposes,
		Position:        position(src.ID, m.Purposes[0], rank, len(peers)),
		ProvenanceWords: provenanceWords(prov, src),
		Rated:           len(peers), Rank: rank, Scored: prov.Scorable(),
		Value: figure.Public{Value: e.Value, Scale: m.Scale, Origin: figure.Origin{
			Publisher: publisherOf(src, e), URL: e.SourceURL, Date: e.SourceDate, Licence: e.License,
			Attribution: e.Attribution, Provenance: prov,
		}},
		Fetched: e.FetchedAt,
	}
	out.Detail = detail(m, e)
	return out
}

// publisherOf names who published the value: for Hugging Face results the
// maker (or Hugging Face, for a verified run), otherwise the source's
// publisher.
func publisherOf(src Source, e catalog.External) string {
	if src.ID == SourceHFEvals {
		if e.Provenance == string(figure.ProvenanceVerified) {
			return "Hugging Face"
		}
		if maker, ok := e.Detail["maker"].(string); ok && maker != "" {
			return maker + " on Hugging Face"
		}
		if owner, _, ok := strings.Cut(e.SourceModel, "/"); ok {
			return owner + " on Hugging Face"
		}
	}
	return src.Publisher
}

// provenanceWords is P-4's provenance, in words.
func provenanceWords(p figure.Provenance, src Source) string {
	switch p {
	case figure.ProvenanceMaker:
		return "reported by the model's maker"
	case figure.ProvenanceVerified:
		return "verified by Hugging Face"
	case figure.ProvenanceIndependent:
		return "tested by " + src.Publisher
	case figure.ProvenanceCrowd:
		return "rated by people comparing answers on " + strings.TrimSuffix(strings.SplitN(src.Publisher, " (", 2)[0], " ")
	}
	return ""
}

// raters is how a sentence names the sizes a source has a value for; hasBeen
// is the same for a sentence about no other size.
func raters(sourceID string) (that, hasBeen string) {
	switch sourceID {
	case SourceArena:
		return "that Arena has rated", "has been rated by Arena"
	case SourceEpoch:
		return "that Epoch AI has tested", "has been tested by Epoch AI"
	}
	return "with this score on Hugging Face", "has this score on Hugging Face"
}

var purposeWords = map[catalog.Purpose]string{
	catalog.PurposeCoding:      "for coding",
	catalog.PurposeChat:        "for everyday chat",
	catalog.PurposeReasoning:   "for reasoning",
	catalog.PurposeLongContext: "with long documents",
	catalog.PurposeVision:      "at reading pictures",
	catalog.PurposeAgentic:     "for agent tasks",
	catalog.PurposeWriting:     "for writing",
}

// position is P-5's sentence: thirds when six or more sizes are scored,
// otherwise the place itself.
func position(sourceID string, p catalog.Purpose, rank, n int) string {
	who, hasBeen := raters(sourceID)
	what := purposeWords[p]
	switch {
	case n <= 1:
		return fmt.Sprintf("No other model here %s yet, so it cannot be ranked %s.", hasBeen, what)
	case n < 6:
		return fmt.Sprintf("%s %s of the %d models here %s.", capitalise(ordinal(rank)), what, n, who)
	}
	third := "Among the weaker"
	switch {
	case rank <= int(math.Ceil(float64(n)/3)):
		third = "Among the strongest"
	case rank <= int(math.Ceil(2*float64(n)/3)):
		third = "In the middle"
	}
	return fmt.Sprintf("%s %s of the %d models here %s.", third, what, n, who)
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// detail is the Advanced view of one value, in words.
func detail(m Metric, e catalog.External) []catalog.PublicDetail {
	var out []catalog.PublicDetail
	add := func(label, text string) {
		if text != "" {
			out = append(out, catalog.PublicDetail{Label: label, Text: text})
		}
	}
	scale := map[string]string{ScaleRating: "a relative rating: only its position among other models means anything", ScalePercent: "percent of questions answered correctly"}[m.Scale]
	value := e.ValueText
	if value == "" {
		value = strconv.FormatFloat(e.Value, 'f', -1, 64)
	}
	add("Value as published", value+" ("+scale+")")
	num := func(k string) (float64, bool) {
		f, ok := e.Detail[k].(float64)
		return f, ok
	}
	if lo, ok := num("lower"); ok {
		if hi, ok := num("upper"); ok {
			add("Uncertainty", fmt.Sprintf("between %s and %s", trim(lo), trim(hi)))
		}
	}
	if votes, ok := num("votes"); ok {
		add("Votes", trim(votes))
	}
	if rank, ok := num("rank"); ok {
		if rows, ok := num("board_rows"); ok {
			add("Place on the whole board", fmt.Sprintf("%s of %s", ordinal(int(rank)), trim(rows)))
		}
	}
	add("Benchmark", m.Metric)
	add("Provenance", e.Provenance)
	if s, ok := e.Detail["notes"].(string); ok {
		add("Notes", s)
	}
	if s, ok := e.Detail["date_basis"].(string); ok {
		add("Date is", s)
	}
	add("Model name at the source", e.SourceModel)
	if t, err := time.Parse(time.RFC3339, e.FetchedAt); err == nil {
		add("Read by the advisor", t.Format("2 January 2006"))
	}
	return out
}

func trim(f float64) string {
	if f == math.Trunc(f) {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return strconv.FormatFloat(f, 'f', 1, 64)
}

func hasPurpose(ps []catalog.Purpose, p catalog.Purpose) bool {
	for _, q := range ps {
		if q == p {
			return true
		}
	}
	return false
}

// Updated is the "public scores last updated" sentence (the note's failure
// rule): the last successful read among the enabled sources, and any source
// whose latest check failed, by name, with its reason in words.
func (v *View) Updated() string {
	var last time.Time
	var failed []string
	names := make([]string, 0, len(v.cfg.Sources))
	for _, src := range v.cfg.Sources {
		if !src.Enabled {
			continue
		}
		names = append(names, src.ID)
		st, ok := v.states[src.ID]
		if !ok {
			continue
		}
		if t, err := time.Parse(time.RFC3339, st.OKAt); err == nil && t.After(last) {
			last = t
		}
		if st.Error != "" {
			failed = append(failed, fmt.Sprintf(" The latest check of %s failed: %s.", src.Name, strings.TrimSuffix(st.Error, ".")))
		}
	}
	sort.Strings(failed)
	out := "Public scores have not been fetched yet."
	if !last.IsZero() {
		out = "Public scores last updated " + last.Format("2 January 2006") + "."
	}
	return out + strings.Join(failed, "")
}

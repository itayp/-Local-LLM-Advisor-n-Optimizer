package external

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"advisor/data"
	"advisor/internal/catalog"
)

// Source ids: one client each (hfevals.go, arena.go, epoch.go). A source in
// external.yaml whose id is not one of these is refused.
const (
	SourceHFEvals = "hf_evals"
	SourceArena   = "arena"
	SourceEpoch   = "epoch"
)

// PermittedHosts is the ceiling on where each client may connect: the hosts
// research/EXTERNAL_SOURCES.md approved (ARCHITECTURE.md D-10, D-53).
// external.yaml names the hosts a source uses and cannot name one that is
// not here, so switching on a new host is a code review, not a data edit.
// Step 12's allow-list audit reads this map beside hf.allowedHost.
var PermittedHosts = map[string][]string{
	SourceHFEvals: {"huggingface.co"},
	SourceArena:   {"datasets-server.huggingface.co"},
	SourceEpoch:   {"epoch.ai"},
}

// Config is data/catalog/external.yaml: the approved sources, the publishers
// never ingested, and the metric → purpose map.
type Config struct {
	Sources       []Source `yaml:"sources"`
	ExcludedHosts []string `yaml:"excluded_hosts"`
	Metrics       []Metric `yaml:"metrics"`
}

// Source is one approved source.
type Source struct {
	ID             string            `yaml:"id"`
	Name           string            `yaml:"name"`
	Publisher      string            `yaml:"publisher"`
	Enabled        bool              `yaml:"enabled"`
	URL            string            `yaml:"url"`
	Hosts          []string          `yaml:"hosts"`
	TermsURL       string            `yaml:"terms_url"`
	TermsChecked   string            `yaml:"terms_checked"`
	Licence        string            `yaml:"licence"`
	LicenceURL     string            `yaml:"licence_url"`
	Attribution    string            `yaml:"attribution"`
	CadenceHours   int               `yaml:"cadence_hours"`
	Dataset        string            `yaml:"dataset,omitempty"`
	Split          string            `yaml:"split,omitempty"`
	ExternalSuffix string            `yaml:"external_suffix,omitempty"`
	Columns        map[string]string `yaml:"columns,omitempty"`
}

// Metric is one entry of the metric → purpose map.
type Metric struct {
	Source         string            `yaml:"source"`
	Metric         string            `yaml:"metric"`
	Purposes       []catalog.Purpose `yaml:"purposes"`
	HigherIsBetter bool              `yaml:"higher_is_better"`
	Tests          string            `yaml:"tests"`
	Scale          string            `yaml:"scale"`
	File           string            `yaml:"file,omitempty"`
	Note           string            `yaml:"note,omitempty"`
}

// Scales a metric's values can be on.
const (
	ScaleRating  = "rating"  // a relative rating (Arena): only positions mean anything
	ScalePercent = "percent" // a share of questions right; a published fraction ≤ 1 is read ×100 when compared
)

// Aliases is data/catalog/aliases.yaml: per source, its exact model names
// for catalogue sizes.
type Aliases map[string][]Alias

// Alias maps one source model name to one catalogue size.
type Alias struct {
	Name       string `yaml:"name"`
	Family     string `yaml:"family"`
	Parameters uint64 `yaml:"parameters"`
	ReviewedAt string `yaml:"reviewed_at"`
	Note       string `yaml:"note,omitempty"`
}

// requiredColumns are the columns a client cannot work without; the others
// in the file are read when present.
var requiredColumns = map[string][]string{
	SourceArena: {"model", "value", "category", "date"},
	SourceEpoch: {"model", "value"},
}

// knownColumns are every column name a client reads.
var knownColumns = map[string][]string{
	SourceArena: {"model", "licence", "value", "lower", "upper", "votes", "rank", "category", "date"},
	SourceEpoch: {"model", "value", "date"},
}

// DefaultConfig loads the embedded external.yaml.
func DefaultConfig() (*Config, error) {
	b, err := fs.ReadFile(data.Files, data.ExternalPath)
	if err != nil {
		return nil, fmt.Errorf("external: reading the embedded %s: %w", data.ExternalPath, err)
	}
	return ParseConfig(b)
}

// DefaultAliases loads the embedded aliases.yaml.
func DefaultAliases() (Aliases, error) {
	b, err := fs.ReadFile(data.Files, data.AliasesPath)
	if err != nil {
		return nil, fmt.Errorf("external: reading the embedded %s: %w", data.AliasesPath, err)
	}
	return ParseAliases(b)
}

// ErrInvalid is wrapped by every validation error.
var ErrInvalid = errors.New("external: invalid configuration")

// ValidationError lists everything wrong with a file.
type ValidationError struct {
	File     string
	Problems []string
}

func (e *ValidationError) Error() string {
	return "external: " + e.File + " is not valid:\n  " + strings.Join(e.Problems, "\n  ")
}

func (e *ValidationError) Unwrap() error { return ErrInvalid }

// ParseConfig decodes external.yaml strictly and validates it.
func ParseConfig(b []byte) (*Config, error) {
	var c Config
	if err := yaml.NewDecoder(bytes.NewReader(b), yaml.DisallowUnknownField()).Decode(&c); err != nil {
		return nil, fmt.Errorf("external: external.yaml: %w", err)
	}
	if p := c.Validate(); len(p) > 0 {
		return nil, &ValidationError{File: "external.yaml", Problems: p}
	}
	return &c, nil
}

// ParseAliases decodes aliases.yaml strictly. It is validated against the
// catalogue and the config by Aliases.Validate.
func ParseAliases(b []byte) (Aliases, error) {
	a := Aliases{}
	if err := yaml.NewDecoder(bytes.NewReader(b), yaml.DisallowUnknownField()).Decode(&a); err != nil {
		return nil, fmt.Errorf("external: aliases.yaml: %w", err)
	}
	return a, nil
}

// Validate returns every problem with c, in file order.
func (c *Config) Validate() []string {
	var out []string
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	seen := map[string]bool{}
	for i, s := range c.Sources {
		where := fmt.Sprintf("sources[%d]", i)
		if s.ID != "" {
			where = fmt.Sprintf("source %q", s.ID)
		}
		permitted, known := PermittedHosts[s.ID]
		switch {
		case !known:
			bad("%s: id must be one of the sources the code has a client for (%s)", where, strings.Join(sourceIDs(), ", "))
		case seen[s.ID]:
			bad("%s: listed twice", where)
		}
		seen[s.ID] = true
		for field, v := range map[string]string{"name": s.Name, "publisher": s.Publisher, "licence": s.Licence, "attribution": s.Attribution} {
			if strings.TrimSpace(v) == "" {
				bad("%s: %s is missing", where, field)
			}
		}
		u, err := url.Parse(s.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			bad("%s: url must be an https URL", where)
		} else if !contains(s.Hosts, u.Hostname()) {
			bad("%s: url's host %s is not in its hosts", where, u.Hostname())
		}
		if len(s.Hosts) == 0 {
			bad("%s: hosts is empty", where)
		}
		for _, h := range s.Hosts {
			if known && !contains(permitted, h) {
				bad("%s: host %s is not one the code permits for this source (external.PermittedHosts: %v)", where, h, permitted)
			}
		}
		for field, v := range map[string]string{"terms_url": s.TermsURL, "licence_url": s.LicenceURL} {
			if !strings.HasPrefix(v, "https://") {
				bad("%s: %s must be an https URL", where, field)
			}
		}
		if _, err := time.Parse(time.DateOnly, s.TermsChecked); err != nil {
			bad("%s: terms_checked %q is not a YYYY-MM-DD date", where, s.TermsChecked)
		}
		if s.CadenceHours < 1 {
			bad("%s: cadence_hours must be at least 1", where)
		}
		switch s.ID {
		case SourceArena:
			if s.Dataset == "" || !strings.Contains(s.Dataset, "/") {
				bad("%s: dataset must be an owner/name Hugging Face dataset", where)
			}
			if s.Split == "" {
				bad("%s: split is missing", where)
			}
		case SourceEpoch:
			if !strings.HasSuffix(s.ExternalSuffix, ".csv") {
				bad("%s: external_suffix must name how Epoch marks other people's result files (it ends in .csv)", where)
			}
		}
		for _, col := range requiredColumns[s.ID] {
			if strings.TrimSpace(s.Columns[col]) == "" {
				bad("%s: columns.%s is missing", where, col)
			}
		}
		for col := range s.Columns {
			if !contains(knownColumns[s.ID], col) {
				bad("%s: columns.%s is not a column this source's client reads (%v)", where, col, knownColumns[s.ID])
			}
		}
	}
	for _, h := range c.ExcludedHosts {
		if h == "" || strings.ContainsAny(h, "/: ") {
			bad("excluded_hosts: %q is not a host name", h)
		}
	}
	seenMetric := map[string]bool{}
	for i, m := range c.Metrics {
		where := fmt.Sprintf("metrics[%d] %s", i, m.Metric)
		src, ok := c.Source(m.Source)
		if !ok {
			bad("%s: source %q is not in sources", where, m.Source)
		}
		prefix := map[string]string{SourceHFEvals: "hf:", SourceArena: "arena:", SourceEpoch: "epoch:"}[m.Source]
		switch {
		case prefix != "" && !strings.HasPrefix(m.Metric, prefix):
			bad("%s: a %s metric is named %s...", where, m.Source, prefix)
		case seenMetric[m.Metric]:
			bad("%s: listed twice", where)
		}
		seenMetric[m.Metric] = true
		if m.Source == SourceArena {
			if sub, cat, ok := strings.Cut(strings.TrimPrefix(m.Metric, "arena:"), "/"); !ok || sub == "" || cat == "" {
				bad("%s: an Arena metric is arena:<subset>/<category>", where)
			}
		}
		if m.Source == SourceHFEvals && strings.Count(strings.TrimPrefix(m.Metric, "hf:"), "/") < 2 {
			bad("%s: a Hugging Face metric is hf:<owner>/<dataset>/<task id>", where)
		}
		if len(m.Purposes) == 0 {
			bad("%s: purposes is empty", where)
		}
		for _, p := range m.Purposes {
			if !p.Valid() {
				bad("%s: purpose %q is not one of %v", where, p, catalog.Purposes)
			}
		}
		if strings.TrimSpace(m.Tests) == "" {
			bad("%s: tests (what it tested, in plain words) is missing", where)
		}
		if m.Scale != ScaleRating && m.Scale != ScalePercent {
			bad("%s: scale must be %s or %s", where, ScaleRating, ScalePercent)
		}
		if m.Source == SourceEpoch {
			switch {
			case !strings.HasSuffix(m.File, ".csv"):
				bad("%s: file must name the CSV in Epoch's ZIP", where)
			case ok && src.ExternalSuffix != "" && strings.HasSuffix(m.File, src.ExternalSuffix):
				bad("%s: file %s is one of the results Epoch collected from others; only Epoch's own runs are read", where, m.File)
			}
		} else if m.File != "" {
			bad("%s: file is only for Epoch metrics", where)
		}
	}
	return out
}

// Validate checks aliases against the catalogue and the config: every source
// is an approved one that reads aliases, every name maps to a size that
// exists, and no name is listed twice for a source.
func (a Aliases) Validate(cat *catalog.Catalogue, cfg *Config) []string {
	var out []string
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	sources := make([]string, 0, len(a))
	for s := range a {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	for _, src := range sources {
		switch {
		case src == SourceHFEvals:
			bad("aliases: %s needs no aliases (it is read from each size's hf_base_repo)", src)
			continue
		case cfg != nil:
			if _, ok := cfg.Source(src); !ok {
				bad("aliases: %s is not a source in external.yaml", src)
				continue
			}
		}
		seen := map[string]bool{}
		for _, al := range a[src] {
			where := fmt.Sprintf("aliases %s %q", src, al.Name)
			if strings.TrimSpace(al.Name) == "" {
				bad("aliases %s: a name is empty", src)
			}
			if seen[al.Name] {
				bad("%s: listed twice", where)
			}
			seen[al.Name] = true
			if _, err := time.Parse(time.DateOnly, al.ReviewedAt); err != nil {
				bad("%s: reviewed_at %q is not a YYYY-MM-DD date", where, al.ReviewedAt)
			}
			if cat == nil {
				continue
			}
			fam, ok := cat.Family(al.Family)
			if !ok {
				bad("%s: family %q is not in families.yaml", where, al.Family)
				continue
			}
			found := false
			for _, sz := range fam.Sizes {
				if sz.Parameters == al.Parameters {
					found = true
				}
			}
			if !found {
				bad("%s: %s has no size of %d parameters in families.yaml", where, al.Family, al.Parameters)
			}
		}
	}
	return out
}

// Check validates the pair together; the error is a *ValidationError.
func Check(cat *catalog.Catalogue, cfg *Config, a Aliases) error {
	if p := a.Validate(cat, cfg); len(p) > 0 {
		return &ValidationError{File: "aliases.yaml", Problems: p}
	}
	return nil
}

// Source returns the source with id.
func (c *Config) Source(id string) (Source, bool) {
	for _, s := range c.Sources {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}

// Metric returns the map entry for (source, metric).
func (c *Config) Metric(source, metric string) (Metric, bool) {
	for _, m := range c.Metrics {
		if m.Source == source && m.Metric == metric {
			return m, true
		}
	}
	return Metric{}, false
}

// MetricsOf returns the map entries of one source, in file order.
func (c *Config) MetricsOf(source string) []Metric {
	var out []Metric
	for _, m := range c.Metrics {
		if m.Source == source {
			out = append(out, m)
		}
	}
	return out
}

// Excluded reports whether host (or a parent domain of it) is an excluded
// publisher.
func (c *Config) Excluded(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, h := range c.ExcludedHosts {
		h = strings.ToLower(h)
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// digest is a short hash of what a source's stored rows depend on besides
// the answer itself (the metric map, the aliases, the catalogue's repos):
// when it changes, a source is re-read unconditionally.
func digest(parts ...any) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%v\x00", p)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func sourceIDs() []string {
	ids := make([]string, 0, len(PermittedHosts))
	for id := range PermittedHosts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

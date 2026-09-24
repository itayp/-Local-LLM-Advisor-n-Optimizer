package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"advisor/data"
)

// Default loads the catalogue embedded in the binary (data/catalog/families.yaml).
func Default() (*Catalogue, error) {
	b, err := fs.ReadFile(data.Files, data.CatalogPath)
	if err != nil {
		return nil, fmt.Errorf("catalog: reading the embedded %s: %w", data.CatalogPath, err)
	}
	return Parse(b)
}

// Parse decodes families.yaml strictly — a key the schema does not know is
// an error, so a typo in a curator's edit cannot silently drop a field — and
// validates it. Every problem is reported, not only the first.
func Parse(b []byte) (*Catalogue, error) {
	var c Catalogue
	dec := yaml.NewDecoder(bytes.NewReader(b), yaml.DisallowUnknownField())
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("catalog: families.yaml: %w", err)
	}
	if problems := c.Validate(); len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return &c, nil
}

// ValidationError lists everything wrong with a catalogue.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "catalog: families.yaml is not valid:\n  " + strings.Join(e.Problems, "\n  ")
}

// ErrInvalid is wrapped by ValidationError for errors.Is.
var ErrInvalid = errors.New("catalog: invalid catalogue")

func (e *ValidationError) Unwrap() error { return ErrInvalid }

var (
	familyIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]*$`)
	ollamaTagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._\-]*(:[a-z0-9][a-z0-9._\-]*)?$`)
	hfRepoPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\-]*/[A-Za-z0-9][A-Za-z0-9._\-]*$`)
	quantPattern     = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_]*$`)
)

// Validate returns every problem with c, in file order. Empty means valid.
func (c *Catalogue) Validate() []string {
	var out []string
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	if len(c.Quants) == 0 {
		bad("quants: the list of tracked quants is empty")
	}
	seenQuant := map[string]bool{}
	for _, q := range c.Quants {
		if !quantPattern.MatchString(q) {
			bad("quants: %q is not a quant name as GGUF files spell it (upper case, e.g. Q4_K_M)", q)
		}
		if seenQuant[q] {
			bad("quants: %q is listed twice", q)
		}
		seenQuant[q] = true
	}
	if len(c.Families) == 0 {
		bad("families: the catalogue is empty")
	}

	seenFamily := map[string]bool{}
	seenTag := map[string]string{}
	for i, f := range c.Families {
		where := fmt.Sprintf("families[%d]", i)
		if f.ID != "" {
			where = fmt.Sprintf("family %q", f.ID)
		}
		switch {
		case f.ID == "":
			bad("%s: id is missing", where)
		case !familyIDPattern.MatchString(f.ID):
			bad("%s: id must be lower case letters, digits, '.' and '-'", where)
		case seenFamily[f.ID]:
			bad("%s: id is used twice", where)
		}
		seenFamily[f.ID] = true
		if strings.TrimSpace(f.DisplayName) == "" {
			bad("%s: display_name is missing", where)
		}
		if strings.TrimSpace(f.Maintainer) == "" {
			bad("%s: maintainer is missing", where)
		}
		switch {
		case f.License.SPDX == "" && f.License.Name == "":
			bad("%s: license needs an spdx id, or a name and url", where)
		case f.License.SPDX == "" && !strings.HasPrefix(f.License.URL, "https://"):
			bad("%s: a license without an spdx id needs an https url to read it at", where)
		case f.License.SPDX != "" && strings.ContainsAny(f.License.SPDX, " \t"):
			bad("%s: license spdx %q is not an SPDX id", where, f.License.SPDX)
		}
		if len(f.Purposes) == 0 {
			bad("%s: purposes is empty", where)
		}
		seenPurpose := map[Purpose]bool{}
		for _, p := range f.Purposes {
			if !p.Valid() {
				bad("%s: purpose %q is not one of %v", where, p, Purposes)
			}
			if seenPurpose[p] {
				bad("%s: purpose %q is listed twice", where, p)
			}
			seenPurpose[p] = true
		}
		if _, err := time.Parse(time.DateOnly, f.ReviewedAt); err != nil {
			bad("%s: reviewed_at %q is not a YYYY-MM-DD date", where, f.ReviewedAt)
		}
		if !strings.HasPrefix(f.Source, "https://") {
			bad("%s: source must be the https URL the entry was checked against", where)
		}
		if len(f.Sizes) == 0 {
			bad("%s: sizes is empty", where)
		}
		seenParams := map[uint64]bool{}
		for j, s := range f.Sizes {
			sw := fmt.Sprintf("%s sizes[%d]", where, j)
			if s.OllamaTag != "" {
				sw = fmt.Sprintf("%s size %q", where, s.OllamaTag)
			}
			if s.Parameters == 0 {
				bad("%s: parameters is missing", sw)
			} else if seenParams[s.Parameters] {
				bad("%s: two sizes have %d parameters", sw, s.Parameters)
			}
			seenParams[s.Parameters] = true
			if s.ActiveParameters >= s.Parameters && s.ActiveParameters != 0 {
				bad("%s: active_parameters must be fewer than parameters (omit it for a dense model)", sw)
			}
			if s.ContextLength <= 0 {
				bad("%s: context_length is missing", sw)
			}
			switch {
			case s.OllamaTag == "":
				bad("%s: ollama_tag is missing", sw)
			case !ollamaTagPattern.MatchString(s.OllamaTag):
				bad("%s: ollama_tag %q is not an Ollama library name[:tag]", sw, s.OllamaTag)
			default:
				norm := NormalizeTag(s.OllamaTag)
				if prev, dup := seenTag[norm]; dup {
					bad("%s: ollama_tag %q is also used by %s", sw, s.OllamaTag, prev)
				}
				seenTag[norm] = f.ID
			}
			if !hfRepoPattern.MatchString(s.HFRepo) {
				bad("%s: hf_repo %q is not an owner/name Hugging Face repo", sw, s.HFRepo)
			}
			switch {
			case s.HFBaseRepo == "":
				bad("%s: hf_base_repo is missing (the original model's repo, where its public scores live)", sw)
			case !hfRepoPattern.MatchString(s.HFBaseRepo):
				bad("%s: hf_base_repo %q is not an owner/name Hugging Face repo", sw, s.HFBaseRepo)
			case strings.EqualFold(s.HFBaseRepo, s.HFRepo):
				bad("%s: hf_base_repo names the GGUF repo; it must be the original model's repo", sw)
			}
			for _, same := range s.HFBaseSameAs {
				switch {
				case !hfRepoPattern.MatchString(same):
					bad("%s: hf_base_same_as %q is not an owner/name Hugging Face repo", sw, same)
				case strings.EqualFold(same, s.HFBaseRepo):
					bad("%s: hf_base_same_as repeats hf_base_repo", sw)
				}
			}
			if s.OllamaQuant != "" && !c.Tracks(s.OllamaQuant) {
				bad("%s: ollama_quant %q is not one of the tracked quants %v", sw, s.OllamaQuant, c.Quants)
			}
		}
	}
	return out
}

// NormalizeTag spells an Ollama model reference the way Ollama lists it: a
// name with no tag means ":latest", and the default registry's prefix is
// dropped ("registry.ollama.ai/library/qwen3:4b" is "qwen3:4b").
func NormalizeTag(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	t = strings.TrimPrefix(t, "registry.ollama.ai/")
	t = strings.TrimPrefix(t, "library/")
	if !strings.Contains(lastPathElem(t), ":") {
		t += ":latest"
	}
	return t
}

func lastPathElem(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Tracks reports whether quant is one of the catalogue's tracked quants.
func (c *Catalogue) Tracks(quant string) bool {
	for _, q := range c.Quants {
		if strings.EqualFold(q, quant) {
			return true
		}
	}
	return false
}

// Family returns the family with id.
func (c *Catalogue) Family(id string) (Family, bool) {
	for _, f := range c.Families {
		if f.ID == id {
			return f, true
		}
	}
	return Family{}, false
}

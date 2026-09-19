package bench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"advisor/data"
)

// Suite mirrors data/bench/suite.yaml; the file's header comment is the
// schema of record. It is the whole of what a benchmark sends to a model:
// the suite's own text, never anything the user typed (product rule 7).
type Suite struct {
	Version          string       `yaml:"version"`
	ReviewedAt       string       `yaml:"reviewed_at"`
	Text             string       `yaml:"text"`
	Lead             string       `yaml:"lead"`
	CompletionTokens int          `yaml:"completion_tokens"`
	Temperature      float64      `yaml:"temperature"`
	Seed             int          `yaml:"seed"`
	Warmups          int          `yaml:"warmups"`
	Repeats          int          `yaml:"repeats"`
	Prompts          []PromptSpec `yaml:"prompts"`

	paragraphs []string
	digest     string
}

// PromptSpec is one prompt of the suite.
type PromptSpec struct {
	ID         string `yaml:"id"`
	Paragraphs int    `yaml:"paragraphs"`
	// Tokens is the prompt's length with the reference tokenizer (Llama 3's):
	// what decides, before a run, whether it fits a context. Each model's own
	// count comes back from the runtime.
	Tokens int `yaml:"tokens"`
}

// ErrSuite wraps every problem with a suite file.
var ErrSuite = errors.New("bench: invalid suite")

// DefaultSuite is the suite embedded in the binary.
func DefaultSuite() (*Suite, error) {
	return LoadSuite(data.Files, data.BenchSuitePath)
}

// LoadSuite reads a suite and its text from fsys.
func LoadSuite(fsys fs.FS, suitePath string) (*Suite, error) {
	y, err := fs.ReadFile(fsys, suitePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSuite, err)
	}
	var s Suite
	dec := yaml.NewDecoder(bytes.NewReader(y), yaml.DisallowUnknownField())
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrSuite, suitePath, err)
	}
	if s.Text == "" || strings.ContainsAny(s.Text, `/\`) {
		return nil, fmt.Errorf("%w: text must name a file beside the suite", ErrSuite)
	}
	text, err := fs.ReadFile(fsys, path.Join(path.Dir(suitePath), s.Text))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSuite, err)
	}
	if err := s.init(y, text); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *Suite) init(yamlBytes, text []byte) error {
	norm := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
	for _, p := range strings.Split(strings.TrimSpace(string(norm(text))), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			s.paragraphs = append(s.paragraphs, p)
		}
	}
	h := sha256.New()
	h.Write(norm(yamlBytes))
	h.Write([]byte{0})
	h.Write(norm(text))
	s.digest = hex.EncodeToString(h.Sum(nil))[:16]

	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if s.Version == "" {
		bad("version is required")
	}
	if !strings.Contains(s.Lead, "{n}") {
		bad("lead must contain {n}, so no two requests share a prefix")
	}
	if s.CompletionTokens <= 0 {
		bad("completion_tokens must be positive")
	}
	if s.Warmups < 0 {
		bad("warmups cannot be negative")
	}
	if s.Repeats < 1 {
		bad("repeats must be at least 1")
	}
	if len(s.Prompts) == 0 {
		bad("at least one prompt")
	}
	seen := map[string]bool{}
	prev := 0
	for _, p := range s.Prompts {
		switch {
		case p.ID == "" || seen[p.ID]:
			bad("prompt %q: ids must be present and unique", p.ID)
		case p.Paragraphs < 1 || p.Paragraphs > len(s.paragraphs):
			bad("prompt %q: paragraphs must be between 1 and %d", p.ID, len(s.paragraphs))
		case p.Tokens <= prev:
			bad("prompt %q: prompts are listed shortest first, and tokens must be counted", p.ID)
		}
		seen[p.ID] = true
		prev = p.Tokens
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrSuite, strings.Join(problems, "; "))
	}
	return nil
}

// Digest identifies the suite's exact content: the file and the text.
func (s *Suite) Digest() string { return s.digest }

// Prompt finds a prompt by id.
func (s *Suite) Prompt(id string) (PromptSpec, bool) {
	for _, p := range s.Prompts {
		if p.ID == id {
			return p, true
		}
	}
	return PromptSpec{}, false
}

// Body is a prompt's text: its first N paragraphs.
func (s *Suite) Body(p PromptSpec) string {
	return strings.Join(s.paragraphs[:p.Paragraphs], "\n\n")
}

// Words counts the words of what Request(p, n) sends, for the
// tokens-per-word ratio a run learns from its first request.
func (s *Suite) Words(p PromptSpec, n int) int {
	return len(strings.Fields(s.Request(p, n)))
}

// Request is the text of the n-th request of a run for prompt p: the lead
// line, numbered, then the prompt's paragraphs. It is sent raw — no chat
// template, no system prompt.
func (s *Suite) Request(p PromptSpec, n int) string {
	return strings.ReplaceAll(s.Lead, "{n}", strconv.Itoa(n)) + "\n\n" + s.Body(p)
}

// Options are the runtime options every request of a run carries:
// deterministic sampling, the answer's budget, and the context under test.
func (s *Suite) Options(numCtx int) map[string]any {
	return map[string]any{
		"temperature": s.Temperature,
		"seed":        s.Seed,
		"num_predict": s.CompletionTokens,
		"num_ctx":     numCtx,
	}
}

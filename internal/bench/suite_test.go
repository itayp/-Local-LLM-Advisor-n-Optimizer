package bench

import (
	"strings"
	"testing"
	"testing/fstest"
	"unicode"
)

// suiteDigests pins every suite version to its exact content. Editing the
// suite or its text without bumping its version fails here: runs of one
// version must have measured the same thing (data/bench/suite.yaml).
var suiteDigests = map[string]string{
	"1": "e8fe8f89b46d9a09",
}

func TestTheSuiteIsPinnedToItsVersion(t *testing.T) {
	s, err := DefaultSuite()
	if err != nil {
		t.Fatal(err)
	}
	want, ok := suiteDigests[s.Version]
	if !ok {
		t.Fatalf("suite version %q has no pinned digest; add %q to suiteDigests once the new version is final", s.Version, s.Digest())
	}
	if s.Digest() != want {
		t.Fatalf("suite version %q changed (digest %s, pinned %s): bump the version in data/bench/suite.yaml and pin the new digest",
			s.Version, s.Digest(), want)
	}
}

// The three prompts the build plan asks for — roughly 500, 2,000 and 8,000
// tokens of ordinary English — with a 256-token answer, deterministic
// sampling, one warm-up and three timed runs each; and the long prompt
// with its answer fits a context of 8,192.
func TestTheSuiteIsTheOneThePlanAsksFor(t *testing.T) {
	s, err := DefaultSuite()
	if err != nil {
		t.Fatal(err)
	}
	if s.CompletionTokens != 256 || s.Temperature != 0 || s.Seed == 0 || s.Warmups != 1 || s.Repeats != 3 {
		t.Fatalf("options: %+v", s)
	}
	nominal := map[string][2]int{"500": {400, 600}, "2000": {1700, 2300}, "8000": {7000, 8192 - 256 - 64}}
	if len(s.Prompts) != len(nominal) {
		t.Fatalf("prompts %+v", s.Prompts)
	}
	for _, p := range s.Prompts {
		r := nominal[p.ID]
		if p.Tokens < r[0] || p.Tokens > r[1] {
			t.Errorf("prompt %s: %d tokens, want %d–%d", p.ID, p.Tokens, r[0], r[1])
		}
		// The stated count is Llama 3's; English prose runs 1.1–1.3 tokens a
		// word in every current tokenizer, so a count far from that was not
		// counted on this text.
		if ratio := float64(p.Tokens) / float64(len(strings.Fields(s.Body(p)))); ratio < 1.1 || ratio > 1.3 {
			t.Errorf("prompt %s: %.2f tokens a word; recount the tokens", p.ID, ratio)
		}
	}
	opts := s.Options(8192)
	if opts["num_ctx"] != 8192 || opts["num_predict"] != 256 || opts["temperature"] != float64(0) {
		t.Fatalf("options %v", opts)
	}
}

// Ordinary English prose, the suite's own: printable ASCII, sentences, no
// markup, no instructions to the model.
func TestTheTextIsOrdinaryEnglish(t *testing.T) {
	s, err := DefaultSuite()
	if err != nil {
		t.Fatal(err)
	}
	long := s.Body(s.Prompts[len(s.Prompts)-1])
	for i, r := range long {
		if r > unicode.MaxASCII || (r < ' ' && r != '\n') {
			t.Fatalf("character %q at %d: keep the text plain ASCII so every tokenizer sees the same bytes", r, i)
		}
	}
	for _, bad := range []string{"#", "<", ">", "{", "}", "http", "You are", "Assistant"} {
		if strings.Contains(long, bad) {
			t.Errorf("the text contains %q", bad)
		}
	}
}

// Every request of a run differs from the one before within its first
// tokens, so the runtime cannot answer from its cache of the last prompt.
func TestRequestsDifferAtTheStart(t *testing.T) {
	s, err := DefaultSuite()
	if err != nil {
		t.Fatal(err)
	}
	p := s.Prompts[0]
	a, b := s.Request(p, 1), s.Request(p, 2)
	if a == b || a[:2] == b[:2] || !strings.HasPrefix(a, "1.\n\n") || !strings.HasSuffix(a, s.Body(p)) {
		t.Fatalf("requests %q… and %q…", a[:20], b[:20])
	}
	if s.Words(p, 1) != len(strings.Fields(s.Body(p)))+1 {
		t.Fatalf("words %d", s.Words(p, 1))
	}
}

func TestInvalidSuitesFailLoudly(t *testing.T) {
	good := `version: "9"
reviewed_at: "2026-09-19"
text: t.txt
lead: "{n}."
completion_tokens: 8
temperature: 0
seed: 1
warmups: 1
repeats: 2
prompts:
  - {id: a, paragraphs: 1, tokens: 5}
`
	fs := func(y string) fstest.MapFS {
		return fstest.MapFS{"s.yaml": {Data: []byte(y)}, "t.txt": {Data: []byte("One two three.\n\nFour five.\n")}}
	}
	if _, err := LoadSuite(fs(good), "s.yaml"); err != nil {
		t.Fatalf("a good suite: %v", err)
	}
	for name, y := range map[string]string{
		"unknown key":    good + "extra: 1\n",
		"lead without n": strings.Replace(good, `"{n}."`, `"Go."`, 1),
		"too many paras": strings.Replace(good, "paragraphs: 1", "paragraphs: 3", 1),
		"no repeats":     strings.Replace(good, "repeats: 2", "repeats: 0", 1),
		"text elsewhere": strings.Replace(good, "t.txt", "../t.txt", 1),
	} {
		if _, err := LoadSuite(fs(y), "s.yaml"); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

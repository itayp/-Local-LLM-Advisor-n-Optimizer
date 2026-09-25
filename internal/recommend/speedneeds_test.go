package recommend

import (
	"strings"
	"testing"

	"advisor/internal/catalog"
)

// validSpeedNeedsYAML is a minimal, complete, valid speed-needs.yaml: one
// row per catalog.Purpose, every value with a basis. Negative tests mutate a
// copy of it with strings.Replace, so each replaced fragment must appear
// exactly once here.
const validSpeedNeedsYAML = `
version: 1
checked: 2026-01-01
words_per_token: 0.75

sources:
  cite: "some citation"

reading:
  listening: {wpm: 160, basis: public, source: cite}
  reading: {wpm: 238, basis: public, source: cite}
  skimming: {wpm: 450, basis: public, source: cite}

stream_bars:
  excellent: {anchor: skimming, basis: chosen, settle: "x"}
  good: {anchor: reading, basis: chosen, settle: "x"}
  usable: {anchor: listening, basis: chosen, settle: "x"}

purposes:
  - purpose: chat
    mode: read_along
    prompt_tokens: {value: 70, basis: public, source: cite}
    wait_s:
      excellent: {value: 1, basis: public, source: cite}
      good: {value: 4, basis: chosen, settle: "x"}
      usable: {value: 10, basis: public, source: cite}
  - purpose: writing
    mode: read_along
    prompt_tokens: {value: 300, basis: chosen, settle: "x"}
    wait_s:
      excellent: {value: 1, basis: public, source: cite}
      good: {value: 4, basis: chosen, settle: "x"}
      usable: {value: 10, basis: public, source: cite}
  - purpose: coding
    mode: read_along
    prompt_tokens: {value: 2000, basis: chosen, settle: "x"}
    wait_s:
      excellent: {value: 1, basis: public, source: cite}
      good: {value: 4, basis: chosen, settle: "x"}
      usable: {value: 10, basis: public, source: cite}
  - purpose: vision
    mode: read_along
    prompt_tokens: {value: 1000, basis: chosen, settle: "x"}
    wait_s:
      excellent: {value: 1, basis: public, source: cite}
      good: {value: 4, basis: chosen, settle: "x"}
      usable: {value: 10, basis: public, source: cite}
  - purpose: reasoning
    mode: read_along
    prompt_tokens: {value: 70, basis: public, source: cite}
    thinking_tokens: {value: 1000, basis: chosen, settle: "x"}
    wait_s:
      excellent: {value: 10, basis: public, source: cite}
      good: {value: 30, basis: chosen, settle: "x"}
      usable: {value: 120, basis: chosen, settle: "x"}
  - purpose: long_context
    mode: read_along
    prompt_tokens: {value: 20000, basis: chosen, settle: "x"}
    wait_s:
      excellent: {value: 10, basis: public, source: cite}
      good: {value: 30, basis: chosen, settle: "x"}
      usable: {value: 120, basis: chosen, settle: "x"}
  - purpose: agentic
    mode: per_step
    step_prompt_tokens: {value: 1000, basis: chosen, settle: "x"}
    step_output_tokens: {value: 150, basis: chosen, settle: "x"}
    wait_s:
      excellent: {value: 10, basis: public, source: cite}
      good: {value: 30, basis: chosen, settle: "x"}
      usable: {value: 120, basis: chosen, settle: "x"}
`

func mustParseSpeedNeeds(t *testing.T, src string) *SpeedNeeds {
	t.Helper()
	sn, err := ParseSpeedNeeds([]byte(src))
	if err != nil {
		t.Fatalf("expected a valid file, got: %v", err)
	}
	return sn
}

// mutate replaces old with new exactly once in the base fixture, failing the
// test (not the parse) if old is not found exactly once — a guard against a
// negative test silently testing nothing because its target text drifted.
func mutate(t *testing.T, old, new string) string {
	t.Helper()
	if n := strings.Count(validSpeedNeedsYAML, old); n != 1 {
		t.Fatalf("fixture text %q appears %d times, want exactly 1", old, n)
	}
	return strings.Replace(validSpeedNeedsYAML, old, new, 1)
}

func wantErr(t *testing.T, src string, contains string) {
	t.Helper()
	_, err := ParseSpeedNeeds([]byte(src))
	if err == nil {
		t.Fatalf("expected an error containing %q, got none", contains)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Errorf("error %q does not contain %q", err.Error(), contains)
	}
}

func TestValidSpeedNeedsFixtureParses(t *testing.T) {
	mustParseSpeedNeeds(t, validSpeedNeedsYAML)
}

// TestEmbeddedSpeedNeedsFileIsValid loads the real, shipped
// data/recommend/speed-needs.yaml and checks the derived tok/s the file's
// own header comment states: listening 3.56, reading 5.29, skimming 10.0
// (wpm ÷ 60 ÷ words_per_token).
func TestEmbeddedSpeedNeedsFileIsValid(t *testing.T) {
	sn, err := DefaultSpeedNeeds()
	if err != nil {
		t.Fatalf("the embedded speed-needs.yaml must parse and validate: %v", err)
	}
	within := func(got, want float64) bool { return got > want-0.01 && got < want+0.01 }
	if got := sn.file.Reading.Listening.TokS(sn.WordsPerToken()); !within(got, 3.56) {
		t.Errorf("listening tok/s = %v, want ~3.56", got)
	}
	if got := sn.file.Reading.Reading.TokS(sn.WordsPerToken()); !within(got, 5.29) {
		t.Errorf("reading tok/s = %v, want ~5.29", got)
	}
	if got := sn.file.Reading.Skimming.TokS(sn.WordsPerToken()); !within(got, 10.0) {
		t.Errorf("skimming tok/s = %v, want ~10.0", got)
	}
	if len(sn.file.Purposes) != len(catalog.Purposes) {
		t.Errorf("purposes = %d, want %d (one per catalog.Purpose)", len(sn.file.Purposes), len(catalog.Purposes))
	}
}

func TestExactlyOneRowPerPurpose(t *testing.T) {
	t.Run("duplicate purpose", func(t *testing.T) {
		src := mutate(t, "- purpose: writing\n    mode: read_along", "- purpose: chat\n    mode: read_along")
		wantErr(t, src, "purpose listed twice")
	})
	t.Run("missing purpose", func(t *testing.T) {
		// Renaming writing to chat both duplicates chat AND removes writing;
		// the duplicate is caught first, so remove the row outright instead.
		start := strings.Index(validSpeedNeedsYAML, "  - purpose: writing")
		end := strings.Index(validSpeedNeedsYAML, "  - purpose: coding")
		if start < 0 || end < 0 || end <= start {
			t.Fatalf("fixture layout drifted: could not locate the writing row")
		}
		src := validSpeedNeedsYAML[:start] + validSpeedNeedsYAML[end:]
		wantErr(t, src, `missing a row for "writing"`)
	})
}

func TestModeRequiresItsFields(t *testing.T) {
	t.Run("invalid mode", func(t *testing.T) {
		src := mutate(t, "- purpose: chat\n    mode: read_along", "- purpose: chat\n    mode: sideways")
		wantErr(t, src, "mode \"sideways\"")
	})
	t.Run("read_along without prompt_tokens", func(t *testing.T) {
		src := mutate(t, "    prompt_tokens: {value: 70, basis: public, source: cite}\n    wait_s:\n      excellent: {value: 1, basis: public, source: cite}\n      good: {value: 4, basis: chosen, settle: \"x\"}\n      usable: {value: 10, basis: public, source: cite}\n  - purpose: writing",
			"    wait_s:\n      excellent: {value: 1, basis: public, source: cite}\n      good: {value: 4, basis: chosen, settle: \"x\"}\n      usable: {value: 10, basis: public, source: cite}\n  - purpose: writing")
		wantErr(t, src, "read_along needs prompt_tokens")
	})
	t.Run("read_along with a per_step field", func(t *testing.T) {
		src := mutate(t, "- purpose: chat\n    mode: read_along\n    prompt_tokens: {value: 70, basis: public, source: cite}",
			"- purpose: chat\n    mode: read_along\n    prompt_tokens: {value: 70, basis: public, source: cite}\n    step_prompt_tokens: {value: 1, basis: chosen, settle: \"x\"}")
		wantErr(t, src, "does not take step_prompt_tokens")
	})
	t.Run("per_step without step tokens", func(t *testing.T) {
		src := mutate(t, "    step_prompt_tokens: {value: 1000, basis: chosen, settle: \"x\"}\n    step_output_tokens: {value: 150, basis: chosen, settle: \"x\"}\n    wait_s:\n      excellent: {value: 10, basis: public, source: cite}\n      good: {value: 30, basis: chosen, settle: \"x\"}\n      usable: {value: 120, basis: chosen, settle: \"x\"}",
			"    wait_s:\n      excellent: {value: 10, basis: public, source: cite}\n      good: {value: 30, basis: chosen, settle: \"x\"}\n      usable: {value: 120, basis: chosen, settle: \"x\"}")
		wantErr(t, src, "per_step needs step_prompt_tokens")
	})
	t.Run("per_step with a read_along field", func(t *testing.T) {
		src := mutate(t, "- purpose: agentic\n    mode: per_step",
			"- purpose: agentic\n    mode: per_step\n    prompt_tokens: {value: 1, basis: chosen, settle: \"x\"}")
		wantErr(t, src, "does not take prompt_tokens")
	})
}

func TestWaitBarsMustRise(t *testing.T) {
	t.Run("excellent not below good", func(t *testing.T) {
		src := mutate(t, "excellent: {value: 1, basis: public, source: cite}\n      good: {value: 4, basis: chosen, settle: \"x\"}\n      usable: {value: 10, basis: public, source: cite}\n  - purpose: writing",
			"excellent: {value: 5, basis: public, source: cite}\n      good: {value: 4, basis: chosen, settle: \"x\"}\n      usable: {value: 10, basis: public, source: cite}\n  - purpose: writing")
		wantErr(t, src, "wait_s must rise")
	})
	t.Run("good not below usable", func(t *testing.T) {
		src := mutate(t, "good: {value: 4, basis: chosen, settle: \"x\"}\n      usable: {value: 10, basis: public, source: cite}\n  - purpose: writing",
			"good: {value: 11, basis: chosen, settle: \"x\"}\n      usable: {value: 10, basis: public, source: cite}\n  - purpose: writing")
		wantErr(t, src, "wait_s must rise")
	})
}

func TestReadingAnchorsMustRise(t *testing.T) {
	t.Run("listening not below reading", func(t *testing.T) {
		src := mutate(t, "listening: {wpm: 160, basis: public, source: cite}", "listening: {wpm: 300, basis: public, source: cite}")
		wantErr(t, src, "reading anchors must rise")
	})
	t.Run("reading not below skimming", func(t *testing.T) {
		src := mutate(t, "reading: {wpm: 238, basis: public, source: cite}", "reading: {wpm: 500, basis: public, source: cite}")
		wantErr(t, src, "reading anchors must rise")
	})
}

func TestStreamBarsAnchorsMustBeKnownAndMustNotReadSlowerAsTheGradeImproves(t *testing.T) {
	t.Run("unknown anchor", func(t *testing.T) {
		src := mutate(t, "excellent: {anchor: skimming, basis: chosen, settle: \"x\"}", "excellent: {anchor: sprinting, basis: chosen, settle: \"x\"}")
		wantErr(t, src, "is not listening, reading or skimming")
	})
	t.Run("excellent slower than good", func(t *testing.T) {
		src := mutate(t, "excellent: {anchor: skimming, basis: chosen, settle: \"x\"}", "excellent: {anchor: listening, basis: chosen, settle: \"x\"}")
		wantErr(t, src, "must not read slower")
	})
}

func TestBasisRules(t *testing.T) {
	cases := []struct {
		name, old, new, wantErrSubstr string
	}{
		{"public without source", "excellent: {anchor: skimming, basis: chosen, settle: \"x\"}", "excellent: {anchor: skimming, basis: public}", "basis public needs a source"},
		{"public source not listed", "usable: {anchor: listening, basis: chosen, settle: \"x\"}", "usable: {anchor: listening, basis: public, source: nowhere}", "is not in sources"},
		{"public with settle too", "usable: {anchor: listening, basis: chosen, settle: \"x\"}", "usable: {anchor: listening, basis: public, source: cite, settle: \"x\"}", "with a trial or settle set too"},
		{"trial without date", "usable: {anchor: listening, basis: chosen, settle: \"x\"}", "usable: {anchor: listening, basis: trial}", "basis trial needs"},
		{"trial bad date", "usable: {anchor: listening, basis: chosen, settle: \"x\"}", "usable: {anchor: listening, basis: trial, trial: 09-25-2026}", "not YYYY-MM-DD"},
		{"trial with source too", "usable: {anchor: listening, basis: chosen, settle: \"x\"}", "usable: {anchor: listening, basis: trial, trial: 2026-09-25, source: cite}", "with a source or settle set too"},
		{"chosen without settle", "usable: {anchor: listening, basis: chosen, settle: \"x\"}", "usable: {anchor: listening, basis: chosen}", "basis chosen needs settle"},
		{"chosen with source too", "usable: {anchor: listening, basis: chosen, settle: \"x\"}", "usable: {anchor: listening, basis: chosen, settle: \"x\", source: cite}", "with a source or trial set too"},
		{"unknown basis", "usable: {anchor: listening, basis: chosen, settle: \"x\"}", "usable: {anchor: listening, basis: guessed, settle: \"x\"}", "is not public, trial or chosen"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantErr(t, mutate(t, c.old, c.new), c.wantErrSubstr)
		})
	}
}

func TestWaitSecondsArithmetic(t *testing.T) {
	sn := mustParseSpeedNeeds(t, validSpeedNeedsYAML)
	coding, ok := sn.Purpose(catalog.PurposeCoding)
	if !ok {
		t.Fatal("no coding row")
	}
	seconds, ok := coding.waitSeconds(1000, 100) // 2000 prompt tokens / 1000 tok/s
	if !ok || seconds < 1.99 || seconds > 2.01 {
		t.Errorf("coding wait at 1000 prompt tok/s = %v, %v; want ~2s", seconds, ok)
	}
	if _, ok := coding.waitSeconds(0, 100); ok {
		t.Error("a zero prompt rate must be unknown, not zero")
	}

	agentic, ok := sn.Purpose(catalog.PurposeAgentic)
	if !ok {
		t.Fatal("no agentic row")
	}
	// step_prompt_tokens 1000 / 500 tok/s = 2s, step_output_tokens 150 / 100 tok/s = 1.5s
	seconds, ok = agentic.waitSeconds(500, 100)
	if !ok || seconds < 3.49 || seconds > 3.51 {
		t.Errorf("agentic wait = %v, %v; want ~3.5s", seconds, ok)
	}
}

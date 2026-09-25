package recommend

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-yaml"

	"advisor/data"
	"advisor/internal/catalog"
)

// Mode is how a purpose's speed is judged (ARCHITECTURE.md D-58): read_along
// compares answer speed with how fast a person takes the words in;
// per_step (agentic) has no one reading along, so its one bar is the time
// for a whole step instead.
type Mode string

const (
	ModeReadAlong Mode = "read_along"
	ModePerStep   Mode = "per_step"
)

func (m Mode) valid() bool { return m == ModeReadAlong || m == ModePerStep }

// Basis is where a speed-needs value came from: a published figure, the
// fleet trial that settles it, or a judgement still awaiting that trial.
// Every value in data/recommend/speed-needs.yaml carries exactly one.
type Basis string

const (
	BasisPublic Basis = "public"
	BasisTrial  Basis = "trial"
	BasisChosen Basis = "chosen"
)

func (b Basis) valid() bool { return b == BasisPublic || b == BasisTrial || b == BasisChosen }

// Provenance is the four fields every valued entry in the file carries
// beside its number: exactly one of source (basis public), trial (basis
// trial) or settle (basis chosen) — never source and settle together, and
// never neither. Embedded (yaml:",inline") so its keys sit flat beside
// "value" or "wpm" in the file, rather than nested under "provenance".
type Provenance struct {
	Basis  Basis  `yaml:"basis"`
	Source string `yaml:"source,omitempty"`
	Trial  string `yaml:"trial,omitempty"`
	Settle string `yaml:"settle,omitempty"`
	Note   string `yaml:"note,omitempty"`
}

func (p Provenance) check(where string, sources map[string]string) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("recommend: speed-needs.yaml: %s: "+format, append([]any{where}, args...)...)
	}
	if !p.Basis.valid() {
		return bad("basis %q is not public, trial or chosen", p.Basis)
	}
	switch p.Basis {
	case BasisPublic:
		if p.Source == "" {
			return bad("basis public needs a source")
		}
		if p.Trial != "" || p.Settle != "" {
			return bad("basis public with a trial or settle set too")
		}
		if _, ok := sources[p.Source]; !ok {
			return bad("source %q is not in sources", p.Source)
		}
	case BasisTrial:
		if p.Trial == "" {
			return bad("basis trial needs the trial's date")
		}
		if p.Source != "" || p.Settle != "" {
			return bad("basis trial with a source or settle set too")
		}
		if _, err := time.Parse("2006-01-02", p.Trial); err != nil {
			return bad("trial date %q is not YYYY-MM-DD", p.Trial)
		}
	case BasisChosen:
		if p.Settle == "" {
			return bad("basis chosen needs settle: what would replace it")
		}
		if p.Source != "" || p.Trial != "" {
			return bad("basis chosen with a source or trial set too")
		}
	}
	return nil
}

// NumBasis is one number in speed-needs.yaml with its provenance: a token
// count, a seconds threshold.
type NumBasis struct {
	Value      float64 `yaml:"value"`
	Provenance `yaml:",inline"`
}

// ReadingRate is one of the three reading anchors (reading.listening,
// .reading, .skimming): words a minute, with its provenance.
type ReadingRate struct {
	WPM        float64 `yaml:"wpm"`
	Provenance `yaml:",inline"`
}

// TokS is the anchor's rate in tokens a second: wpm ÷ 60 ÷ words_per_token.
func (r ReadingRate) TokS(wordsPerToken float64) float64 {
	return r.WPM / 60 / wordsPerToken
}

// ReadingAnchors are the three reading speeds a stream grade is judged
// against (Brysbaert 2019).
type ReadingAnchors struct {
	Listening ReadingRate `yaml:"listening"`
	Reading   ReadingRate `yaml:"reading"`
	Skimming  ReadingRate `yaml:"skimming"`
}

func (r ReadingAnchors) byName(anchor string) (ReadingRate, bool) {
	switch anchor {
	case "listening":
		return r.Listening, true
	case "reading":
		return r.Reading, true
	case "skimming":
		return r.Skimming, true
	}
	return ReadingRate{}, false
}

// rank orders the three anchors slowest (0) to fastest (2), for the loader's
// own rising-order checks.
func (r ReadingAnchors) rank(anchor string) int {
	switch anchor {
	case "listening":
		return 0
	case "reading":
		return 1
	case "skimming":
		return 2
	}
	return -1
}

// StreamBar names which reading anchor a read_along grade needs.
type StreamBar struct {
	Anchor     string `yaml:"anchor"`
	Provenance `yaml:",inline"`
}

// StreamBars are the three grades a read_along purpose's answer speed is
// graded against, each pointing at one reading anchor.
type StreamBars struct {
	Excellent StreamBar `yaml:"excellent"`
	Good      StreamBar `yaml:"good"`
	Usable    StreamBar `yaml:"usable"`
}

// WaitBars are the seconds-before-the-first-word thresholds for one purpose
// (or, for a per_step purpose, one whole step), at each grade.
type WaitBars struct {
	Excellent NumBasis `yaml:"excellent"`
	Good      NumBasis `yaml:"good"`
	Usable    NumBasis `yaml:"usable"`
}

// PurposeSpeedNeed is one row of speed-needs.yaml: what a purpose's typical
// prompt looks like, and how long a wait still feels good.
type PurposeSpeedNeed struct {
	Purpose catalog.Purpose `yaml:"purpose"`
	Mode    Mode            `yaml:"mode"`

	// read_along only:
	PromptTokens   *NumBasis `yaml:"prompt_tokens,omitempty"`
	ThinkingTokens *NumBasis `yaml:"thinking_tokens,omitempty"` // reasoning only

	// per_step only:
	StepPromptTokens *NumBasis `yaml:"step_prompt_tokens,omitempty"`
	StepOutputTokens *NumBasis `yaml:"step_output_tokens,omitempty"`

	WaitS WaitBars `yaml:"wait_s"`
	Note  string   `yaml:"note,omitempty"`
}

// waitSeconds is D-58's wait arithmetic: the seconds before the first
// visible word, at the given prompt and generation rates (tokens a second).
// ok is false when a rate this mode needs is missing or zero — the caller
// must then treat the wait as unknown, never as zero (CLAUDE.md, "unknown is
// unknown").
func (p PurposeSpeedNeed) waitSeconds(promptTPS, genTPS float64) (seconds float64, ok bool) {
	switch p.Mode {
	case ModePerStep:
		if p.StepPromptTokens == nil || p.StepOutputTokens == nil || promptTPS <= 0 || genTPS <= 0 {
			return 0, false
		}
		return p.StepPromptTokens.Value/promptTPS + p.StepOutputTokens.Value/genTPS, true
	default: // read_along
		if p.PromptTokens == nil || promptTPS <= 0 {
			return 0, false
		}
		seconds = p.PromptTokens.Value / promptTPS
		if p.ThinkingTokens != nil {
			if genTPS <= 0 {
				return 0, false
			}
			seconds += p.ThinkingTokens.Value / genTPS
		}
		return seconds, true
	}
}

// waitGrade grades a wait, in seconds, against this purpose's own bars.
func (p PurposeSpeedNeed) waitGrade(seconds float64) Grade {
	switch {
	case seconds <= p.WaitS.Excellent.Value:
		return GradeExcellent
	case seconds <= p.WaitS.Good.Value:
		return GradeGood
	case seconds <= p.WaitS.Usable.Value:
		return GradeUsable
	default:
		return GradeTooSlow
	}
}

// SpeedNeedsFile mirrors data/recommend/speed-needs.yaml field for field;
// the file's own header comment is the schema of record.
type SpeedNeedsFile struct {
	Version       int                `yaml:"version"`
	Checked       string             `yaml:"checked"`
	WordsPerToken float64            `yaml:"words_per_token"`
	Sources       map[string]string  `yaml:"sources"`
	Reading       ReadingAnchors     `yaml:"reading"`
	StreamBars    StreamBars         `yaml:"stream_bars"`
	Purposes      []PurposeSpeedNeed `yaml:"purposes"`
}

// SpeedNeeds is the parsed, validated table.
type SpeedNeeds struct {
	file SpeedNeedsFile
}

var (
	speedNeedsOnce sync.Once
	speedNeedsData *SpeedNeeds
	speedNeedsErr  error
)

// DefaultSpeedNeeds parses the table embedded in the binary, once.
func DefaultSpeedNeeds() (*SpeedNeeds, error) {
	speedNeedsOnce.Do(func() {
		b, err := fs.ReadFile(data.Files, data.SpeedNeedsPath)
		if err != nil {
			speedNeedsErr = fmt.Errorf("recommend: reading the embedded %s: %w", data.SpeedNeedsPath, err)
			return
		}
		speedNeedsData, speedNeedsErr = ParseSpeedNeeds(b)
	})
	return speedNeedsData, speedNeedsErr
}

// ParseSpeedNeeds decodes speed-needs.yaml strictly (an unknown key is an
// error) and validates every rule its own header comment states.
func ParseSpeedNeeds(b []byte) (*SpeedNeeds, error) {
	var f SpeedNeedsFile
	dec := yaml.NewDecoder(bytes.NewReader(b), yaml.DisallowUnknownField())
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("recommend: speed-needs.yaml: %w", err)
	}
	if err := validateSpeedNeeds(f); err != nil {
		return nil, err
	}
	return &SpeedNeeds{file: f}, nil
}

func validateSpeedNeeds(f SpeedNeedsFile) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("recommend: speed-needs.yaml: "+format, args...)
	}
	if f.Version < 1 {
		return bad("version must be at least 1")
	}
	if _, err := time.Parse("2006-01-02", f.Checked); err != nil {
		return bad("checked date %q is not YYYY-MM-DD", f.Checked)
	}
	if f.WordsPerToken <= 0 || f.WordsPerToken > 2 {
		return bad("words_per_token %v is not a plausible words-per-token ratio", f.WordsPerToken)
	}
	if len(f.Sources) == 0 {
		return bad("sources must have at least one citation")
	}
	for id, text := range f.Sources {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(text) == "" {
			return bad("a source id and its citation must both be non-empty")
		}
	}

	// Reading anchors: rising listening < reading < skimming (they are words
	// a minute a person takes in; each faster than the last).
	if err := f.Reading.Listening.check("reading.listening", f.Sources); err != nil {
		return err
	}
	if err := f.Reading.Reading.check("reading.reading", f.Sources); err != nil {
		return err
	}
	if err := f.Reading.Skimming.check("reading.skimming", f.Sources); err != nil {
		return err
	}
	if !(f.Reading.Listening.WPM < f.Reading.Reading.WPM && f.Reading.Reading.WPM < f.Reading.Skimming.WPM) {
		return bad("reading anchors must rise: listening (%v) < reading (%v) < skimming (%v)",
			f.Reading.Listening.WPM, f.Reading.Reading.WPM, f.Reading.Skimming.WPM)
	}

	// stream_bars: each names a valid anchor, and the grades must rise the
	// same way the anchors they name do (excellent's anchor no slower than
	// good's, good's no slower than usable's) — the arithmetic a "worse of
	// the two" grade depends on.
	bars := map[string]StreamBar{"excellent": f.StreamBars.Excellent, "good": f.StreamBars.Good, "usable": f.StreamBars.Usable}
	rank := map[string]int{}
	for name, bar := range bars {
		if err := bar.check("stream_bars."+name, f.Sources); err != nil {
			return err
		}
		r := f.Reading.rank(bar.Anchor)
		if r < 0 {
			return bad("stream_bars.%s: anchor %q is not listening, reading or skimming", name, bar.Anchor)
		}
		rank[name] = r
	}
	if !(rank["excellent"] >= rank["good"] && rank["good"] >= rank["usable"]) {
		return bad("stream_bars must not read slower as the grade improves: excellent=%s, good=%s, usable=%s",
			bars["excellent"].Anchor, bars["good"].Anchor, bars["usable"].Anchor)
	}

	// Purposes: exactly one row per catalog.Purpose.
	seen := map[catalog.Purpose]bool{}
	for i := range f.Purposes {
		p := &f.Purposes[i]
		where := fmt.Sprintf("purposes[%d] (%s)", i, p.Purpose)
		if !p.Purpose.Valid() {
			return bad("%s: not a known purpose", where)
		}
		if seen[p.Purpose] {
			return bad("%s: purpose listed twice", where)
		}
		seen[p.Purpose] = true

		if !p.Mode.valid() {
			return bad("%s: mode %q is not read_along or per_step", where, p.Mode)
		}
		switch p.Mode {
		case ModeReadAlong:
			if p.PromptTokens == nil {
				return bad("%s: read_along needs prompt_tokens", where)
			}
			if p.StepPromptTokens != nil || p.StepOutputTokens != nil {
				return bad("%s: read_along does not take step_prompt_tokens or step_output_tokens", where)
			}
			if err := p.PromptTokens.check(where+".prompt_tokens", f.Sources); err != nil {
				return err
			}
			if p.ThinkingTokens != nil {
				if err := p.ThinkingTokens.check(where+".thinking_tokens", f.Sources); err != nil {
					return err
				}
			}
		case ModePerStep:
			if p.StepPromptTokens == nil || p.StepOutputTokens == nil {
				return bad("%s: per_step needs step_prompt_tokens and step_output_tokens", where)
			}
			if p.PromptTokens != nil || p.ThinkingTokens != nil {
				return bad("%s: per_step does not take prompt_tokens or thinking_tokens", where)
			}
			if err := p.StepPromptTokens.check(where+".step_prompt_tokens", f.Sources); err != nil {
				return err
			}
			if err := p.StepOutputTokens.check(where+".step_output_tokens", f.Sources); err != nil {
				return err
			}
		}
		if err := p.WaitS.Excellent.check(where+".wait_s.excellent", f.Sources); err != nil {
			return err
		}
		if err := p.WaitS.Good.check(where+".wait_s.good", f.Sources); err != nil {
			return err
		}
		if err := p.WaitS.Usable.check(where+".wait_s.usable", f.Sources); err != nil {
			return err
		}
		if !(p.WaitS.Excellent.Value < p.WaitS.Good.Value && p.WaitS.Good.Value < p.WaitS.Usable.Value) {
			return bad("%s: wait_s must rise: excellent (%v) < good (%v) < usable (%v)",
				where, p.WaitS.Excellent.Value, p.WaitS.Good.Value, p.WaitS.Usable.Value)
		}
	}
	for _, p := range catalog.Purposes {
		if !seen[p] {
			return bad("purposes is missing a row for %q", p)
		}
	}
	return nil
}

// WordsPerToken turns tokens into the words a person reads (reasons.go).
func (sn *SpeedNeeds) WordsPerToken() float64 { return sn.file.WordsPerToken }

// Purpose looks a row up by catalog.Purpose. ok is false only for a purpose
// the table has never heard of — validation guarantees every catalog.Purpose
// has a row, so this should not happen with the embedded file.
func (sn *SpeedNeeds) Purpose(p catalog.Purpose) (PurposeSpeedNeed, bool) {
	for _, row := range sn.file.Purposes {
		if row.Purpose == p {
			return row, true
		}
	}
	return PurposeSpeedNeed{}, false
}

// StreamRate is the tokens-a-second an answer must reach to earn grade for a
// read_along purpose — the same three numbers for every one of them, since
// stream_bars names reading anchors, not purposes. ok is false for
// GradeTooSlow, which has no rate of its own (below usable).
func (sn *SpeedNeeds) StreamRate(g Grade) (float64, bool) {
	var bar StreamBar
	switch g {
	case GradeExcellent:
		bar = sn.file.StreamBars.Excellent
	case GradeGood:
		bar = sn.file.StreamBars.Good
	case GradeUsable:
		bar = sn.file.StreamBars.Usable
	default:
		return 0, false
	}
	anchor, ok := sn.file.Reading.byName(bar.Anchor)
	if !ok {
		return 0, false
	}
	return anchor.TokS(sn.file.WordsPerToken), true
}

// streamGrade grades a read_along purpose's generation speed (tokens a
// second) against the stream bars.
func (sn *SpeedNeeds) streamGrade(genTPS float64) Grade {
	if hi, ok := sn.StreamRate(GradeExcellent); ok && genTPS >= hi {
		return GradeExcellent
	}
	if g, ok := sn.StreamRate(GradeGood); ok && genTPS >= g {
		return GradeGood
	}
	if u, ok := sn.StreamRate(GradeUsable); ok && genTPS >= u {
		return GradeUsable
	}
	return GradeTooSlow
}

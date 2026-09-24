package figure

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Public is a number about a model that someone else published — a
// benchmark score, a leaderboard rating — never a number about this machine
// (research/EXTERNAL_SOURCES.md, P-1). It is the third kind of number the
// advisor shows, and it has its own type so that it cannot be mistaken for
// the other two:
//
//   - it has no Source: it is neither estimated nor measured here, and its
//     JSON has no "source" key at all, so the UI's <Figure> — whose props
//     require one — cannot render it (ui/src/components/PublicFigure.tsx
//     does, and nothing else);
//   - it always carries its Origin — who published it, the source's own
//     date, the credit its licence asks for — and refuses to marshal without
//     them, as Source refuses its zero value;
//   - no API struct holds a Public beside a Bytes or a Rate
//     (CheckSeparation; TestPublicAndLocalNeverShareAStruct): public and
//     local numbers sit in sibling sub-objects, "public" and "local".
//
// Value is the number as the source published it, on Scale; the default
// view shows a position in words, the raw value only under Advanced (P-5).
type Public struct {
	Value  float64 `json:"value"`
	Scale  string  `json:"scale"` // what Value is measured in: "rating", "percent", ...
	Origin Origin  `json:"origin"`
}

// Origin is where a public value came from. It is shown directly beneath
// every public value, not behind a tap (P-4).
type Origin struct {
	// Publisher is who published the value, in words: "Arena (arena.ai)",
	// "Epoch AI", "Qwen on Hugging Face". Named publisher, not source, so a
	// public value's JSON has no "source" key for a local figure's to match.
	Publisher string `json:"publisher"`
	// URL is where a person can read the value at its source.
	URL string `json:"url,omitempty"`
	// Date is the source's own date for the value (YYYY-MM-DD): the
	// leaderboard's or the evaluation's, never the day the advisor fetched it.
	Date string `json:"date"`
	// Licence is what the value is published under ("CC-BY-4.0").
	Licence string `json:"licence"`
	// Attribution is the credit the licence asks for, as it must be shown.
	Attribution string     `json:"attribution"`
	Provenance  Provenance `json:"provenance"`
}

// Provenance is who produced a public value, which decides how it is
// labelled and whether it may be scored (P-4; the recommendation engine
// scores verified, independent and crowd values, never the maker's own).
type Provenance string

const (
	// ProvenanceMaker: the model's maker reported it about its own model.
	ProvenanceMaker Provenance = "maker"
	// ProvenanceVerified: Hugging Face verified the run as reproducible.
	ProvenanceVerified Provenance = "verified"
	// ProvenanceIndependent: an independent evaluator ran it (Epoch AI).
	ProvenanceIndependent Provenance = "independent"
	// ProvenanceCrowd: people comparing answers rated it (Arena).
	ProvenanceCrowd Provenance = "crowd"
)

// Valid reports whether p is one of the four provenances.
func (p Provenance) Valid() bool {
	switch p {
	case ProvenanceMaker, ProvenanceVerified, ProvenanceIndependent, ProvenanceCrowd:
		return true
	}
	return false
}

// Scorable reports whether the recommendation engine may score a value of
// this provenance (the maker's own numbers are shown, labelled, not scored).
func (p Provenance) Scorable() bool {
	return p == ProvenanceVerified || p == ProvenanceIndependent || p == ProvenanceCrowd
}

// ErrIncompleteOrigin is returned when a public value is serialised without
// the origin it must be shown with.
var ErrIncompleteOrigin = errors.New("figure: a public value needs its publisher, its source's date, its attribution and a valid provenance")

// Check reports what the origin is missing; nil when it is complete.
func (o Origin) Check() error {
	var missing []string
	if strings.TrimSpace(o.Publisher) == "" {
		missing = append(missing, "publisher")
	}
	if strings.TrimSpace(o.Date) == "" {
		missing = append(missing, "date")
	}
	if strings.TrimSpace(o.Attribution) == "" {
		missing = append(missing, "attribution")
	}
	if !o.Provenance.Valid() {
		missing = append(missing, fmt.Sprintf("provenance (got %q)", string(o.Provenance)))
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w (missing %s)", ErrIncompleteOrigin, strings.Join(missing, ", "))
	}
	return nil
}

// MarshalJSON refuses a public value whose origin is incomplete: a number
// from someone else must never reach the screen without saying whose it is.
func (p Public) MarshalJSON() ([]byte, error) {
	if err := p.Origin.Check(); err != nil {
		return nil, err
	}
	type plain Public
	return json.Marshal(plain(p))
}

// UnmarshalJSON refuses the same.
func (p *Public) UnmarshalJSON(b []byte) error {
	type plain Public
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	if err := v.Origin.Check(); err != nil {
		return err
	}
	*p = Public(v)
	return nil
}

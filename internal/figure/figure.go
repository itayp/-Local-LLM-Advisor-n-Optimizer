// Package figure is where product rule 4 lives in the type system:
//
//	Estimated is never dressed as measured. Two visual treatments, everywhere,
//	always. A measurement replaces an estimate the moment it exists.
//
// A figure is a number a user will see. Every figure carries a Source, and a
// Source is either Estimated or Measured — there is no third value and no
// zero value that serialises quietly. Marshalling a figure whose Source is
// unset fails, so a forgotten provenance is a failing test or a 500, never a
// number on screen that looks measured.
//
// The rule for API types is mechanical: a numeric field a user will see is a
// figure type from this package. Numeric fields that are neither estimated
// nor measured — ids, counts, timestamps, configuration such as num_ctx,
// values read straight from the OS — carry the struct tag `source:"n/a"`
// and say why in a comment. figurecheck (see check.go) walks every API type
// and fails on anything else.
//
// A third kind of number arrived with build-plan step 9b: Public (public.go),
// a value about a model that someone else published. It is neither
// estimated nor measured and has no Source; it carries its Origin instead,
// and CheckSeparation keeps it out of every struct that holds a Bytes or a
// Rate (research/EXTERNAL_SOURCES.md, the display rule).
package figure

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Source says where a number came from. It is the JSON field "source" on
// every figure.
type Source string

const (
	// Estimated: arithmetic over metadata (the estimator, the speed model).
	Estimated Source = "estimated"
	// Measured: read from this machine while it ran (a benchmark, a VRAM
	// reading after a load).
	Measured Source = "measured"
)

// ErrInvalidSource is returned when a figure is serialised or parsed with a
// Source that is neither Estimated nor Measured.
var ErrInvalidSource = errors.New("figure: source must be \"estimated\" or \"measured\"")

// Valid reports whether s is one of the two allowed values.
func (s Source) Valid() bool { return s == Estimated || s == Measured }

// MarshalJSON refuses to serialise an invalid Source. This is deliberate:
// the zero value must not reach the UI.
func (s Source) MarshalJSON() ([]byte, error) {
	if !s.Valid() {
		return nil, fmt.Errorf("%w (got %q)", ErrInvalidSource, string(s))
	}
	return json.Marshal(string(s))
}

// UnmarshalJSON refuses to parse an invalid Source.
func (s *Source) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	if !Source(v).Valid() {
		return fmt.Errorf("%w (got %q)", ErrInvalidSource, v)
	}
	*s = Source(v)
	return nil
}

// Bytes is a memory or disk size a user will see, with its provenance.
type Bytes struct {
	Value  uint64 `json:"value"`
	Source Source `json:"source"`
}

// EstimatedBytes builds an estimated Bytes figure.
func EstimatedBytes(v uint64) Bytes { return Bytes{Value: v, Source: Estimated} }

// MeasuredBytes builds a measured Bytes figure.
func MeasuredBytes(v uint64) Bytes { return Bytes{Value: v, Source: Measured} }

// Rate is a throughput a user will see — tokens per second, most often.
//
// An estimate is a range (Low..High) because the speed model is honest about
// being a model; a measurement is a point (Low == High == Value). The UI
// renders the two differently and never has to guess which it has.
type Rate struct {
	Value  float64 `json:"value"` // the point value, or the midpoint of a range
	Low    float64 `json:"low"`   // == Value for a measurement
	High   float64 `json:"high"`  // == Value for a measurement
	Unit   string  `json:"unit"`  // "tok/s", "ms", ...
	Source Source  `json:"source"`
}

// EstimatedRange builds an estimated Rate spanning low..high.
func EstimatedRange(low, high float64, unit string) Rate {
	if high < low {
		low, high = high, low
	}
	return Rate{Value: (low + high) / 2, Low: low, High: high, Unit: unit, Source: Estimated}
}

// MeasuredRate builds a measured point Rate.
func MeasuredRate(v float64, unit string) Rate {
	return Rate{Value: v, Low: v, High: v, Unit: unit, Source: Measured}
}

// IsRange reports whether the figure spans more than a point.
func (r Rate) IsRange() bool { return r.Low != r.High }

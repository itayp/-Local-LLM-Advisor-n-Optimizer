package figure

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSourceRefusesZeroValue(t *testing.T) {
	// The whole point of the package: a figure without provenance cannot
	// become JSON.
	_, err := json.Marshal(Bytes{Value: 1})
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("marshal of unset source: got err %v, want ErrInvalidSource", err)
	}
	_, err = json.Marshal(Rate{Value: 1, Unit: "tok/s"})
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("marshal of unset Rate source: got err %v, want ErrInvalidSource", err)
	}
}

func TestSourceRoundTrip(t *testing.T) {
	b, err := json.Marshal(MeasuredBytes(42))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"value":42,"source":"measured"}` {
		t.Fatalf("unexpected JSON %s", b)
	}
	var back Bytes
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back != MeasuredBytes(42) {
		t.Fatalf("round trip lost data: %+v", back)
	}

	var bad Bytes
	err = json.Unmarshal([]byte(`{"value":1,"source":"guessed"}`), &bad)
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("unmarshal of invalid source: got %v, want ErrInvalidSource", err)
	}
}

func TestRateShapes(t *testing.T) {
	r := EstimatedRange(50, 40, "tok/s") // reversed on purpose
	if r.Low != 40 || r.High != 50 || r.Value != 45 || !r.IsRange() || r.Source != Estimated {
		t.Fatalf("EstimatedRange: %+v", r)
	}
	m := MeasuredRate(51.2, "tok/s")
	if m.IsRange() || m.Low != 51.2 || m.High != 51.2 || m.Source != Measured {
		t.Fatalf("MeasuredRate: %+v", m)
	}
}

type okType struct {
	Name    string
	Size    Bytes
	Speed   Rate
	ID      int64   `source:"n/a"` // an id
	Count   int     `source:"n/a"` // a count
	Nested  []inner // walked recursively
	Pointer *inner
	Lookup  map[string]inner
}

type inner struct {
	Ctx  int `source:"n/a"` // configuration
	Mem  Bytes
	Rate *Rate
}

type badType struct {
	Name      string
	VRAMBytes uint64   // bare number, no tag: must be reported
	TokPerSec float64  // same
	Maybe     *float64 // a pointer to a number is still a number
	Series    []uint64 // so is a slice of them
	ByName    map[string]int
	Inner     struct {
		Deep int32 // and nested
	}
}

func TestCheckAcceptsFiguresAndTaggedFields(t *testing.T) {
	if problems := Check(okType{}, &okType{}, []okType{}); len(problems) != 0 {
		t.Fatalf("expected no problems, got:\n%s", strings.Join(problems, "\n"))
	}
}

func TestCheckReportsBareNumbers(t *testing.T) {
	problems := Check(badType{})
	want := []string{"badType.VRAMBytes", "badType.TokPerSec", "badType.Maybe", "badType.Series", "badType.ByName", "badType.Inner.Deep"}
	for _, w := range want {
		found := false
		for _, p := range problems {
			if strings.HasPrefix(p, "figure."+w+" ") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a problem for %s; got:\n%s", w, strings.Join(problems, "\n"))
		}
	}
	if len(problems) != len(want) {
		t.Errorf("expected exactly %d problems, got %d:\n%s", len(want), len(problems), strings.Join(problems, "\n"))
	}
}

func TestPublicRefusesAnIncompleteOrigin(t *testing.T) {
	good := Public{Value: 1234, Scale: "rating", Origin: Origin{
		Publisher: "Arena (arena.ai)", Date: "2026-09-15", Licence: "CC-BY-4.0",
		Attribution: "Arena leaderboard dataset", Provenance: ProvenanceCrowd,
	}}
	b, err := json.Marshal(good)
	if err != nil {
		t.Fatalf("a complete public value must marshal: %v", err)
	}
	if strings.Contains(string(b), `"source"`) {
		t.Fatalf("a public value must have no \"source\" key, so <Figure> cannot take it: %s", b)
	}
	var back Public
	if err := json.Unmarshal(b, &back); err != nil || back != good {
		t.Fatalf("round trip: %v %+v", err, back)
	}
	for name, mutate := range map[string]func(*Public){
		"no publisher":   func(p *Public) { p.Origin.Publisher = "" },
		"no date":        func(p *Public) { p.Origin.Date = " " },
		"no attribution": func(p *Public) { p.Origin.Attribution = "" },
		"no provenance":  func(p *Public) { p.Origin.Provenance = "" },
		"bad provenance": func(p *Public) { p.Origin.Provenance = "measured" },
	} {
		p := good
		mutate(&p)
		if _, err := json.Marshal(p); !errors.Is(err, ErrIncompleteOrigin) {
			t.Errorf("%s: marshal = %v, want ErrIncompleteOrigin", name, err)
		}
		raw, _ := json.Marshal(struct {
			Value  float64 `json:"value"`
			Origin Origin  `json:"origin"`
		}{p.Value, p.Origin})
		var q Public
		if err := json.Unmarshal(raw, &q); !errors.Is(err, ErrIncompleteOrigin) {
			t.Errorf("%s: unmarshal = %v, want ErrIncompleteOrigin", name, err)
		}
	}
}

func TestPublicIsAProvenanceCarryingType(t *testing.T) {
	type block struct {
		Values []Public `json:"values"`
		Count  int      `json:"count" source:"n/a"` // a count
	}
	if p := Check(block{}); len(p) != 0 {
		t.Fatalf("Check must accept Public as a figure type: %v", p)
	}
}

func TestCheckSeparation(t *testing.T) {
	type publicBlock struct{ Values []Public }
	type localBlock struct{ Speed *Rate }
	type siblings struct {
		Public publicBlock
		Local  localBlock
	}
	type mixed struct {
		Speed  Rate
		Scores []Public
	}
	type nested struct{ Inner *mixed }
	if p := CheckSeparation(siblings{}); len(p) != 0 {
		t.Fatalf("sibling sub-objects must pass: %v", p)
	}
	if p := CheckSeparation(nested{}); len(p) != 1 || !strings.Contains(p[0], "mixed") {
		t.Fatalf("a struct holding both must be reported, however deep: %v", p)
	}
	if !ProvenanceCrowd.Scorable() || ProvenanceMaker.Scorable() {
		t.Fatal("the maker's own numbers are shown, not scored")
	}
}

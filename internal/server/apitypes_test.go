package server

import (
	"strings"
	"testing"

	"advisor/internal/figure"
)

// TestEveryUserFacingNumberHasASource is product rule 4 as a build gate:
// every numeric field in every API type is a figure (with its source) or is
// explicitly tagged `source:"n/a"` with a comment saying why.
func TestEveryUserFacingNumberHasASource(t *testing.T) {
	problems := figure.Check(APITypes()...)
	if len(problems) > 0 {
		t.Fatalf("%d numeric API field(s) without provenance:\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
}

// TestPublicAndLocalNeverShareAStruct is the display rule's first half as a
// build gate (research/EXTERNAL_SOURCES.md P-2): no API struct holds a
// public value (figure.Public) beside an estimate or a measurement
// (figure.Bytes, figure.Rate); they sit in sibling sub-objects.
func TestPublicAndLocalNeverShareAStruct(t *testing.T) {
	if problems := figure.CheckSeparation(APITypes()...); len(problems) > 0 {
		t.Fatalf("%d API struct(s) mix public and local numbers:\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
}

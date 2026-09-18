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

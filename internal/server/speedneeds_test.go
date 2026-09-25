package server

import (
	"net/http"
	"testing"

	"advisor/internal/catalog"
	"advisor/internal/recommend"
)

// TestSpeedNeedsServesTheCuratedTable: GET /api/speed-needs is the same
// embedded, validated table recommend.DefaultSpeedNeeds parses (D-58) — one
// row per catalog.Purpose, a stream bar only for a read_along purpose, and
// a words_per_token the UI needs to turn tok/s into words a second.
func TestSpeedNeedsServesTheCuratedTable(t *testing.T) {
	ts := newTestServer(t)
	var resp SpeedNeedsResponse
	getJSON(t, ts.URL+"/api/speed-needs", 200, &resp)

	if resp.WordsPerToken <= 0 {
		t.Fatalf("words_per_token = %v, want > 0", resp.WordsPerToken)
	}
	if len(resp.Purposes) != len(catalog.Purposes) {
		t.Fatalf("purposes = %d, want %d (one per catalog.Purpose)", len(resp.Purposes), len(catalog.Purposes))
	}

	seen := map[catalog.Purpose]bool{}
	for _, row := range resp.Purposes {
		seen[row.Purpose] = true
		if !(row.Wait.Excellent < row.Wait.Good && row.Wait.Good < row.Wait.Usable) {
			t.Errorf("%s: wait_s does not rise: %+v", row.Purpose, row.Wait)
		}
		switch row.Mode {
		case recommend.ModeReadAlong:
			if row.Stream == nil {
				t.Errorf("%s: read_along without a stream bar", row.Purpose)
				continue
			}
			if !(row.Stream.Excellent >= row.Stream.Good && row.Stream.Good >= row.Stream.Usable) {
				t.Errorf("%s: stream does not fall as the grade drops: %+v", row.Purpose, row.Stream)
			}
		case recommend.ModePerStep:
			if row.Stream != nil {
				t.Errorf("%s: per_step has a stream bar, want none", row.Purpose)
			}
		default:
			t.Errorf("%s: mode %q is neither read_along nor per_step", row.Purpose, row.Mode)
		}
	}
	for _, p := range catalog.Purposes {
		if !seen[p] {
			t.Errorf("missing a row for %q", p)
		}
	}
}

func TestSpeedNeedsWrongMethodIs405(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/speed-needs", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/speed-needs = %d, want 405", resp.StatusCode)
	}
}

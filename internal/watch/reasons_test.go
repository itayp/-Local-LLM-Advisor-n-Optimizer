package watch

import (
	"strings"
	"testing"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/figure"
	"advisor/internal/recommend"
)

func testRec() recommend.Recommendation {
	return recommend.Recommendation{
		Model:       catalog.Model{ID: 1},
		DisplayName: "Qwen3.5 8B",
		Reasons: []recommend.Reason{
			{Kind: "fit", Text: "Fits your 16 GB graphics card with room to spare."},
			{Kind: "speed", Text: "Estimated to answer at roughly 45 to 55 words a second — about as fast as you read."},
			{Kind: "size", Text: "About 5 GB to download."},
			{Kind: "change", Text: "Compared with Llama 3 8B, which you have: about twice the size, so noticeably more capable."},
		},
		VersusCurrent: "Compared with Llama 3 8B, which you have: about twice the size, so noticeably more capable.",
		Speed:         &figure.Rate{Low: 45, High: 55, Unit: "tok/s", Source: figure.Estimated},
	}
}

// TestNotificationForNeverDuplicatesTheVersusSentence locks in the fix for
// the bug PRD §12's example caught: recommend/reasons.go's card() already
// puts the versus-current sentence into Reasons as a "change" reason
// (item 6) AND sets VersusCurrent to the same string — a notification that
// read both said it twice.
func TestNotificationForNeverDuplicatesTheVersusSentence(t *testing.T) {
	rec := testRec()
	note := notificationFor(rec, nil, "", time.Now())

	want := "Compared with Llama 3 8B, which you have: about twice the size, so noticeably more capable."
	if n := strings.Count(note.Body, want); n != 1 {
		t.Fatalf("versus-current sentence appears %d times, want 1:\n%s", n, note.Body)
	}
	// And every other reason still made it in, once each.
	for _, want := range []string{"Fits your 16 GB graphics card with room to spare.", "About 5 GB to download."} {
		if strings.Count(note.Body, want) != 1 {
			t.Errorf("body missing %q:\n%s", want, note.Body)
		}
	}
}

// TestNotificationForOrdersLikePRD12 checks the bullet order PRD §12's
// example shows: what it fits and the rest of the plain reasons, then the
// public-data bullet, then what it changes, then the speed lines.
func TestNotificationForOrdersLikePRD12(t *testing.T) {
	rec := testRec()
	rec.Public = &catalog.PublicEntry{
		Scored: true, Position: "Among the strongest for coding of 9 models here that Arena has rated.",
		Purposes: []catalog.Purpose{catalog.PurposeCoding},
	}
	note := notificationFor(rec, nil, "", time.Now())

	fit := strings.Index(note.Body, "Fits your 16 GB graphics card")
	public := strings.Index(note.Body, "Strong coding benchmark results")
	change := strings.Index(note.Body, "Compared with Llama 3 8B")
	speed := strings.Index(note.Body, "Estimated 45–55 tok/s")
	if fit < 0 || public < 0 || change < 0 || speed < 0 {
		t.Fatalf("a bullet is missing entirely:\n%s", note.Body)
	}
	if !(fit < public && public < change && change < speed) {
		t.Fatalf("bullets are out of PRD §12's order (fit, public, change, speed):\n%s", note.Body)
	}
}

func TestPublicBullet(t *testing.T) {
	tests := []struct {
		name string
		e    *catalog.PublicEntry
		want string
	}{
		{"nil entry", nil, ""},
		{"not scored (the maker's own numbers)", &catalog.PublicEntry{
			Scored: false, Position: "Among the strongest for coding of 9 models here that Arena has rated.",
			Purposes: []catalog.Purpose{catalog.PurposeCoding},
		}, ""},
		{"middle of the pack", &catalog.PublicEntry{
			Scored: true, Position: "In the middle for coding of 9 models here that Arena has rated.",
			Purposes: []catalog.Purpose{catalog.PurposeCoding},
		}, ""},
		{"too few rated to have thirds", &catalog.PublicEntry{
			Scored: true, Position: "1st for coding of 4 models here that Arena has rated.",
			Purposes: []catalog.Purpose{catalog.PurposeCoding},
		}, ""},
		{"top third, coding", &catalog.PublicEntry{
			Scored: true, Position: "Among the strongest for coding of 9 models here that Arena has rated.",
			Purposes: []catalog.Purpose{catalog.PurposeCoding},
		}, "Strong coding benchmark results"},
		{"top third, a purpose with no short word yet", &catalog.PublicEntry{
			Scored: true, Position: "Among the strongest for everyday chat of 9 models here that Arena has rated.",
			Purposes: []catalog.Purpose{catalog.Purpose("made_up")},
		}, "Strong benchmark results"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := publicBullet(tc.e); got != tc.want {
				t.Errorf("publicBullet() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNotificationForOmitsThePublicBulletWithNothingInstalled makes sure
// the public bullet still shows with no current model to compare with
// (VersusCurrent == "", no "change" reason at all) — the loop skipping
// "change" reasons must not also skip the public bullet.
func TestNotificationForOmitsThePublicBulletWithNothingInstalled(t *testing.T) {
	rec := testRec()
	rec.Reasons = rec.Reasons[:len(rec.Reasons)-1] // drop the "change" reason
	rec.VersusCurrent = ""
	rec.Public = &catalog.PublicEntry{
		Scored: true, Position: "Among the strongest for coding of 9 models here that Arena has rated.",
		Purposes: []catalog.Purpose{catalog.PurposeCoding},
	}
	note := notificationFor(rec, nil, "", time.Now())
	if !strings.Contains(note.Body, "Strong coding benchmark results") {
		t.Fatalf("body missing the public bullet with nothing installed:\n%s", note.Body)
	}
	if strings.Contains(note.Body, "Compared with") {
		t.Fatalf("body has a versus sentence with nothing installed:\n%s", note.Body)
	}
}

package watch

import (
	"fmt"
	"math"
	"strings"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/figure"
	"advisor/internal/recommend"
)

// notificationFor builds the desktop notification for a candidate that
// qualified: a recommendation the engine produced for the user's own
// purposes, that changes something from the model they have (or, with
// nothing installed, simply made the cut). Every line but one is a sentence
// recommend already wrote for the Recommend screen (product rule 3 and
// CLAUDE.md's "what the customer reads is templated" both live in
// internal/recommend/reasons.go) — this function arranges them into PRD
// §12's shape, "Why it may matter to you:" and a bullet per reason, and adds
// nothing of its own but the title, the public-data bullet (publicBullet,
// below) and the speed lines' numbers.
//
// rec.Reasons already carries a "change" reason with this same text as
// rec.VersusCurrent whenever there is a current model to compare with
// (recommend/reasons.go's card(), item 6) — the loop below skips it and
// rec.VersusCurrent is added once, on its own, after the public bullet, so
// PRD §12's order holds (what it fits, what public data says, what it
// changes, how fast) without saying the same sentence twice.
func notificationFor(rec recommend.Recommendation, cur *recommend.Current, url string, now time.Time) Notification {
	var lines []string
	for _, r := range rec.Reasons {
		if r.Kind == "change" || strings.TrimSpace(r.Text) == "" {
			continue
		}
		lines = append(lines, r.Text)
	}
	if b := publicBullet(rec.Public); b != "" {
		lines = append(lines, b)
	}
	if rec.VersusCurrent != "" {
		lines = append(lines, rec.VersusCurrent)
	}
	if rec.Speed != nil {
		lines = append(lines, "Estimated "+formatRate(*rec.Speed))
	}
	if cur != nil && cur.Estimate != nil && cur.Estimate.Speed.Generation != nil {
		lines = append(lines, "Your current model ("+cur.Name+"): "+formatRate(*cur.Estimate.Speed.Generation))
	}

	var body strings.Builder
	body.WriteString("Why it may matter to you:")
	for _, l := range lines {
		body.WriteString("\n• " + l)
	}
	if url != "" {
		body.WriteString("\n\nRun benchmark: " + url)
	}

	return Notification{
		Recommendation: rec,
		Title:          "New model: " + rec.DisplayName,
		Body:           body.String(),
		URL:            url,
		CreatedAt:      now,
	}
}

// formatRate renders a speed figure as plain text for a desktop
// notification, which has no room for the UI's own <Figure> rendering: "45
// tok/s" for a measurement or a range collapsed to a point, "45–55
// tok/s" for a real range.
func formatRate(r figure.Rate) string {
	lo, hi := int(math.Round(r.Low)), int(math.Round(r.High))
	if lo == hi {
		return fmt.Sprintf("%d %s", lo, r.Unit)
	}
	return fmt.Sprintf("%d–%d %s", lo, hi, r.Unit)
}

// publicPurposeWords names a purpose the short way a notification bullet
// wants ("Strong coding benchmark results") — plainer than the sentences
// recommend/reasons.go's own purposeWords builds full reasons from, and
// plainer still than external's "for coding"/"at reading pictures" forms,
// which read naturally only inside external.position()'s own sentence.
var publicPurposeWords = map[catalog.Purpose]string{
	catalog.PurposeCoding:      "coding",
	catalog.PurposeChat:        "chat",
	catalog.PurposeReasoning:   "reasoning",
	catalog.PurposeLongContext: "long-context",
	catalog.PurposeVision:      "vision",
	catalog.PurposeAgentic:     "agentic",
	catalog.PurposeWriting:     "writing",
}

// publicBullet is PRD §12's "Strong coding benchmark results" line. It
// never claims more than the model's own "Public data" block already would
// (P-7): e is nil for a size with nothing public, e.Scored is false for the
// maker's own numbers (never scored, never bulleted — P-4), and the bar for
// "strong" is the exact one external.position() already draws for that
// block's own "Among the strongest" sentence — a top-third finish among the
// sizes the source has rated, only once there are enough of them to have
// thirds. Reusing that wording, rather than the rank and count Rank/Rated
// carry, keeps the two places a person could read this claim — the
// notification and the model's own Details — saying it exactly the same
// way, from one place that decides it (public.go's position).
func publicBullet(e *catalog.PublicEntry) string {
	if e == nil || !e.Scored || !strings.HasPrefix(e.Position, "Among the strongest") {
		return ""
	}
	if len(e.Purposes) > 0 {
		if word, ok := publicPurposeWords[e.Purposes[0]]; ok {
			return "Strong " + word + " benchmark results"
		}
	}
	return "Strong benchmark results"
}

// suppressedReason says, in words, why a candidate that was checked did not
// qualify — the watch log's "what was suppressed and why" (build-plan step
// 10, item 4). It never claims more than the engine's own result: when the
// engine had nothing to say for the whole machine (no catalogue yet, memory
// unreadable, nothing fits at all), that sentence is reused as-is.
func suppressedReason(res recommend.Result, cur *recommend.Current) string {
	if res.Empty != "" {
		return res.Empty
	}
	if cur != nil {
		return "not a real change from " + cur.Name + " for the purposes you picked"
	}
	return "does not make the top picks for the purposes you picked, on this computer"
}

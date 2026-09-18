// Package recommend turns a profile, the user's purposes and the catalogue
// into at most three recommendations, each with reasons written for the
// customer (product rule 3). Step 1: types only. Step 5 implements Recommend
// and its scoring config.
//
// The advisor calls no LLM to do this job: recommendation is rules over data.
// If a step finds itself wanting a model to decide, the catalogue is missing
// a field — add the field.
package recommend

import (
	"advisor/internal/catalog"
	"advisor/internal/estimate"
	"advisor/internal/figure"
)

// Confidence is derived from which inputs were measured, estimated or
// unknown (PRD §21, last risk). The UI shows it on every card.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Preferences are the user's standing choices that shape a recommendation.
type Preferences struct {
	// CurrentModel is the installed model the user runs today, if any. Every
	// recommendation then says what it changes versus that model, or is
	// dropped.
	CurrentModel string `json:"current_model,omitempty"`
	// MinContext is the smallest context the user needs (0: no preference).
	MinContext int `json:"min_context" source:"n/a"`
	// GPUOnly: prefer configurations with no CPU offload.
	GPUOnly bool `json:"gpu_only"`
}

// Reason is one line of the "why", written for someone who does not know
// what a KV cache is: "fits your 16 GB graphics card with room for long
// documents", "about 5 GB to download".
type Reason struct {
	Text string `json:"text"`
	// Kind lets the UI pick an icon and the glossary entry: fit | size |
	// speed | purpose | change | warning.
	Kind string `json:"kind"`
}

// Recommendation is one card.
type Recommendation struct {
	Model    catalog.Model     `json:"model"`
	File     catalog.File      `json:"file"`
	NumCtx   int               `json:"num_ctx" source:"n/a"` // the context to run it at; configuration
	Estimate estimate.Estimate `json:"estimate"`
	// Download is what it costs to get it — the blob a beginner would pull.
	// The size is a fact from the catalogue, shown as-is.
	DownloadBytes uint64 `json:"download_bytes" source:"n/a"`
	// Speed is the headline number on the card: the estimate, or the
	// measurement once one exists for this configuration on this profile.
	Speed      figure.Rate `json:"speed"`
	Reasons    []Reason    `json:"reasons"`
	Confidence Confidence  `json:"confidence"`
	// Score is internal ranking material; the UI shows the order, not the
	// number. Not a user-facing figure.
	Score float64 `json:"score" source:"n/a"`
	// VersusCurrent is what this changes compared with Preferences.CurrentModel,
	// in one sentence, when a current model exists.
	VersusCurrent string `json:"versus_current,omitempty"`
}

// Result is the API response for GET /api/recommend.
type Result struct {
	Purposes        []catalog.Purpose `json:"purposes"`
	Recommendations []Recommendation  `json:"recommendations"` // at most three
	// Warning is set when the honest answer needs a preface — "your graphics
	// card is not being used by Ollama; these are the numbers without it"
	// (weak hardware is a tier, not an error: product rule 6).
	Warning string `json:"warning,omitempty"`
}

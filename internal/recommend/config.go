package recommend

import (
	"advisor/internal/catalog"
	"advisor/internal/estimate"
)

// Config holds every weight and threshold the recommendation engine uses.
//
//	score = purposeFit^Purpose × fitFactor^Fit × speedFactor^Speed × sizeFactor^Size
//
// A product, so that a zero anywhere is a veto (a model that does not serve
// the purpose, or does not fit, is not rescued by being fast), with the
// weights as exponents, so that raising one makes its factor bite harder.
//
// Unlike estimate.Config, nothing here can be measured with an instrument:
// these are judgements about what a careful person would recommend. They
// are CHOSEN, each says what it encodes, and recommend_test.go holds the
// outcomes they have to produce on the fixture machines — the test of a
// change is whether the top pick for each fleet machine is still one Itay
// would give it (build-plan step 5, "Done when").
type Config struct {
	// MaxRecommendations: "at most three" (build-plan step 5). A beginner
	// shown ten has been shown nothing.
	MaxRecommendations int

	// Weights are the exponents of the four factors. Purpose, fit and size
	// are 1. Speed is 1.5: above ComfortableTPS the factor is 1 and the
	// exponent changes nothing, but below it — a model that answers slower
	// than its owner reads — being bigger is not worth it to a beginner, and
	// without the extra weight a 22 GB mixture-of-experts model at three
	// words a second out-scores a 2 GB model at reading speed on a laptop
	// with no graphics card.
	Weights Weights

	// ---- purpose fit -------------------------------------------------------

	// A family's purposes are listed most-credible first in families.yaml.
	// The first listed purpose scores 1, each later one PurposeRankStep less,
	// never below PurposeFloor; a purpose the family does not list scores 0.
	// The step is gentle on purpose: being listed at all is most of the
	// signal, and a family with many purposes should not be marked down for
	// the ones at the end of a long list. Several purposes asked for are
	// averaged, so a family that serves only one of two is held back, not
	// dropped.
	PurposeRankStep float64
	PurposeFloor    float64

	// PurposeContext is the context a purpose wants, in tokens. The engine
	// runs a model at the longest context up to this that still fits, and
	// scales purpose fit by sqrt(chosen ÷ wanted) when it has to go shorter —
	// a coding model that can only keep 4k tokens in mind is half the coding
	// model. Coding and reasoning want room for files and for thinking;
	// agents re-read tool output; "long documents" means what it says.
	PurposeContext map[catalog.Purpose]int

	// LongContextMin is the shortest context at which a model counts as
	// serving the long_context purpose at all: 32k tokens is roughly fifty
	// pages.
	LongContextMin int

	// ---- fit ---------------------------------------------------------------

	// FitFactor is how much each category is worth. Headroom is the full
	// mark; a tight fit is nearly as good; a model that spills onto the
	// processor is a last resort, because it is several times slower and
	// the speed factor already sees only part of that.
	FitFactor map[estimate.Category]float64

	// ---- speed -------------------------------------------------------------

	// ComfortableTPS is the generation speed at which faster stops mattering
	// to a person reading the answer: 10 tokens a second is about 7 words a
	// second, comfortably above reading speed. Below it the factor falls linearly,
	// all the way down: on a machine where everything is slow the ranking
	// then turns on speed, which is what sends small models to weak hardware
	// (product rule 6) instead of the largest one that happens to fit in
	// memory. SpeedFloor only keeps the factor from reaching zero, so that a
	// CPU-only laptop still gets a ranked list. The speed
	// scored is the geometric middle of the estimated range — the natural
	// centre of a range whose ends are a ratio apart, and the cautious one
	// where the range is wide (a processor whose memory modules the advisor
	// cannot see).
	ComfortableTPS float64
	SpeedFloor     float64
	// UnknownSpeedFactor is the factor when there is no speed estimate (the
	// card is not in gpus.yaml): neither rewarded nor vetoed — and the
	// confidence says low.
	UnknownSpeedFactor float64

	// ---- size --------------------------------------------------------------

	// Within what fits, a larger model is the more capable one; the size
	// factor is log2(1 + billions of effective parameters) ÷ log2(1 +
	// SizeReferenceBillions), capped at 1. Effective parameters are the total
	// for a dense model, the geometric mean of total and active for a
	// mixture of experts (the usual rule of thumb for what such a model is
	// "worth"), and the model card's effective count for Gemma's
	// per-layer-embedding sizes. This is the factor that keeps a 24 GB card
	// from being handed a 1B model. Until step 9b brings public quality
	// signals it is the only proxy for quality the catalogue carries.
	SizeReferenceBillions float64

	// ---- which file --------------------------------------------------------

	// DefaultQuants picks the one file of a size the engine recommends: the
	// first of these that the catalogue holds. The first two are what a size's
	// ollama_tag pulls — the download a beginner actually gets (Q4_K_M, or
	// MXFP4 for models released in it); the rest are fallbacks for a size
	// whose repo lacks the usual file, nearest in quality first. The MVP
	// recommends that file only: the Ollama adapter cannot pull another quant
	// from Hugging Face yet (backend.ErrUnsupportedSource), and choosing
	// quants automatically is PRD §18, Phase 2.
	DefaultQuants []string

	// OnePerFamily keeps the three cards from being three sizes of one model.
	OnePerFamily bool

	// ---- versus the model the user has -------------------------------------

	// A recommendation must say what it changes against the user's current
	// model, or be dropped. These are what counts as a change worth saying:
	// a size ratio (either way), a speed ratio (either way), a context at
	// least twice as long.
	NoticeableSizeRatio    float64
	NoticeableSpeedRatio   float64
	NoticeableContextRatio float64
}

// Weights are the exponents of the score's four factors.
type Weights struct {
	Purpose float64 `json:"purpose" source:"n/a"` // configuration
	Fit     float64 `json:"fit" source:"n/a"`     // configuration
	Speed   float64 `json:"speed" source:"n/a"`   // configuration
	Size    float64 `json:"size" source:"n/a"`    // configuration
}

// DefaultConfig returns the weights the advisor ships with.
func DefaultConfig() Config {
	return Config{
		MaxRecommendations: 3,
		Weights:            Weights{Purpose: 1, Fit: 1, Speed: 1.5, Size: 1},

		PurposeRankStep: 0.05,
		PurposeFloor:    0.6,
		PurposeContext: map[catalog.Purpose]int{
			catalog.PurposeChat:        8192,
			catalog.PurposeWriting:     8192,
			catalog.PurposeVision:      8192,
			catalog.PurposeCoding:      16384,
			catalog.PurposeReasoning:   16384,
			catalog.PurposeAgentic:     32768,
			catalog.PurposeLongContext: 65536,
		},
		LongContextMin: 32768,

		FitFactor: map[estimate.Category]float64{
			estimate.FitsWithHeadroom: 1.0,
			estimate.Fits:             0.9,
			estimate.NeedsCPUOffload:  0.35,
		},

		ComfortableTPS:     10,
		SpeedFloor:         0.02,
		UnknownSpeedFactor: 0.6,

		SizeReferenceBillions: 70,

		DefaultQuants: []string{"Q4_K_M", "MXFP4", "Q5_K_M", "Q6_K", "Q8_0", "IQ4_XS", "Q3_K_M"},
		OnePerFamily:  true,

		NoticeableSizeRatio:    1.4,
		NoticeableSpeedRatio:   1.3,
		NoticeableContextRatio: 2,
	}
}

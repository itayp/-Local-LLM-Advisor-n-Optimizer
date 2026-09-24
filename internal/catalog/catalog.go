// Package catalog is the curated model catalogue: a maintained list of
// trusted model families with purpose tags (data/catalog/families.yaml),
// with Hugging Face as the metadata source for each family's GGUF files.
//
// The pieces:
//
//	load.go      the YAML loader and its validation (strict: unknown keys are errors)
//	files.go     reading a Hugging Face repo's file list: quant labels, multi-part
//	             files, which files are weights and which are vision encoders
//	header.go    the catalogue's typed view of a parsed GGUF header
//	match.go     mapping a runtime's installed models to catalogue rows
//	gguf/        the GGUF header parser
//	hf/          the Hugging Face Hub client (listings, range reads, rate limits)
//	refresh/     Run: every size → its repo listing → a header read per tracked
//	             quant → catalog_models and catalog_files, never downloading
//	             weights; MapInstalled
//
// The file is data — the docs must never count it (product rule 8).
package catalog

import (
	"time"

	"advisor/internal/figure"
)

// Purpose is what a user wants local AI for. The enum is shared with the UI
// (ui/src/api/types.ts) and with recommend; keep the three in step
// (TestPurposesMatchTheUI checks the UI's copy).
type Purpose string

const (
	PurposeCoding      Purpose = "coding"
	PurposeChat        Purpose = "chat"
	PurposeReasoning   Purpose = "reasoning"
	PurposeLongContext Purpose = "long_context"
	PurposeVision      Purpose = "vision"
	PurposeAgentic     Purpose = "agentic"
	PurposeWriting     Purpose = "writing"
)

// Purposes lists every purpose, in the order the UI shows them.
var Purposes = []Purpose{
	PurposeCoding, PurposeChat, PurposeReasoning, PurposeLongContext,
	PurposeVision, PurposeAgentic, PurposeWriting,
}

// Valid reports whether p is one of Purposes.
func (p Purpose) Valid() bool {
	for _, q := range Purposes {
		if p == q {
			return true
		}
	}
	return false
}

// Catalogue is the whole of families.yaml.
type Catalogue struct {
	// Quants are the quant variants the advisor tracks, spelled as GGUF file
	// names spell them ("Q4_K_M"). A repo's other files are seen in its
	// listing but neither header-read nor stored: a beginner is choosing
	// between a handful of sizes, not the twenty-five a repo publishes.
	Quants   []string `yaml:"quants" json:"quants"`
	Families []Family `yaml:"families" json:"families"`
}

// Family is one curated model family as families.yaml describes it.
type Family struct {
	ID          string    `yaml:"id" json:"id"`
	DisplayName string    `yaml:"display_name" json:"display_name"`
	Maintainer  string    `yaml:"maintainer" json:"maintainer"`
	License     License   `yaml:"license" json:"license"`
	Purposes    []Purpose `yaml:"purposes" json:"purposes"`
	Sizes       []Size    `yaml:"sizes" json:"sizes"`
	ReviewedAt  string    `yaml:"reviewed_at" json:"reviewed_at"` // YYYY-MM-DD, when a person last checked this entry
	Source      string    `yaml:"source" json:"source"`           // where they checked it: the model card
	Notes       string    `yaml:"notes,omitempty" json:"notes,omitempty"`
}

// License is SPDX where possible, otherwise the name and where to read it.
type License struct {
	SPDX string `yaml:"spdx,omitempty" json:"spdx,omitempty"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	URL  string `yaml:"url,omitempty" json:"url,omitempty"`
}

// Size is one parameter-count variant of a family.
type Size struct {
	Parameters       uint64 `yaml:"parameters" json:"parameters" source:"n/a"`                                   // total parameter count, from the model card
	ActiveParameters uint64 `yaml:"active_parameters,omitempty" json:"active_parameters,omitempty" source:"n/a"` // used per token when fewer than all (MoE, per-layer embeddings), from the model card; 0 = all
	ContextLength    int    `yaml:"context_length" json:"context_length" source:"n/a"`                           // trained context, from the model card
	OllamaTag        string `yaml:"ollama_tag" json:"ollama_tag"`                                                // what a beginner would pull, e.g. "llama3.1:8b"
	HFRepo           string `yaml:"hf_repo" json:"hf_repo"`                                                      // the GGUF repo used for metadata
	// HFBaseRepo is the original model's repo as its maker published it —
	// where public benchmark results for this size live (build-plan step 9b)
	// and whose creation date is the size's release date.
	HFBaseRepo string `yaml:"hf_base_repo" json:"hf_base_repo"`
	// HFBaseSameAs are other repo names a GGUF repo's card may give as its
	// base_model for the SAME original weights — a repo the maker renamed
	// (Hugging Face redirects the old name), or the maker's BF16 copy of an
	// FP8 release. They only quiet the curator's base-model check; public
	// scores are still read from HFBaseRepo alone.
	HFBaseSameAs []string `yaml:"hf_base_same_as,omitempty" json:"hf_base_same_as,omitempty"`
	// OllamaQuant is the quant OllamaTag pulls, read by hand from the Ollama
	// library page; "" means the usual default (recommend.Config.DefaultQuants).
	OllamaQuant string `yaml:"ollama_quant,omitempty" json:"ollama_quant,omitempty"`
}

// Model is a catalogue row: one family size, persisted (catalog_models),
// with what the last refresh learned about it.
type Model struct {
	ID       int64  `json:"id" source:"n/a"`
	FamilyID string `json:"family_id"`
	Size     Size   `json:"size"`

	// Present is false once the size has left families.yaml; the row stays so
	// benchmarks and estimates that point at its files keep their history.
	Present bool `json:"present"`

	HFSHA             string `json:"hf_sha,omitempty"`                // the repo commit the last refresh read
	ParametersCounted uint64 `json:"parameters_counted" source:"n/a"` // Hugging Face's count from the tensor shapes; 0 when it gave none
	RefreshedAt       string `json:"refreshed_at,omitempty"`          // RFC 3339; "" = never resolved
	RefreshError      string `json:"refresh_error,omitempty"`         // why the last refresh of this size failed, in words
	// ReleasedAt is when the original model's repo was created on Hugging
	// Face (YYYY-MM-DD), read by the external refresh; "" = not read yet. For
	// display only: it is never scored.
	ReleasedAt string `json:"released_at,omitempty"`

	Files []File `json:"files"`
}

// FileRole says what a GGUF file in a model's repo is for.
type FileRole string

const (
	// RoleModel is the language model's weights: one row per tracked quant.
	RoleModel FileRole = "model"
	// RoleProjector is a multimodal model's separate vision (or audio)
	// encoder ("mmproj-*.gguf"), which a runtime loads beside the weights —
	// the extra memory reading an image costs.
	RoleProjector FileRole = "projector"
)

// File is one quant variant of a Model: a GGUF file whose header was read
// with a range request, never downloaded (catalog_files).
type File struct {
	ID       int64    `json:"id" source:"n/a"`
	ModelID  int64    `json:"model_id" source:"n/a"`
	Filename string   `json:"filename"` // path in the repo; part 1 for a multi-part file
	Role     FileRole `json:"role"`
	Quant    string   `json:"quant"` // Q4_K_M, Q8_0, ... as the file name spells it; "F16" etc. for a projector
	SHA      string   `json:"sha,omitempty"`

	Parts         int     `json:"parts" source:"n/a"`           // 1, or the N of "-00001-of-0000N.gguf"
	Bytes         uint64  `json:"bytes" source:"n/a"`           // file size from the Hub listing, summed across parts; a fact, not an estimate
	BitsPerWeight float64 `json:"bits_per_weight" source:"n/a"` // bytes × 8 / parameters; a property of the file

	// Present is false once the file has gone from the repo (or its quant
	// from the tracked list); the row stays for history.
	Present bool `json:"present"`

	Header GGUFHeader `json:"header"`
	// Layout is what each layer keeps in memory as the context grows, derived
	// from the header (layout.go) whenever a file is read from the store — it
	// is never stored, so a better reading needs no refresh. Empty for a
	// projector, and for a File built by hand; estimate derives it then.
	Layout    Layout    `json:"layout"`
	FetchedAt time.Time `json:"fetched_at"`
}

// GGUFHeader carries the metadata fields the estimator needs. Step 0's
// finding: attention.key_length, where a model states one, is the real head
// dimension — embedding_length / head_count merely usually equals it.
type GGUFHeader struct {
	Architecture          string `json:"architecture"`
	GGUFVersion           int    `json:"gguf_version" source:"n/a"`
	TensorCount           int    `json:"tensor_count" source:"n/a"`
	BlockCount            int    `json:"block_count" source:"n/a"`
	HeadCount             int    `json:"head_count" source:"n/a"`
	HeadCountKV           int    `json:"head_count_kv" source:"n/a"` // largest per-layer value when the file lists one per layer
	HeadCountKVStated     bool   `json:"head_count_kv_stated"`       // false: the file leaves it out and llama.cpp uses head_count
	KeyLength             int    `json:"key_length" source:"n/a"`    // 0 when the model does not state one
	ValueLength           int    `json:"value_length" source:"n/a"`  // 0 when the model does not state one
	EmbeddingLength       int    `json:"embedding_length" source:"n/a"`
	ContextLength         int    `json:"context_length" source:"n/a"`
	SlidingWindow         int    `json:"sliding_window" source:"n/a"`          // 0 when the architecture has none
	FullAttentionInterval int    `json:"full_attention_interval" source:"n/a"` // hybrid models: every Nth layer is attention; 0 = all layers
	FileType              int    `json:"file_type" source:"n/a"`               // general.file_type (llama_ftype); -1 when the file does not state it
	FileTypeName          string `json:"file_type_name"`                       // "Q4_K_M"; "" when not stated
	ExpertCount           int    `json:"expert_count" source:"n/a"`            // > 0 marks a MoE model
	ExpertUsedCount       int    `json:"expert_used_count" source:"n/a"`       // experts active per token
	HasVision             bool   `json:"has_vision"`                           // a projector / vision tower is present

	// Complete is true when the whole metadata section was read; false when
	// the read stopped at the tokenizer because everything above was already
	// in hand (the usual case — see ARCHITECTURE.md D-33).
	Complete bool `json:"complete"`
}

// External is one stored public value about a catalogue size
// (catalog_external): a benchmark score or a rating someone else published,
// read by internal/catalog/external. It is never stored in the same columns
// as a local measurement, and the API serves it only as a figure.Public
// inside a "public" block — never beside an estimate or a measurement (PRD
// §10, research/EXTERNAL_SOURCES.md P-1, P-2). It is not an API type.
type External struct {
	ID          int64
	Source      string // an approved source's id in data/catalog/external.yaml: hf_evals | arena | epoch
	SourceModel string // the source's own name for the model (a repo id, an Arena model name)
	ModelID     int64  // the catalogue size it maps to
	Metric      string // "hf:<dataset>/<task>", "arena:<subset>/<category>", "epoch:<benchmark>"
	Value       float64
	ValueText   string
	// SourceDate is the source's own date for the value — the evaluation's or
	// the leaderboard's, never the day the advisor fetched it (YYYY-MM-DD).
	SourceDate  string
	SourceURL   string
	Provenance  string // maker | verified | independent | crowd
	Attribution string // what the licence asks the advisor to say
	License     string
	Detail      map[string]any // bounds, votes, rank, board size, notes: shown under Advanced
	FetchedAt   string         // RFC 3339, when the advisor last read it
	Present     bool           // false once the value has left its source; kept, never shown or scored
}

// PublicMetric is one (source, metric) pair's values across the catalogue
// sizes, as the recommendation engine reads them: only the values that may
// be scored (verified, independent, crowd — never the maker's own), keyed
// by catalogue size id. internal/catalog/external builds it; recommend
// reads it without importing external.
type PublicMetric struct {
	Source         string
	Metric         string
	HigherIsBetter bool
	Values         map[int64]float64 // catalogue size id → value
}

// PublicEntry is one public value as a size's "Public data" block shows it
// (research/EXTERNAL_SOURCES.md P-3 to P-7): the size's position among the
// curated sizes the source has scored, in words, first; who published it,
// what it tested and the source's own date directly beneath; the raw value
// and its detail only under Advanced. It holds a figure.Public and nothing
// local: an estimate or a measurement never shares its struct (P-2).
type PublicEntry struct {
	SourceID   string    `json:"source_id"`   // hf_evals | arena | epoch
	SourceName string    `json:"source_name"` // "Arena leaderboard dataset"
	Metric     string    `json:"metric"`      // "arena:text/coding" (Advanced)
	Tests      string    `json:"tests"`       // what it tested, in plain words
	Purposes   []Purpose `json:"purposes"`    // what the metric map says it informs
	// Position is the size's place in words: "Among the strongest for coding
	// of the 9 models here that Arena has rated."
	Position string `json:"position"`
	// ProvenanceWords says who produced the value: "rated by people comparing
	// answers on Arena", "reported by the model's maker".
	ProvenanceWords string `json:"provenance_words"`
	Rated           int    `json:"rated" source:"n/a"` // how many curated sizes this source has a value for on this metric; a count
	Rank            int    `json:"rank" source:"n/a"`  // this size's place among them, 1 = best; a count
	// Scored says whether the recommendation engine may use it (the maker's
	// own numbers are shown, not scored).
	Scored  bool           `json:"scored"`
	Value   figure.Public  `json:"value"`
	Detail  []PublicDetail `json:"detail"`     // Advanced: raw value, scale, bounds, votes, notes, ids
	Fetched string         `json:"fetched_at"` // Advanced: when the advisor last read it (RFC 3339)
}

// PublicDetail is one labelled line of a public value's Advanced detail,
// already in words.
type PublicDetail struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

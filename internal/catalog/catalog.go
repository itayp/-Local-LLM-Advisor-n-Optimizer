// Package catalog is the curated model catalogue: a maintained list of
// trusted model families with purpose tags (data/catalog/families.yaml),
// with Hugging Face as the metadata source for each family's GGUF files.
//
// Step 1: types only. Step 4 adds the YAML loader, the Hugging Face client
// (internal/catalog/hf) and the GGUF header parser (internal/catalog/gguf),
// and persists what they find into catalog_models / catalog_files.
//
// The file is data — the docs must never count it (product rule 8).
package catalog

import "time"

// Purpose is what a user wants local AI for. The enum is shared with the UI
// (ui/src/api/types.ts) and with recommend; keep the three in step.
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

// Family is one curated model family as families.yaml describes it.
type Family struct {
	ID          string    `yaml:"id" json:"id"`
	DisplayName string    `yaml:"display_name" json:"display_name"`
	Maintainer  string    `yaml:"maintainer" json:"maintainer"`
	License     License   `yaml:"license" json:"license"`
	Purposes    []Purpose `yaml:"purposes" json:"purposes"`
	Sizes       []Size    `yaml:"sizes" json:"sizes"`
	ReviewedAt  string    `yaml:"reviewed_at" json:"reviewed_at"` // YYYY-MM-DD, when a person last checked this entry
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
	Parameters    uint64 `yaml:"parameters" json:"parameters" source:"n/a"`         // parameter count, from the model card
	ContextLength int    `yaml:"context_length" json:"context_length" source:"n/a"` // trained context, from the model card
	OllamaTag     string `yaml:"ollama_tag" json:"ollama_tag"`                      // what a beginner would pull, e.g. "llama3.1:8b"
	HFRepo        string `yaml:"hf_repo" json:"hf_repo"`                            // the GGUF repo used for metadata
}

// Model is a catalogue row: one family size, persisted (catalog_models).
type Model struct {
	ID       int64  `json:"id" source:"n/a"`
	FamilyID string `json:"family_id"`
	Family   Family `json:"family"`
	Size     Size   `json:"size"`
}

// File is one quant variant of a Model: a GGUF file whose header was read
// with a range request, never downloaded (catalog_files).
type File struct {
	ID       int64  `json:"id" source:"n/a"`
	ModelID  int64  `json:"model_id" source:"n/a"`
	Filename string `json:"filename"`
	Quant    string `json:"quant"` // Q4_K_M, Q8_0, ...
	SHA      string `json:"sha,omitempty"`

	Bytes         uint64  `json:"bytes" source:"n/a"`           // file size from the Hub listing, summed across parts; a fact, not an estimate
	BitsPerWeight float64 `json:"bits_per_weight" source:"n/a"` // derived from bytes and parameters; a property of the file

	Header    GGUFHeader `json:"header"`
	FetchedAt time.Time  `json:"fetched_at"`
}

// GGUFHeader carries the metadata fields the estimator needs. Step 0's
// finding: attention.key_length, where a model states one, is the real head
// dimension — embedding_length / head_count merely usually equals it.
type GGUFHeader struct {
	Architecture    string `json:"architecture"`
	BlockCount      int    `json:"block_count" source:"n/a"`
	HeadCount       int    `json:"head_count" source:"n/a"`
	HeadCountKV     int    `json:"head_count_kv" source:"n/a"`
	KeyLength       int    `json:"key_length" source:"n/a"` // 0 when the model does not state one
	EmbeddingLength int    `json:"embedding_length" source:"n/a"`
	ContextLength   int    `json:"context_length" source:"n/a"`
	SlidingWindow   int    `json:"sliding_window" source:"n/a"` // 0 when the architecture has none
	FileType        int    `json:"file_type" source:"n/a"`      // general.file_type
	ExpertCount     int    `json:"expert_count" source:"n/a"`   // > 0 marks a MoE model
	HasVision       bool   `json:"has_vision"`                  // a projector / vision tower is present
}

// External is one public benchmark or quality signal about a catalogue
// model (catalog_external). It is never stored in the same columns as a
// local measurement, and the UI never shows the two in the same column
// (PRD §10).
type External struct {
	ID          int64     `json:"id" source:"n/a"`
	Source      string    `json:"source"` // "huggingface", "artificial_analysis", ... (step 9a decides)
	SourceModel string    `json:"source_model_id"`
	ModelID     int64     `json:"model_id" source:"n/a"` // 0 when the alias did not resolve
	Metric      string    `json:"metric"`
	Value       float64   `json:"value" source:"n/a"` // a published number, quoted as-is with its source and date
	FetchedAt   time.Time `json:"fetched_at"`
	License     string    `json:"license"`
}

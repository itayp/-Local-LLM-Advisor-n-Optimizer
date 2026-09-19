package gguf

import (
	"fmt"
	"math"
	"strings"
)

// RequiredKeys are the metadata keys the estimator cannot do without, with
// "{arch}" standing for general.architecture. A StopAtTokenizer parse stops
// early only once all of them have been read. head_count_kv is on the list
// because a file that states it after the tokenizer must not be read as one
// that leaves it out (llama.cpp then uses head_count, which would make the
// KV-cache estimate wrong by the grouped-query factor).
//
// general.file_type is not on the list (ARCHITECTURE.md D-37). Current
// llama.cpp quantizers write it last, after the tokenizer, so requiring it
// meant reading 6–11 MB of vocabulary per file to learn what the file name
// already says; it is kept when it comes before the tokenizer and is a
// cross-check, not an input.
var RequiredKeys = []string{
	"general.architecture",
	"{arch}.block_count",
	"{arch}.context_length",
	"{arch}.embedding_length",
	"{arch}.attention.head_count",
	"{arch}.attention.head_count_kv",
}

// projectorArchitectures are the architectures of the separate vision (and
// audio) encoder files that multimodal models ship beside the language
// model ("mmproj-*.gguf"). They have no block_count of the language-model
// kind and no tokenizer; general.architecture is all they need.
var projectorArchitectures = map[string]bool{"clip": true}

// haveRequired reports whether h already holds every required key.
func haveRequired(h *Header) bool {
	arch, ok := h.KV["general.architecture"].(string)
	if !ok {
		return false
	}
	if projectorArchitectures[arch] {
		return true
	}
	for _, k := range RequiredKeys {
		if _, ok := h.KV[strings.ReplaceAll(k, "{arch}", arch)]; !ok {
			return false
		}
	}
	return true
}

// Missing lists the required keys h does not hold, with the architecture
// substituted. Empty for a projector file.
func (h *Header) Missing() []string {
	arch, _ := h.KV["general.architecture"].(string)
	if arch == "" {
		return []string{"general.architecture"}
	}
	if projectorArchitectures[arch] {
		return nil
	}
	var out []string
	for _, k := range RequiredKeys {
		k = strings.ReplaceAll(k, "{arch}", arch)
		if _, ok := h.KV[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

// Metadata is the typed view of a header: the fields the catalogue stores in
// their own columns and the estimator reads. Everything else stays in
// Header.KV. A count the file does not state is 0 (and, where 0 would be
// ambiguous, a *Stated flag says so) — never a guess.
type Metadata struct {
	Architecture string // general.architecture: "llama", "qwen3", "gemma4", "clip", ...
	Type         string // general.type: "model", "mmproj", "imatrix", ...; "" in older files
	Name         string // general.name
	SizeLabel    string // general.size_label: "8B", "35B-A3B", ...

	FileType       uint32 // general.file_type (llama_ftype); see FileTypeName
	FileTypeStated bool

	BlockCount      uint64 // {arch}.block_count: transformer layers
	ContextLength   uint64 // {arch}.context_length: trained context
	EmbeddingLength uint64 // {arch}.embedding_length
	HeadCount       uint64 // {arch}.attention.head_count (max over layers when per-layer)

	// HeadCountKV is {arch}.attention.head_count_kv — the grouped-query KV
	// heads, the factor the KV-cache estimate turns on. When the file gives
	// one value per layer (hybrid and some efficient architectures, with 0
	// for layers that keep no cache), this is the largest, and KVHeadsPerLayer
	// keeps the list. When the file does not state it at all, this is
	// HeadCount — llama.cpp's own rule for such files, not a guess — and
	// HeadCountKVStated is false.
	HeadCountKV       uint64
	HeadCountKVStated bool
	KVHeadsPerLayer   []uint64

	// KeyLength is {arch}.attention.key_length, the real head dimension
	// where a model states one (ARCHITECTURE.md D-20: qwen3:4b states 128
	// against embedding_length/head_count = 80). 0 when not stated.
	KeyLength   uint64
	ValueLength uint64 // {arch}.attention.value_length; 0 when not stated

	SlidingWindow         uint64 // {arch}.attention.sliding_window; 0 when the architecture has none
	ExpertCount           uint64 // {arch}.expert_count; > 0 marks a mixture-of-experts model
	ExpertUsedCount       uint64 // {arch}.expert_used_count: experts active per token
	FullAttentionInterval uint64 // {arch}.full_attention_interval: hybrid models where only every Nth layer is attention (e.g. qwen35)

	ParameterCount uint64 // general.parameter_count, when the writer stated it; 0 otherwise

	// Projector is true for a vision/audio encoder file (mmproj) rather than
	// a language model.
	Projector bool
}

// Metadata extracts the typed fields.
func (h *Header) Metadata() Metadata {
	m := Metadata{}
	m.Architecture, _ = h.KV["general.architecture"].(string)
	m.Type, _ = h.KV["general.type"].(string)
	m.Name, _ = h.KV["general.name"].(string)
	m.SizeLabel, _ = h.KV["general.size_label"].(string)
	m.Projector = projectorArchitectures[m.Architecture] || m.Type == "mmproj"
	if ft, ok := h.Uint("general.file_type"); ok && ft <= math.MaxUint32 {
		m.FileType, m.FileTypeStated = uint32(ft), true
	}
	m.ParameterCount, _ = h.Uint("general.parameter_count")

	a := m.Architecture + "."
	m.BlockCount, _ = h.Uint(a + "block_count")
	m.ContextLength, _ = h.Uint(a + "context_length")
	m.EmbeddingLength, _ = h.Uint(a + "embedding_length")
	m.HeadCount, _ = h.maxUint(a + "attention.head_count")
	if kv, ok := h.maxUint(a + "attention.head_count_kv"); ok {
		m.HeadCountKV, m.HeadCountKVStated = kv, true
		if list, ok := h.KV[a+"attention.head_count_kv"].([]any); ok {
			for _, x := range list {
				n, _ := toUint(x)
				m.KVHeadsPerLayer = append(m.KVHeadsPerLayer, n)
			}
		}
	} else {
		m.HeadCountKV = m.HeadCount
	}
	m.KeyLength, _ = h.maxUint(a + "attention.key_length")
	m.ValueLength, _ = h.maxUint(a + "attention.value_length")
	m.SlidingWindow, _ = h.Uint(a + "attention.sliding_window")
	m.ExpertCount, _ = h.Uint(a + "expert_count")
	m.ExpertUsedCount, _ = h.Uint(a + "expert_used_count")
	m.FullAttentionInterval, _ = h.Uint(a + "full_attention_interval")
	return m
}

// Uint returns a non-negative integer value. Floats and arrays are not
// integers; a negative integer is not a count.
func (h *Header) Uint(key string) (uint64, bool) {
	return toUint(h.KV[key])
}

// maxUint is Uint, or the largest element when the value is a per-layer
// array — the convention probe0 established for per-layer head counts.
func (h *Header) maxUint(key string) (uint64, bool) {
	v, ok := h.KV[key]
	if !ok {
		return 0, false
	}
	if list, isList := v.([]any); isList {
		var best uint64
		found := false
		for _, x := range list {
			if n, ok := toUint(x); ok {
				found = true
				if n > best {
					best = n
				}
			}
		}
		return best, found
	}
	return toUint(v)
}

func toUint(v any) (uint64, bool) {
	switch x := v.(type) {
	case uint64:
		return x, true
	case int64:
		if x >= 0 {
			return uint64(x), true
		}
	}
	return 0, false
}

// FileTypeName is general.file_type as llama.cpp names it (llama_ftype,
// without the LLAMA_FTYPE_MOSTLY_ prefix): 15 → "Q4_K_M". Unknown values
// come back as "file_type 99" — never as a guess.
func FileTypeName(ft uint32) string {
	if n, ok := fileTypeNames[ft]; ok {
		return n
	}
	return fmt.Sprintf("file_type %d", ft)
}

// fileTypeNames is llama.cpp's llama_ftype enum (include/llama.h). Gaps are
// types llama.cpp has removed; their numbers are never reused.
var fileTypeNames = map[uint32]string{
	0:  "F32",
	1:  "F16",
	2:  "Q4_0",
	3:  "Q4_1",
	7:  "Q8_0",
	8:  "Q5_0",
	9:  "Q5_1",
	10: "Q2_K",
	11: "Q3_K_S",
	12: "Q3_K_M",
	13: "Q3_K_L",
	14: "Q4_K_S",
	15: "Q4_K_M",
	16: "Q5_K_S",
	17: "Q5_K_M",
	18: "Q6_K",
	19: "IQ2_XXS",
	20: "IQ2_XS",
	21: "Q2_K_S",
	22: "IQ3_XS",
	23: "IQ3_XXS",
	24: "IQ1_S",
	25: "IQ4_NL",
	26: "IQ3_S",
	27: "IQ3_M",
	28: "IQ2_S",
	29: "IQ2_M",
	30: "IQ4_XS",
	31: "IQ1_M",
	32: "BF16",
	36: "TQ1_0",
	37: "TQ2_0",
	38: "MXFP4_MOE",
}

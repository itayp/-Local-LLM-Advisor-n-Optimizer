package catalog

import (
	"math"
	"strings"

	"advisor/internal/catalog/gguf"
)

// HeaderFromGGUF is the catalogue's view of a parsed header: the typed
// fields catalog_files keeps in columns. The full metadata (tokenizer
// excluded) goes to header_json beside them.
func HeaderFromGGUF(h *gguf.Header) GGUFHeader {
	m := h.Metadata()
	out := GGUFHeader{
		Architecture:          m.Architecture,
		GGUFVersion:           int(h.Version),
		TensorCount:           clampInt(h.TensorCount),
		BlockCount:            clampInt(m.BlockCount),
		HeadCount:             clampInt(m.HeadCount),
		HeadCountKV:           clampInt(m.HeadCountKV),
		HeadCountKVStated:     m.HeadCountKVStated,
		KeyLength:             clampInt(m.KeyLength),
		ValueLength:           clampInt(m.ValueLength),
		EmbeddingLength:       clampInt(m.EmbeddingLength),
		ContextLength:         clampInt(m.ContextLength),
		SlidingWindow:         clampInt(m.SlidingWindow),
		FullAttentionInterval: clampInt(m.FullAttentionInterval),
		FileType:              -1,
		ExpertCount:           clampInt(m.ExpertCount),
		ExpertUsedCount:       clampInt(m.ExpertUsedCount),
		Complete:              h.Complete,
	}
	if m.FileTypeStated {
		out.FileType = int(m.FileType)
		out.FileTypeName = gguf.FileTypeName(m.FileType)
	}
	return out
}

func clampInt(v uint64) int {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(v)
}

// HeaderFromRuntime is HeaderFromGGUF for a model a runtime already has on
// disk: the metadata as the runtime reports it (Ollama's /api/show
// model_info — the GGUF file's own keys, decoded from JSON, so every number
// is a float64 and an array is []any). It returns the typed header and the
// metadata converted to the parser's value types, which is what NewLayout
// reads. Nothing is guessed: a key the runtime left out (Ollama drops arrays
// unless asked for the verbose form) is missing here too, and the layout
// says so.
func HeaderFromRuntime(meta map[string]any) (GGUFHeader, map[string]any) {
	kv := make(map[string]any, len(meta))
	for k, v := range meta {
		if strings.HasPrefix(k, "tokenizer.") {
			continue
		}
		kv[k] = runtimeValue(v)
	}
	h := HeaderFromGGUF(&gguf.Header{Version: 3, KV: kv})
	h.GGUFVersion = 0 // not stated by the runtime
	return h, kv
}

// runtimeValue turns a JSON-decoded value into the parser's types: a whole
// number becomes uint64 (or int64 when negative), other numbers stay
// float64, arrays are converted element by element.
func runtimeValue(v any) any {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			if x >= 0 {
				return uint64(x)
			}
			return int64(x)
		}
		return x
	case int:
		if x >= 0 {
			return uint64(x)
		}
		return int64(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = runtimeValue(e)
		}
		return out
	}
	return v
}

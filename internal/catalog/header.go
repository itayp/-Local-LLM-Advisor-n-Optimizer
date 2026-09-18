package catalog

import (
	"math"

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

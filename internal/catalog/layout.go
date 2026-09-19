package catalog

import (
	"fmt"
	"math"
)

// Layout says what each of a model's layers keeps in memory as a
// conversation grows. For the models step 0 measured it is one line — every
// layer is an attention layer with the same heads, and the cache grows with
// every token — and the memory estimate is ARCHITECTURE.md D-20's formula.
// Most of the catalogue is no longer that simple:
//
//   - hybrid models (Qwen3.5's Gated DeltaNet, Nemotron's Mamba-2) keep a
//     cache on a fraction of their layers and a small fixed state on the rest;
//   - sliding-window models (Gemma, gpt-oss) stop growing the cache of most
//     layers at the window;
//   - Gemma's small sizes share one layer's cache with the layers after it;
//   - multi-head latent attention (GLM-4.7-Flash, header "deepseek2") caches
//     one compressed key per token and no value;
//   - multi-token-prediction layers at the end of a file (block_count is one
//     more than the model card's layer count) are not run by the runtime.
//
// A Layout is derived from the header, never stored: NewLayout reads the
// typed fields and the raw metadata (catalog_files.header_json), so a better
// reading of the same header needs no catalogue refresh. Every rule below
// follows what llama.cpp's loader does with the same keys (checked against
// ggml-org/llama.cpp 5b59b83, 2026-09-19: src/llama-hparams.cpp,
// src/llama-kv-cache*.cpp, src/models/{gemma4,qwen35,nemotron-h,openai-moe,
// deepseek2}.cpp) — the runtime's own arithmetic, not a guess at it.
type Layout struct {
	// Groups are the layers that keep a key/value cache, grouped by shape.
	Groups []LayerGroup `json:"groups"`

	// RecurrentLayers keep a fixed-size state instead of a cache;
	// RecurrentStateElements is that state's size per layer, in 32-bit
	// floats (convolution state plus recurrent state). Read from the header.
	RecurrentLayers        int    `json:"recurrent_layers" source:"n/a"`
	RecurrentStateElements uint64 `json:"recurrent_state_elements" source:"n/a"`

	// StatelessLayers keep nothing per token: layers that reuse another
	// layer's cache, prediction layers the runtime does not run, and blocks
	// without attention. A count read from the header.
	StatelessLayers int `json:"stateless_layers" source:"n/a"`

	// Basis says how far the layout can be trusted — it feeds a
	// recommendation's confidence.
	Basis LayoutBasis `json:"basis"`
	// Notes say, for the Advanced view, what was read and what was assumed.
	Notes []string `json:"notes,omitempty"`
}

// LayerKind is how a group of layers' cache grows.
type LayerKind string

const (
	// LayersFull attend over the whole context: the cache grows with every token.
	LayersFull LayerKind = "full"
	// LayersSliding attend over a window: the cache stops growing at the window.
	LayersSliding LayerKind = "sliding"
)

// LayerGroup is a run of layers with the same cache shape. Every number is
// read from the header.
type LayerGroup struct {
	Kind        LayerKind `json:"kind"`
	Layers      int       `json:"layers" source:"n/a"`
	KVHeads     int       `json:"kv_heads" source:"n/a"`
	KeyLength   int       `json:"key_length" source:"n/a"`
	ValueLength int       `json:"value_length" source:"n/a"` // 0: no value cache (multi-head latent attention)
	Window      int       `json:"window" source:"n/a"`       // sliding layers only, in tokens
}

// LayoutBasis is how a Layout was established.
type LayoutBasis string

const (
	// LayoutUniform: every layer is a full-attention layer of one shape —
	// the shape step 0 measured on three machines (ARCHITECTURE.md D-20).
	LayoutUniform LayoutBasis = "uniform"
	// LayoutStated: the header states the structure (per-layer heads, which
	// layers slide, which are recurrent, which share a cache) and the layout
	// follows it the way llama.cpp's loader does. Not yet measured on the
	// fleet beyond step 0's two hybrid rows.
	LayoutStated LayoutBasis = "stated"
	// LayoutArchitecture: the header states a sliding window but not which
	// layers use it; the pattern is the one llama.cpp hard-codes for the
	// architecture (slidingPatterns).
	LayoutArchitecture LayoutBasis = "architecture"
	// LayoutIncomplete: something the layout needs is missing from the header
	// (a hybrid model's state size, a usable layer count). What could be read
	// is used, the note says what could not, and the estimate is less sure.
	LayoutIncomplete LayoutBasis = "incomplete"
)

// slidingPattern is how llama.cpp spreads sliding-window layers over an
// architecture whose GGUF files state a window but not which layers use it:
// of every Period layers all but one slide — the last one attends to
// everything, or the first when DenseFirst (llama_hparams::set_swa_pattern).
type slidingPattern struct {
	Period     int
	DenseFirst bool
}

// slidingPatterns lists every architecture llama.cpp runs with a sliding
// window and a hard-coded pattern (the load_swa_pattern calls in
// src/models/*.cpp of ggml-org/llama.cpp 5b59b83, 2026-09-19). Architectures
// that state the pattern themselves (gemma4 writes one flag per layer) need
// no entry. An architecture that is NOT here and states no pattern is run by
// llama.cpp with full attention on every layer, whatever window its header
// mentions — which is what step 0 measured for phi3 — so counting every
// layer in full is then exact, not cautious.
var slidingPatterns = map[string]slidingPattern{
	"afmoe":           {4, false},
	"cohere2":         {4, false},
	"cohere2moe":      {4, true},
	"exaone-moe":      {4, false},
	"exaone4":         {4, false},
	"gemma-embedding": {6, false},
	"gemma2":          {2, false},
	"gemma3":          {6, false},
	"gemma3n":         {5, false},
	"gpt-oss":         {2, false},
	"laguna":          {4, true},
	"llama4":          {4, false}, // chunked attention: the chunk plays the window's part
	"mellum":          {4, false},
	"muse-glimmer":    {4, false},
	"olmo2":           {4, false},
	"plamo3":          {8, false},
	"smallthinker":    {4, true},
}

// recurrentByInterval are the architectures where, absent a per-layer list,
// every layer is recurrent except each full_attention_interval-th one
// (llama.cpp src/models/qwen35.cpp: interval 4 when the key is missing).
var recurrentByInterval = map[string]bool{"qwen3next": true, "qwen35": true, "qwen35moe": true}

// NewLayout derives the layout of a language-model file from its header: h
// is the typed view, kv the raw metadata pairs as the parser kept them or
// as header_json decodes them (numbers may be uint64, int64 or float64;
// arrays []any). kv may be nil — then only the typed fields speak, which is
// exact for a plain attention stack and incomplete for a hybrid one.
func NewLayout(h GGUFHeader, kv map[string]any) Layout {
	var l Layout
	note := func(format string, args ...any) { l.Notes = append(l.Notes, fmt.Sprintf(format, args...)) }
	a := h.Architecture + "."

	keyLen, valLen := h.KeyLength, h.ValueLength
	if keyLen == 0 && h.HeadCount > 0 {
		keyLen = h.EmbeddingLength / h.HeadCount
		note("the header states no attention.key_length; head dimension taken as embedding_length / head_count = %d", keyLen)
	}
	if valLen == 0 {
		valLen = keyLen
	}
	if h.BlockCount <= 0 || keyLen <= 0 {
		l.Basis = LayoutIncomplete
		note("the header states no usable layer count or head dimension")
		return l
	}
	l.Basis = LayoutUniform
	stated := func() {
		if l.Basis == LayoutUniform {
			l.Basis = LayoutStated
		}
	}

	// Multi-token-prediction layers are appended after the model's own; the
	// runtime does not run them for ordinary generation, so they keep nothing.
	n := h.BlockCount
	if nextn, ok := kvInt(kv, a+"nextn_predict_layers"); ok && nextn > 0 && nextn < n {
		n -= nextn
		l.StatelessLayers += nextn
		stated()
		note("%d multi-token-prediction layer(s) at the end of the file keep no cache", nextn)
	}

	// Multi-head latent attention: one compressed key per token, no value.
	if mla, ok := kvInt(kv, a+"attention.key_length_mla"); ok && mla > 0 {
		valLen = 0
		stated()
		note("multi-head latent attention: the cache holds one compressed key of %d per token and no values", keyLen)
	}

	// Per-layer KV heads (0 = the layer has no attention).
	heads := make([]int, n)
	for i := range heads {
		heads[i] = h.HeadCountKV
	}
	perLayerHeads := false
	if list, ok := kvInts(kv, a+"attention.head_count_kv"); ok && len(list) >= n {
		copy(heads, list[:n])
		perLayerHeads = true
		stated()
	}
	ffn, haveFFN := kvInts(kv, a+"feed_forward_length")

	// Which layers are recurrent.
	recurrent := make([]bool, n)
	_, hasSSM := kvInt(kv, a+"ssm.state_size")
	switch list, ok := kvBools(kv, a+"attention.recurrent_layers"); {
	case ok && len(list) >= n:
		copy(recurrent, list[:n])
		stated()
	case h.FullAttentionInterval > 1 || (recurrentByInterval[h.Architecture] && hasSSM):
		interval := h.FullAttentionInterval
		if interval <= 1 {
			interval = 4 // llama.cpp's default for these architectures
		}
		for i := range recurrent {
			recurrent[i] = (i+1)%interval != 0
		}
		stated()
		note("hybrid attention: one layer in %d is an attention layer; the others keep a small fixed state", interval)
	case perLayerHeads && hasSSM:
		// Nemotron-H's rule: recurrent iff the layer has neither KV heads nor
		// a feed-forward block (a layer with a feed-forward block and no KV
		// heads is a plain MLP or expert layer and keeps nothing).
		for i := range recurrent {
			recurrent[i] = heads[i] == 0 && (!haveFFN || i >= len(ffn) || ffn[i] == 0)
		}
	}
	if conv, ok := kvInt(kv, a+"ssm.conv_kernel"); ok && hasSSM {
		inner, _ := kvInt(kv, a+"ssm.inner_size")
		state, _ := kvInt(kv, a+"ssm.state_size")
		groups, _ := kvInt(kv, a+"ssm.group_count")
		if conv > 0 {
			l.RecurrentStateElements = uint64(conv-1) * uint64(inner+2*groups*state)
		}
		l.RecurrentStateElements += uint64(state) * uint64(inner)
	}

	// Layers that reuse an earlier layer's cache (Gemma's small sizes): the
	// last shared_kv_layers layers own none.
	ownCacheUntil := n
	if shared, ok := kvInt(kv, a+"attention.shared_kv_layers"); ok && shared > 0 && shared < n {
		ownCacheUntil = n - shared
		stated()
		note("the last %d layers reuse an earlier layer's cache", shared)
	}

	// Which layers slide.
	sliding := make([]bool, n)
	if h.SlidingWindow > 0 {
		list, isList := kvBools(kv, a+"attention.sliding_window_pattern")
		period, isPeriod := kvInt(kv, a+"attention.sliding_window_pattern")
		known, isKnown := slidingPatterns[h.Architecture]
		switch {
		case isList && len(list) >= n:
			copy(sliding, list[:n])
			stated()
			count := 0
			for _, v := range sliding {
				if v {
					count++
				}
			}
			// Gemma 4 states the pattern layer by layer; step 5's cards for it
			// carried no note, so the Advanced view could not show why its
			// cache grows slowly (step 6's catch-up of step 5's "worth a look").
			note("sliding-window attention on %d of %d layers (window %d tokens), as the header states layer by layer", count, n, h.SlidingWindow)
		case isPeriod || isKnown:
			pattern := known
			if isPeriod {
				pattern.Period = period
				stated()
			} else if l.Basis != LayoutIncomplete {
				l.Basis = LayoutArchitecture
			}
			if pattern.Period > 1 {
				for i := range sliding {
					if pattern.DenseFirst {
						sliding[i] = i%pattern.Period != 0
					} else {
						sliding[i] = i%pattern.Period < pattern.Period-1
					}
				}
				note("sliding-window attention on %d of every %d layers (window %d tokens)", pattern.Period-1, pattern.Period, h.SlidingWindow)
			}
		default:
			// The header mentions a window, but the runtime has no
			// sliding-window pattern for this architecture and runs every
			// layer with full attention: the plain formula is exact.
			note("the header states a sliding window of %d tokens, which the runtime does not use for %q: every layer attends to the whole context", h.SlidingWindow, h.Architecture)
		}
	}
	swaKey, swaVal := keyLen, valLen
	if v, ok := kvInt(kv, a+"attention.key_length_swa"); ok && v > 0 {
		swaKey = v
		swaVal = v
		if vv, ok := kvInt(kv, a+"attention.value_length_swa"); ok && vv > 0 {
			swaVal = vv
		}
	}

	for i := 0; i < n; i++ {
		switch {
		case recurrent[i]:
			l.RecurrentLayers++
			continue
		case i >= ownCacheUntil, heads[i] <= 0:
			l.StatelessLayers++
			continue
		}
		g := LayerGroup{Kind: LayersFull, Layers: 1, KVHeads: heads[i], KeyLength: keyLen, ValueLength: valLen}
		if sliding[i] {
			g = LayerGroup{Kind: LayersSliding, Layers: 1, KVHeads: heads[i], KeyLength: swaKey, ValueLength: swaVal, Window: h.SlidingWindow}
		}
		l.addGroup(g)
	}
	if l.RecurrentLayers > 0 && l.RecurrentStateElements == 0 {
		l.Basis = LayoutIncomplete
		note("the header marks %d recurrent layers but not the size of their state; it is left out", l.RecurrentLayers)
	}
	return l
}

func (l *Layout) addGroup(g LayerGroup) {
	for i := range l.Groups {
		x := &l.Groups[i]
		if x.Kind == g.Kind && x.KVHeads == g.KVHeads && x.KeyLength == g.KeyLength && x.ValueLength == g.ValueLength && x.Window == g.Window {
			x.Layers += g.Layers
			return
		}
	}
	l.Groups = append(l.Groups, g)
}

// CacheLayers is how many layers keep a key/value cache.
func (l Layout) CacheLayers() int {
	n := 0
	for _, g := range l.Groups {
		n += g.Layers
	}
	return n
}

// kvNumber reads one metadata value as a non-negative integer, whatever
// numeric type the parser or a JSON round trip left it in.
func kvNumber(v any) (int, bool) {
	switch x := v.(type) {
	case uint64:
		if x <= math.MaxInt32 {
			return int(x), true
		}
	case int64:
		if x >= 0 && x <= math.MaxInt32 {
			return int(x), true
		}
	case int:
		if x >= 0 {
			return x, true
		}
	case float64:
		if x >= 0 && x <= math.MaxInt32 && x == math.Trunc(x) {
			return int(x), true
		}
	}
	return 0, false
}

func kvInt(kv map[string]any, key string) (int, bool) {
	v, ok := kv[key]
	if !ok {
		return 0, false
	}
	return kvNumber(v)
}

func kvInts(kv map[string]any, key string) ([]int, bool) {
	list, ok := kv[key].([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	out := make([]int, len(list))
	for i, v := range list {
		n, ok := kvNumber(v)
		if !ok {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

func kvBools(kv map[string]any, key string) ([]bool, bool) {
	list, ok := kv[key].([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	out := make([]bool, len(list))
	for i, v := range list {
		switch x := v.(type) {
		case bool:
			out[i] = x
		default:
			n, ok := kvNumber(v)
			if !ok {
				return nil, false
			}
			out[i] = n != 0
		}
	}
	return out, true
}

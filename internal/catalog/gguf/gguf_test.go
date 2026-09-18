package gguf

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The fixtures in testdata/ are real GGUF headers: the first bytes of model
// files, captured from Ollama's blob store on the fleet's M1 Pro
// (2026-09-18) and gzipped. Each holds the whole metadata section, rounded
// up to 4 KiB, and nothing of the weights. testdata/README.md says which
// blob each came from.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".gguf.head.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type realCase struct {
	name        string
	tensors     uint64
	kvCount     uint64
	headerBytes int64 // end of the metadata section, as a Python walk of the same bytes found it
	want        Metadata
	stopsAt     string // with StopAtTokenizer; "" = cannot stop early (a required key comes after the tokenizer)
}

var realCases = []realCase{
	{
		// llama.cpp's writer order (the one Hugging Face GGUFs have):
		// general.*, llama.*, general.file_type, tokenizer.*, then
		// general.quantization_version.
		name: "llama3.2-1b", tensors: 147, kvCount: 30, headerBytes: 7822528,
		want: Metadata{
			Architecture: "llama", Type: "model", Name: "Llama 3.2 1B Instruct", SizeLabel: "1B",
			FileType: 7, FileTypeStated: true, // Q8_0: Ollama's llama3.2:1b
			BlockCount: 16, ContextLength: 131072, EmbeddingLength: 2048,
			HeadCount: 32, HeadCountKV: 8, HeadCountKVStated: true, KeyLength: 64, ValueLength: 64,
		},
		stopsAt: "tokenizer.ggml.model",
	},
	{
		// Ollama's own writer: keys sorted. D-20's case — key_length 128
		// against embedding_length/head_count = 80.
		name: "qwen3-4b", tensors: 398, kvCount: 33, headerBytes: 5933099,
		want: Metadata{
			Architecture: "qwen3", Type: "model", Name: "Qwen3 4B Thinking 2507", SizeLabel: "4B",
			FileType: 15, FileTypeStated: true, // Q4_K_M
			BlockCount: 36, ContextLength: 262144, EmbeddingLength: 2560,
			HeadCount: 32, HeadCountKV: 8, HeadCountKVStated: true, KeyLength: 128, ValueLength: 128,
			ParameterCount: 4022468096,
		},
		stopsAt: "tokenizer.chat_template",
	},
	{
		// A hybrid (Gated DeltaNet + attention every 4th layer) model whose
		// writer put general.file_type after the tokenizer: the
		// StopAtTokenizer parse must read on rather than stop without it.
		name: "minicpm-v4.6", tensors: 320, kvCount: 39, headerBytes: 10936740,
		want: Metadata{
			Architecture: "qwen35", Type: "model", Name: "MiniCPM V 4_6", SizeLabel: "752M",
			FileType: 15, FileTypeStated: true,
			BlockCount: 24, ContextLength: 262144, EmbeddingLength: 1024,
			HeadCount: 8, HeadCountKV: 2, HeadCountKVStated: true, KeyLength: 256, ValueLength: 256,
			FullAttentionInterval: 4,
		},
	},
	{
		// The same model's vision encoder: a separate file, no tokenizer.
		name: "minicpm-v4.6-projector", tensors: 459, kvCount: 26, headerBytes: 1152,
		want: Metadata{
			Architecture: "clip", Type: "mmproj", Name: "MiniCPM V 4_6", SizeLabel: "548M",
			FileType: 1, FileTypeStated: true, Projector: true,
		},
	},
}

func TestParseRealHeaders(t *testing.T) {
	for _, tc := range realCases {
		t.Run(tc.name, func(t *testing.T) {
			b := fixture(t, tc.name)
			h, err := Parse(bytes.NewReader(b), Options{})
			if err != nil {
				t.Fatal(err)
			}
			if h.Version != 3 || h.TensorCount != tc.tensors || h.KVCount != tc.kvCount {
				t.Errorf("version/tensors/kv = %d/%d/%d, want 3/%d/%d", h.Version, h.TensorCount, h.KVCount, tc.tensors, tc.kvCount)
			}
			if !h.Complete || h.KVRead != h.KVCount || h.StoppedAt != "" {
				t.Errorf("complete=%v read=%d stopped=%q, want a complete parse", h.Complete, h.KVRead, h.StoppedAt)
			}
			if h.BytesRead != tc.headerBytes {
				t.Errorf("header ends at %d, want %d", h.BytesRead, tc.headerBytes)
			}
			for k := range h.KV {
				if strings.HasPrefix(k, "tokenizer.") {
					t.Errorf("tokenizer key %q was kept; the tokenizer is skipped entirely", k)
				}
			}
			if got := h.Metadata(); !metadataEqual(got, tc.want) {
				t.Errorf("metadata:\n got  %+v\n want %+v", got, tc.want)
			}
			if miss := h.Missing(); len(miss) != 0 {
				t.Errorf("Missing() = %v on a complete real header", miss)
			}
		})
	}
}

func metadataEqual(a, b Metadata) bool {
	return reflect.DeepEqual(a, b)
}

// countingReader counts what the parser pulled from the source — the bytes
// a range reader would have downloaded.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func TestStopAtTokenizerReadsOnlyWhatItNeeds(t *testing.T) {
	for _, tc := range realCases {
		t.Run(tc.name, func(t *testing.T) {
			b := fixture(t, tc.name)
			full, err := Parse(bytes.NewReader(b), Options{})
			if err != nil {
				t.Fatal(err)
			}
			cr := &countingReader{r: bytes.NewReader(b)}
			h, err := Parse(cr, Options{StopAtTokenizer: true})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := h.Metadata(), full.Metadata(); !metadataEqual(got, want) {
				t.Errorf("stopping early changed the answer:\n got  %+v\n want %+v", got, want)
			}
			if tc.stopsAt == "" {
				if !h.Complete {
					t.Errorf("stopped at %q although a required key comes after the tokenizer", h.StoppedAt)
				}
				return
			}
			if h.Complete || h.StoppedAt != tc.stopsAt {
				t.Errorf("complete=%v stoppedAt=%q, want a stop at %q", h.Complete, h.StoppedAt, tc.stopsAt)
			}
			// The whole point: the megabytes of tokenizer are never pulled.
			// The parser's buffer reads ahead at most 64 KiB.
			if cr.n > h.BytesRead+64<<10 {
				t.Errorf("read %d bytes of a %d-byte header to stop at byte %d", cr.n, tc.headerBytes, h.BytesRead)
			}
			if h.BytesRead > 16<<10 {
				t.Errorf("stopped at byte %d; the metadata before the tokenizer is a few KiB", h.BytesRead)
			}
		})
	}
}

func TestTruncatedPrefixFailsAsTruncated(t *testing.T) {
	b := fixture(t, "llama3.2-1b")
	for _, n := range []int{0, 3, 4, 7, 12, 23, 24, 40, 100, 1000, 4000, 1 << 20, 7822527} {
		_, err := Parse(bytes.NewReader(b[:n]), Options{})
		if !errors.Is(err, ErrTruncated) {
			t.Errorf("prefix of %d bytes: err = %v, want ErrTruncated", n, err)
		}
		if errors.Is(err, ErrMalformed) {
			t.Errorf("prefix of %d bytes: a short prefix is not a malformed file: %v", n, err)
		}
	}
	// With StopAtTokenizer, a prefix that reaches the tokenizer is enough.
	full, _ := Parse(bytes.NewReader(b), Options{StopAtTokenizer: true})
	if _, err := Parse(bytes.NewReader(b[:full.BytesRead+64]), Options{StopAtTokenizer: true}); err != nil {
		t.Errorf("prefix up to the tokenizer: %v", err)
	}
}

// --- synthetic headers -------------------------------------------------

// w writes GGUF by hand, little-endian, so tests can build the headers no
// real file would have.
type w struct{ bytes.Buffer }

func (b *w) u32(v uint32) *w { _ = binary.Write(&b.Buffer, binary.LittleEndian, v); return b }
func (b *w) u64(v uint64) *w { _ = binary.Write(&b.Buffer, binary.LittleEndian, v); return b }
func (b *w) str(s string) *w { b.u64(uint64(len(s))); b.WriteString(s); return b }

func (b *w) kv(key string, t Type, v any) *w {
	b.str(key).u32(uint32(t))
	b.val(t, v)
	return b
}

func (b *w) val(t Type, v any) {
	switch t {
	case TypeString:
		b.str(v.(string))
	case TypeUint32:
		b.u32(v.(uint32))
	case TypeInt32:
		_ = binary.Write(&b.Buffer, binary.LittleEndian, v.(int32))
	case TypeUint64:
		b.u64(v.(uint64))
	case TypeInt64:
		_ = binary.Write(&b.Buffer, binary.LittleEndian, v.(int64))
	case TypeUint8:
		b.WriteByte(v.(uint8))
	case TypeInt8:
		b.WriteByte(byte(v.(int8)))
	case TypeUint16:
		_ = binary.Write(&b.Buffer, binary.LittleEndian, v.(uint16))
	case TypeInt16:
		_ = binary.Write(&b.Buffer, binary.LittleEndian, v.(int16))
	case TypeFloat32:
		b.u32(math.Float32bits(v.(float32)))
	case TypeFloat64:
		b.u64(math.Float64bits(v.(float64)))
	case TypeBool:
		if v.(bool) {
			b.WriteByte(1)
		} else {
			b.WriteByte(0)
		}
	}
}

func (b *w) arr(key string, elem Type, vals ...any) *w {
	b.str(key).u32(uint32(TypeArray)).u32(uint32(elem)).u64(uint64(len(vals)))
	for _, v := range vals {
		b.val(elem, v)
	}
	return b
}

func head(kvs int) *w {
	b := &w{}
	b.u32(Magic).u32(3).u64(0).u64(uint64(kvs))
	return b
}

// minimal is a complete, valid header for a made-up llama-shaped model.
func minimal(extra func(*w), extraCount int) []byte {
	b := head(7 + extraCount)
	b.kv("general.architecture", TypeString, "llama")
	b.kv("general.file_type", TypeUint32, uint32(15))
	b.kv("llama.block_count", TypeUint32, uint32(32))
	b.kv("llama.context_length", TypeUint32, uint32(8192))
	b.kv("llama.embedding_length", TypeUint32, uint32(4096))
	b.kv("llama.attention.head_count", TypeUint32, uint32(32))
	b.kv("llama.attention.head_count_kv", TypeUint32, uint32(8))
	if extra != nil {
		extra(b)
	}
	return b.Bytes()
}

func TestMinimalHeader(t *testing.T) {
	h, err := Parse(bytes.NewReader(minimal(nil, 0)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := h.Metadata()
	if m.Architecture != "llama" || m.BlockCount != 32 || m.HeadCountKV != 8 || m.FileType != 15 || !m.HeadCountKVStated {
		t.Errorf("metadata = %+v", m)
	}
	if FileTypeName(m.FileType) != "Q4_K_M" {
		t.Errorf("FileTypeName(15) = %q", FileTypeName(m.FileType))
	}
}

func TestMalformedHeadersFailLoudly(t *testing.T) {
	good := minimal(nil, 0)
	patch := func(off int, v ...byte) []byte {
		b := slices.Clone(good)
		copy(b[off:], v)
		return b
	}
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"not GGUF at all", []byte("PK\x03\x04 this is a zip file, not a model header"), "not a GGUF file"},
		{"ggml magic", patch(0, 'G', 'G', 'M', 'L'), "not a GGUF file"},
		{"big-endian version", patch(4, 0, 0, 0, 3), "big-endian"},
		{"big-endian magic", patch(0, 'F', 'U', 'G', 'G'), "big-endian"},
		{"version 1", patch(4, 1, 0, 0, 0), "version 1"},
		{"version 99", patch(4, 99, 0, 0, 0), "unknown GGUF version"},
		{"absurd tensor count", patch(8, 0, 0, 0, 0, 0, 0, 0, 0x10), "tensor count"},
		{"absurd metadata count", patch(16, 0, 0, 0, 0, 0, 0, 1, 0), "metadata count"},
		{"absurd key length", patch(24, 0, 0, 0, 0, 0, 0, 0, 0x40), "string length"},
		{"empty key", func() []byte { b := head(1); b.str("").u32(4).u32(1); return b.Bytes() }(), "empty metadata key"},
		{"duplicate key", minimal(func(b *w) { b.kv("llama.block_count", TypeUint32, uint32(1)) }, 1), "duplicate"},
		{"unknown value type", func() []byte { b := head(1); b.str("general.architecture").u32(13); return b.Bytes() }(), "unknown value type 13"},
		{"unknown array element type", func() []byte {
			b := head(1)
			b.str("x.list").u32(uint32(TypeArray)).u32(77).u64(1)
			return b.Bytes()
		}(), "unknown array element type"},
		{"array of arrays", func() []byte {
			b := head(1)
			b.str("x.list").u32(uint32(TypeArray)).u32(uint32(TypeArray)).u64(1)
			return b.Bytes()
		}(), "arrays of arrays"},
		{"absurd array length", func() []byte {
			b := head(1)
			b.str("tokenizer.ggml.tokens").u32(uint32(TypeArray)).u32(uint32(TypeString)).u64(1 << 40)
			return b.Bytes()
		}(), "array length"},
		{"absurd string length", func() []byte {
			b := head(1)
			b.str("general.name").u32(uint32(TypeString)).u64(1 << 50)
			return b.Bytes()
		}(), "string length"},
		{"bool that is not 0 or 1", func() []byte {
			b := head(1)
			b.str("general.flag").u32(uint32(TypeBool)).WriteByte(7)
			return b.Bytes()
		}(), "neither 0 nor 1"},
		{"no architecture", func() []byte { b := head(1); b.kv("general.name", TypeString, "x"); return b.Bytes() }(), "general.architecture is missing"},
		{"architecture not a string", func() []byte { b := head(1); b.kv("general.architecture", TypeUint32, uint32(1)); return b.Bytes() }(), "general.architecture is missing or not a string"},
		{"invalid UTF-8 key", func() []byte { b := head(1); b.str("gen\xffral").u32(4).u32(1); return b.Bytes() }(), "not UTF-8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(bytes.NewReader(tc.in), Options{})
			if err == nil {
				t.Fatal("parsed without error")
			}
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
			var fe *FormatError
			if !errors.As(err, &fe) {
				t.Errorf("err is %T, want *FormatError", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to say %q", err, tc.want)
			}
		})
	}
}

func TestPerLayerAndMissingFields(t *testing.T) {
	t.Run("per-layer kv heads", func(t *testing.T) {
		b := head(6)
		b.kv("general.architecture", TypeString, "nemotron_h")
		b.kv("nemotron_h.block_count", TypeUint32, uint32(4))
		b.arr("nemotron_h.attention.head_count", TypeUint32, uint32(32), uint32(0), uint32(32), uint32(0))
		b.arr("nemotron_h.attention.head_count_kv", TypeUint32, uint32(0), uint32(0), uint32(8), uint32(0))
		b.kv("nemotron_h.expert_count", TypeUint32, uint32(128))
		b.kv("nemotron_h.expert_used_count", TypeUint32, uint32(6))
		h, err := Parse(bytes.NewReader(b.Bytes()), Options{})
		if err != nil {
			t.Fatal(err)
		}
		m := h.Metadata()
		if m.HeadCount != 32 || m.HeadCountKV != 8 || !m.HeadCountKVStated ||
			!slices.Equal(m.KVHeadsPerLayer, []uint64{0, 0, 8, 0}) || m.ExpertCount != 128 || m.ExpertUsedCount != 6 {
			t.Errorf("metadata = %+v", m)
		}
		if miss := h.Missing(); !slices.Equal(miss, []string{"general.file_type", "nemotron_h.context_length", "nemotron_h.embedding_length"}) {
			t.Errorf("Missing() = %v", miss)
		}
	})
	t.Run("head_count_kv not stated", func(t *testing.T) {
		b := head(3)
		b.kv("general.architecture", TypeString, "phi2")
		b.kv("phi2.attention.head_count", TypeUint32, uint32(32))
		b.kv("phi2.attention.sliding_window", TypeUint32, uint32(4096))
		h, err := Parse(bytes.NewReader(b.Bytes()), Options{})
		if err != nil {
			t.Fatal(err)
		}
		m := h.Metadata()
		if m.HeadCountKV != 32 || m.HeadCountKVStated || m.SlidingWindow != 4096 || m.FileTypeStated {
			t.Errorf("metadata = %+v", m)
		}
	})
	t.Run("every scalar type", func(t *testing.T) {
		b := head(12)
		b.kv("general.architecture", TypeString, "x")
		b.kv("x.u8", TypeUint8, uint8(200))
		b.kv("x.i8", TypeInt8, int8(-5))
		b.kv("x.u16", TypeUint16, uint16(60000))
		b.kv("x.i16", TypeInt16, int16(-300))
		b.kv("x.u32", TypeUint32, uint32(4000000000))
		b.kv("x.i32", TypeInt32, int32(-7))
		b.kv("x.u64", TypeUint64, uint64(1<<40))
		b.kv("x.i64", TypeInt64, int64(-1<<40))
		b.kv("x.f32", TypeFloat32, float32(0.5))
		b.kv("x.f64", TypeFloat64, float64(1e-6))
		b.kv("x.b", TypeBool, true)
		h, err := Parse(bytes.NewReader(b.Bytes()), Options{})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"general.architecture": "x", "x.u8": uint64(200), "x.i8": int64(-5), "x.u16": uint64(60000),
			"x.i16": int64(-300), "x.u32": uint64(4000000000), "x.i32": int64(-7), "x.u64": uint64(1 << 40),
			"x.i64": int64(-1 << 40), "x.f32": 0.5, "x.f64": 1e-6, "x.b": true,
		}
		for k, v := range want {
			if h.KV[k] != v {
				t.Errorf("%s = %#v, want %#v", k, h.KV[k], v)
			}
		}
		if h.Types["x.u16"] != TypeUint16 || h.Types["x.f32"] != TypeFloat32 {
			t.Errorf("types not recorded: %v", h.Types)
		}
		if _, ok := h.Uint("x.i32"); ok {
			t.Error("a negative integer read as a count")
		}
		if _, ok := h.Uint("x.f32"); ok {
			t.Error("a float read as a count")
		}
	})
	t.Run("oversized values are skipped, not kept", func(t *testing.T) {
		big := make([]any, keepArrayLen+1)
		for i := range big {
			big[i] = uint32(i)
		}
		b := head(3)
		b.kv("general.architecture", TypeString, "x")
		b.arr("x.big", TypeUint32, big...)
		b.kv("general.description", TypeString, strings.Repeat("a", keepStringLen+1))
		h, err := Parse(bytes.NewReader(b.Bytes()), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := h.KV["x.big"]; ok || !slices.Contains(h.Skipped, "x.big") || !slices.Contains(h.Skipped, "general.description") {
			t.Errorf("kept=%v skipped=%v", h.KV, h.Skipped)
		}
		if !h.Complete || h.KVRead != 3 {
			t.Errorf("complete=%v read=%d", h.Complete, h.KVRead)
		}
	})
}

func TestFileTypeNames(t *testing.T) {
	for ft, want := range map[uint32]string{0: "F32", 1: "F16", 7: "Q8_0", 15: "Q4_K_M", 17: "Q5_K_M", 18: "Q6_K", 30: "IQ4_XS", 32: "BF16", 38: "MXFP4_MOE", 99: "file_type 99", 4: "file_type 4"} {
		if got := FileTypeName(ft); got != want {
			t.Errorf("FileTypeName(%d) = %q, want %q", ft, got, want)
		}
	}
}

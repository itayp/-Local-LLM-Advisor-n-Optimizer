// Package gguf reads the header of a GGUF model file: the magic, the format
// version, the tensor count and the key/value metadata — the part of the file
// the estimator needs (block count, attention heads, head dimension, context
// length, file type). It never reads tensor data, and it reads the header
// from any io.Reader, so the same parser serves a local file, a captured
// fixture, and a Hugging Face range request (internal/catalog/hf) that has
// fetched only the first few megabytes of a multi-gigabyte file.
//
// The tokenizer (tokenizer.* keys: vocabularies, merges, scores, the chat
// template) is most of a header's bytes and none of the estimator's inputs.
// Its values are skipped, never stored. With Options.StopAtTokenizer the
// parse ends at the first tokenizer key once every required field has been
// read, so a range reader never fetches the tokenizer at all.
//
// A header that is not a GGUF header, or is corrupt, fails loudly with a
// *FormatError that says where and why; a header that is merely cut short
// (a prefix that ends inside the metadata) fails with ErrTruncated.
//
// Format reference: the GGUF specification in the ggml repository
// (docs/gguf.md) and llama.cpp's ggml/src/gguf.cpp, which is what every
// runtime this product drives actually uses to read these files.
package gguf

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

// Magic is "GGUF" read as a little-endian uint32.
const Magic uint32 = 0x46554747

// Value types, as the specification numbers them.
type Type uint32

const (
	TypeUint8   Type = 0
	TypeInt8    Type = 1
	TypeUint16  Type = 2
	TypeInt16   Type = 3
	TypeUint32  Type = 4
	TypeInt32   Type = 5
	TypeFloat32 Type = 6
	TypeBool    Type = 7
	TypeString  Type = 8
	TypeArray   Type = 9
	TypeUint64  Type = 10
	TypeInt64   Type = 11
	TypeFloat64 Type = 12
)

func (t Type) String() string {
	switch t {
	case TypeUint8:
		return "uint8"
	case TypeInt8:
		return "int8"
	case TypeUint16:
		return "uint16"
	case TypeInt16:
		return "int16"
	case TypeUint32:
		return "uint32"
	case TypeInt32:
		return "int32"
	case TypeFloat32:
		return "float32"
	case TypeBool:
		return "bool"
	case TypeString:
		return "string"
	case TypeArray:
		return "array"
	case TypeUint64:
		return "uint64"
	case TypeInt64:
		return "int64"
	case TypeFloat64:
		return "float64"
	}
	return fmt.Sprintf("type(%d)", uint32(t))
}

// fixedSize is the byte width of a scalar type; 0 for string and array.
func (t Type) fixedSize() int {
	switch t {
	case TypeUint8, TypeInt8, TypeBool:
		return 1
	case TypeUint16, TypeInt16:
		return 2
	case TypeUint32, TypeInt32, TypeFloat32:
		return 4
	case TypeUint64, TypeInt64, TypeFloat64:
		return 8
	}
	return 0
}

func (t Type) valid() bool { return t <= TypeFloat64 }

// Limits a well-formed header never reaches. They exist so a corrupt or
// hostile header fails with an error instead of asking for gigabytes of
// memory or looping for hours. The largest real values today: ~1,500 KV
// pairs is unheard of (typical: 25–50); vocabularies are ~260k entries and
// merge lists ~280k; tensor counts reach a few thousand for large MoE
// models.
const (
	maxKVCount     = 1 << 16
	maxTensorCount = 1 << 22
	maxKeyLen      = 1 << 16
	maxStringLen   = 1 << 26 // 64 MiB: any single string, kept or skipped
	maxArrayLen    = 1 << 26
	keepStringLen  = 1 << 20 // longer non-tokenizer strings are skipped, not stored
	keepArrayLen   = 4096    // longer non-tokenizer arrays are skipped, not stored
)

// ErrTruncated means the input ended inside the header: a prefix too short
// for this file, not a corrupt file. A range reader that sees it should read
// further; a caller holding the whole prefix it will ever get should say so.
var ErrTruncated = errors.New("gguf: header is truncated")

// ErrMalformed is wrapped by every *FormatError, so callers can tell "this is
// not a usable GGUF header" from I/O trouble with errors.Is.
var ErrMalformed = errors.New("gguf: malformed header")

// FormatError is a header that is not valid GGUF. Offset is the byte offset
// where the problem was found; Key is the metadata key being read, if any.
type FormatError struct {
	Offset int64
	Key    string
	Msg    string
}

func (e *FormatError) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("gguf: malformed header at byte %d (key %q): %s", e.Offset, e.Key, e.Msg)
	}
	return fmt.Sprintf("gguf: malformed header at byte %d: %s", e.Offset, e.Msg)
}

func (e *FormatError) Unwrap() error { return ErrMalformed }

// Options controls how much of the header is read.
type Options struct {
	// StopAtTokenizer ends the parse just before the first "tokenizer." key,
	// provided every key RequiredKeys names for this file's architecture has
	// been read by then. If one has not, the parse skips the tokenizer and
	// carries on to the end, so the required fields are the same either way —
	// only the bytes read differ. Optional keys written after the tokenizer
	// (general.file_type in current llama.cpp output) are then not read.
	StopAtTokenizer bool
}

// Header is what Parse read.
type Header struct {
	Version     uint32
	TensorCount uint64
	KVCount     uint64 // as the file states it

	// KV holds every metadata pair that was read and kept, keyed as the file
	// names it. Values are uint64 (every unsigned integer type), int64
	// (every signed type), float64 (both float types), bool, string, or
	// []any of those for arrays. tokenizer.* values are never kept, and
	// neither are strings over 1 MiB or arrays over 4096 elements; their keys
	// are listed in Skipped instead.
	KV map[string]any

	// Types records each kept key's type as the file declared it (for an
	// array, the element type), so the width of the original is not lost.
	Types map[string]Type

	Skipped []string // keys read past without keeping the value
	KVRead  uint64   // pairs read (== KVCount when Complete)

	// Complete is true when every pair was read. False only when
	// Options.StopAtTokenizer ended the parse early (StoppedAt names the
	// key it stopped before).
	Complete  bool
	StoppedAt string

	// BytesRead is how far into the file the parse got: the header's length
	// when Complete (the tensor-info section starts here).
	BytesRead int64
}

// Parse reads a GGUF header from r. It reads no further than the end of the
// metadata (or, with StopAtTokenizer, the start of the tokenizer), so r may
// be a prefix of the file.
func Parse(r io.Reader, opts Options) (*Header, error) {
	p := &parser{r: bufio.NewReaderSize(r, 64<<10)}
	return p.parse(opts)
}

type parser struct {
	r       *bufio.Reader
	off     int64
	key     string // key being read, for error messages
	scratch [8]byte
}

func (p *parser) malformed(format string, args ...any) error {
	return &FormatError{Offset: p.off, Key: p.key, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) truncated(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		where := ""
		if p.key != "" {
			where = fmt.Sprintf(" inside key %q", p.key)
		}
		return fmt.Errorf("%w: input ends at byte %d%s", ErrTruncated, p.off, where)
	}
	return fmt.Errorf("gguf: reading at byte %d: %w", p.off, err)
}

// read reads exactly n bytes into a fresh slice (n is bounded by the limits
// above before any call).
func (p *parser) read(n int) ([]byte, error) {
	b := make([]byte, n)
	if err := p.fill(b); err != nil {
		return nil, err
	}
	return b, nil
}

// fill reads exactly len(b) bytes, advancing the offset by what was read.
func (p *parser) fill(b []byte) error {
	got, err := io.ReadFull(p.r, b)
	p.off += int64(got)
	if err != nil {
		return p.truncated(err)
	}
	return nil
}

func (p *parser) skip(n uint64) error {
	for n > 0 {
		step := n
		if step > math.MaxInt32 {
			step = math.MaxInt32
		}
		got, err := p.r.Discard(int(step))
		p.off += int64(got)
		if err != nil {
			return p.truncated(io.ErrUnexpectedEOF)
		}
		n -= uint64(got)
	}
	return nil
}

func (p *parser) u32() (uint32, error) {
	if err := p.fill(p.scratch[:4]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(p.scratch[:4]), nil
}

func (p *parser) u64() (uint64, error) {
	if err := p.fill(p.scratch[:8]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(p.scratch[:8]), nil
}

func (p *parser) parse(opts Options) (*Header, error) {
	magic, err := p.u32()
	if err != nil {
		return nil, err
	}
	if magic == bitsSwapped(Magic) {
		return nil, &FormatError{Offset: 0, Msg: "big-endian GGUF is not supported (the runtimes this product drives read little-endian files)"}
	}
	if magic != Magic {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, magic)
		return nil, &FormatError{Offset: 0, Msg: fmt.Sprintf("not a GGUF file: magic is %q, want \"GGUF\"", b)}
	}
	version, err := p.u32()
	if err != nil {
		return nil, err
	}
	switch {
	case version == 2 || version == 3:
	case version == 1:
		return nil, &FormatError{Offset: 4, Msg: "GGUF version 1 (2023, 32-bit lengths) is not supported; re-convert the model"}
	case version&0xFFFF == 0 && bitsSwapped(version) >= 1 && bitsSwapped(version) <= 3:
		return nil, &FormatError{Offset: 4, Msg: "big-endian GGUF is not supported (the runtimes this product drives read little-endian files)"}
	default:
		return nil, &FormatError{Offset: 4, Msg: fmt.Sprintf("unknown GGUF version %d", version)}
	}
	h := &Header{Version: version, KV: map[string]any{}, Types: map[string]Type{}}
	if h.TensorCount, err = p.u64(); err != nil {
		return nil, err
	}
	if h.TensorCount > maxTensorCount {
		return nil, &FormatError{Offset: 8, Msg: fmt.Sprintf("tensor count %d is not plausible", h.TensorCount)}
	}
	if h.KVCount, err = p.u64(); err != nil {
		return nil, err
	}
	if h.KVCount > maxKVCount {
		return nil, &FormatError{Offset: 16, Msg: fmt.Sprintf("metadata count %d is not plausible", h.KVCount)}
	}

	for i := uint64(0); i < h.KVCount; i++ {
		p.key = ""
		start := p.off
		key, err := p.string(maxKeyLen, true)
		if err != nil {
			return nil, err
		}
		if key == "" {
			return nil, p.malformed("empty metadata key")
		}
		if !utf8.ValidString(key) {
			return nil, p.malformed("metadata key is not UTF-8")
		}
		p.key = key
		if _, dup := h.KV[key]; dup || contains(h.Skipped, key) {
			return nil, p.malformed("duplicate metadata key")
		}
		if opts.StopAtTokenizer && strings.HasPrefix(key, "tokenizer.") && haveRequired(h) {
			// Stop before this key: the offset reported is where it begins.
			h.Complete = false
			h.StoppedAt = key
			h.BytesRead = start
			return h, nil
		}
		tv, err := p.u32()
		if err != nil {
			return nil, err
		}
		t := Type(tv)
		if !t.valid() {
			return nil, p.malformed("unknown value type %d", tv)
		}
		keep := !strings.HasPrefix(key, "tokenizer.")
		v, elem, kept, err := p.value(t, keep)
		if err != nil {
			return nil, err
		}
		if kept {
			h.KV[key] = v
			if t == TypeArray {
				h.Types[key] = elem
			} else {
				h.Types[key] = t
			}
		} else {
			h.Skipped = append(h.Skipped, key)
		}
		h.KVRead++
	}
	p.key = ""
	h.Complete = true
	h.BytesRead = p.off
	if _, ok := h.KV["general.architecture"].(string); !ok {
		return nil, &FormatError{Offset: p.off, Msg: "general.architecture is missing or not a string"}
	}
	return h, nil
}

// bitsSwapped reverses the byte order of a uint32.
func bitsSwapped(v uint32) uint32 {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return binary.BigEndian.Uint32(b[:])
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// string reads a GGUF string (uint64 length + bytes). keep=false skips the
// bytes; a string longer than limit is malformed.
func (p *parser) string(limit uint64, keep bool) (string, error) {
	n, err := p.u64()
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", p.malformed("string length %d is not plausible", n)
	}
	if !keep {
		return "", p.skip(n)
	}
	b, err := p.read(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// value reads one value of type t. With keep, scalars and small arrays are
// returned; otherwise the bytes are skipped. kept reports whether v is the
// value (false: skipped). elem is an array's element type.
func (p *parser) value(t Type, keep bool) (v any, elem Type, kept bool, err error) {
	switch t {
	case TypeString:
		n, err := p.u64()
		if err != nil {
			return nil, 0, false, err
		}
		if n > maxStringLen {
			return nil, 0, false, p.malformed("string length %d is not plausible", n)
		}
		if !keep || n > keepStringLen {
			return nil, 0, false, p.skip(n)
		}
		b, err := p.read(int(n))
		if err != nil {
			return nil, 0, false, err
		}
		return string(b), 0, true, nil
	case TypeArray:
		et, err := p.u32()
		if err != nil {
			return nil, 0, false, err
		}
		elem := Type(et)
		if !elem.valid() {
			return nil, 0, false, p.malformed("unknown array element type %d", et)
		}
		if elem == TypeArray {
			return nil, 0, false, p.malformed("arrays of arrays are not valid in a model header")
		}
		count, err := p.u64()
		if err != nil {
			return nil, 0, false, err
		}
		if count > maxArrayLen {
			return nil, 0, false, p.malformed("array length %d is not plausible", count)
		}
		if !keep || count > keepArrayLen {
			if w := elem.fixedSize(); w > 0 {
				return nil, elem, false, p.skip(count * uint64(w))
			}
			for i := uint64(0); i < count; i++ { // strings: each has its own length
				if _, err := p.string(maxStringLen, false); err != nil {
					return nil, 0, false, err
				}
			}
			return nil, elem, false, nil
		}
		out := make([]any, 0, count)
		for i := uint64(0); i < count; i++ {
			x, _, _, err := p.value(elem, true)
			if err != nil {
				return nil, 0, false, err
			}
			out = append(out, x)
		}
		return out, elem, true, nil
	default:
		w := t.fixedSize()
		b := p.scratch[:w]
		if err := p.fill(b); err != nil {
			return nil, 0, false, err
		}
		if !keep {
			return nil, 0, false, nil
		}
		switch t {
		case TypeUint8:
			return uint64(b[0]), 0, true, nil
		case TypeInt8:
			return int64(int8(b[0])), 0, true, nil
		case TypeUint16:
			return uint64(binary.LittleEndian.Uint16(b)), 0, true, nil
		case TypeInt16:
			return int64(int16(binary.LittleEndian.Uint16(b))), 0, true, nil
		case TypeUint32:
			return uint64(binary.LittleEndian.Uint32(b)), 0, true, nil
		case TypeInt32:
			return int64(int32(binary.LittleEndian.Uint32(b))), 0, true, nil
		case TypeUint64:
			return binary.LittleEndian.Uint64(b), 0, true, nil
		case TypeInt64:
			return int64(binary.LittleEndian.Uint64(b)), 0, true, nil
		case TypeFloat32:
			return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))), 0, true, nil
		case TypeFloat64:
			return math.Float64frombits(binary.LittleEndian.Uint64(b)), 0, true, nil
		case TypeBool:
			if b[0] > 1 {
				return nil, 0, false, p.malformed("bool value %d is neither 0 nor 1", b[0])
			}
			return b[0] == 1, 0, true, nil
		}
	}
	return nil, 0, false, p.malformed("unhandled value type %s", t)
}

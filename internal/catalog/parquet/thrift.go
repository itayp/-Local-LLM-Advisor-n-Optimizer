package parquet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// The Thrift compact protocol, as far as Parquet's metadata needs it: a
// struct decodes into a map from field id to value, and the callers pick the
// fields they read (parquet.thrift names them). Values are int64 for every
// integer width, bool, float64, []byte for binary and strings, []any for
// lists and sets, and tstruct for structs. Maps are not used by the fields
// read here and are refused.

type tstruct map[int16]any

const (
	ctStop   = 0
	ctTrue   = 1
	ctFalse  = 2
	ctByte   = 3
	ctI16    = 4
	ctI32    = 5
	ctI64    = 6
	ctDouble = 7
	ctBinary = 8
	ctList   = 9
	ctSet    = 10
	ctMap    = 11
	ctStruct = 12
)

// maxDepth and maxItems bound what a malformed footer can make the decoder do.
const (
	maxDepth = 32
	maxItems = 1 << 20
)

var errThrift = errors.New("parquet: the file's metadata is not valid Thrift")

type tdecoder struct {
	b     []byte
	p     int
	depth int
}

func (d *tdecoder) byte() (byte, error) {
	if d.p >= len(d.b) {
		return 0, errThrift
	}
	c := d.b[d.p]
	d.p++
	return c, nil
}

func (d *tdecoder) uvarint() (uint64, error) {
	v, n := binary.Uvarint(d.b[d.p:])
	if n <= 0 {
		return 0, errThrift
	}
	d.p += n
	return v, nil
}

func (d *tdecoder) zigzag() (int64, error) {
	u, err := d.uvarint()
	if err != nil {
		return 0, err
	}
	return int64(u>>1) ^ -int64(u&1), nil
}

func (d *tdecoder) value(t byte) (any, error) {
	switch t {
	case ctTrue:
		return true, nil
	case ctFalse:
		return false, nil
	case ctByte:
		c, err := d.byte()
		return int64(int8(c)), err
	case ctI16, ctI32, ctI64:
		return d.zigzag()
	case ctDouble:
		if d.p+8 > len(d.b) {
			return nil, errThrift
		}
		v := math.Float64frombits(binary.LittleEndian.Uint64(d.b[d.p:]))
		d.p += 8
		return v, nil
	case ctBinary:
		n, err := d.uvarint()
		if err != nil || n > uint64(len(d.b)-d.p) {
			return nil, errThrift
		}
		v := d.b[d.p : d.p+int(n)]
		d.p += int(n)
		return v, nil
	case ctList, ctSet:
		h, err := d.byte()
		if err != nil {
			return nil, err
		}
		size, et := uint64(h>>4), h&0x0f
		if size == 15 {
			if size, err = d.uvarint(); err != nil {
				return nil, err
			}
		}
		if size > maxItems {
			return nil, errThrift
		}
		out := make([]any, 0, size)
		for i := uint64(0); i < size; i++ {
			var v any
			if et == ctTrue || et == ctFalse {
				// In a list, a bool is one byte: 1 true, 2 false.
				c, err := d.byte()
				if err != nil {
					return nil, err
				}
				v = c == ctTrue
			} else if v, err = d.value(et); err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case ctStruct:
		return d.structure()
	}
	return nil, fmt.Errorf("%w (type %d)", errThrift, t)
}

func (d *tdecoder) structure() (tstruct, error) {
	d.depth++
	defer func() { d.depth-- }()
	if d.depth > maxDepth {
		return nil, errThrift
	}
	out := tstruct{}
	var last int16
	for {
		h, err := d.byte()
		if err != nil {
			return nil, err
		}
		if h == ctStop {
			return out, nil
		}
		t := h & 0x0f
		id := last
		if delta := int16(h >> 4); delta != 0 {
			id += delta
		} else {
			v, err := d.zigzag()
			if err != nil {
				return nil, err
			}
			id = int16(v)
		}
		last = id
		v, err := d.value(t)
		if err != nil {
			return nil, err
		}
		out[id] = v
	}
}

// decodeStruct reads one struct from the start of b and says how many bytes it took.
func decodeStruct(b []byte) (tstruct, int, error) {
	d := &tdecoder{b: b}
	s, err := d.structure()
	return s, d.p, err
}

func (s tstruct) int(id int16) (int64, bool) {
	v, ok := s[id].(int64)
	return v, ok
}

func (s tstruct) str(id int16) string {
	b, _ := s[id].([]byte)
	return string(b)
}

func (s tstruct) sub(id int16) tstruct {
	v, _ := s[id].(tstruct)
	return v
}

func (s tstruct) list(id int16) []any {
	v, _ := s[id].([]any)
	return v
}

func (s tstruct) boolean(id int16, def bool) bool {
	if v, ok := s[id].(bool); ok {
		return v
	}
	return def
}

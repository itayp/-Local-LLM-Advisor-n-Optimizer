package parquet

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var errSnappy = errors.New("parquet: a page's Snappy data is corrupt")

// snappyDecode is the Snappy block format (the one Parquet uses, without
// framing): the uncompressed length as a varint, then literals and copies.
// want is the page header's uncompressed size, which the result must match.
func snappyDecode(src []byte, want int) ([]byte, error) {
	n, k := binary.Uvarint(src)
	if k <= 0 || n != uint64(want) {
		return nil, errSnappy
	}
	dst := make([]byte, 0, want)
	for p := k; p < len(src); {
		tag := src[p]
		switch tag & 3 {
		case 0: // literal
			l := int(tag >> 2)
			p++
			if l >= 60 {
				extra := l - 59
				if p+extra > len(src) {
					return nil, errSnappy
				}
				l = 0
				for i := 0; i < extra; i++ {
					l |= int(src[p+i]) << (8 * i)
				}
				p += extra
			}
			l++
			if l <= 0 || p+l > len(src) || len(dst)+l > want {
				return nil, errSnappy
			}
			dst = append(dst, src[p:p+l]...)
			p += l
		default:
			var length, offset int
			switch tag & 3 {
			case 1:
				if p+2 > len(src) {
					return nil, errSnappy
				}
				length = 4 + int(tag>>2)&7
				offset = int(tag&0xe0)<<3 | int(src[p+1])
				p += 2
			case 2:
				if p+3 > len(src) {
					return nil, errSnappy
				}
				length = 1 + int(tag>>2)
				offset = int(binary.LittleEndian.Uint16(src[p+1:]))
				p += 3
			case 3:
				if p+5 > len(src) {
					return nil, errSnappy
				}
				length = 1 + int(tag>>2)
				offset = int(binary.LittleEndian.Uint32(src[p+1:]))
				p += 5
			}
			if offset <= 0 || offset > len(dst) || len(dst)+length > want {
				return nil, errSnappy
			}
			start := len(dst) - offset
			for i := 0; i < length; i++ { // may overlap itself: byte by byte
				dst = append(dst, dst[start+i])
			}
		}
	}
	if len(dst) != want {
		return nil, errSnappy
	}
	return dst, nil
}

// rleDecode reads count values of bitWidth bits from Parquet's
// RLE/bit-packed hybrid encoding (definition levels, dictionary indices).
func rleDecode(b []byte, bitWidth, count int) ([]uint32, error) {
	if bitWidth < 0 || bitWidth > 32 {
		return nil, fmt.Errorf("parquet: a bit width of %d", bitWidth)
	}
	out := make([]uint32, 0, count)
	byteWidth := (bitWidth + 7) / 8
	p := 0
	for len(out) < count {
		h, n := binary.Uvarint(b[p:])
		if n <= 0 {
			return nil, errors.New("parquet: run-length data ends early")
		}
		p += n
		if h&1 == 0 { // a run of one value
			run := int(min(h>>1, uint64(count-len(out))))
			if p+byteWidth > len(b) {
				return nil, errors.New("parquet: a run-length run ends early")
			}
			var v uint32
			for i := 0; i < byteWidth; i++ {
				v |= uint32(b[p+i]) << (8 * i)
			}
			p += byteWidth
			for i := 0; i < run && len(out) < count; i++ {
				out = append(out, v)
			}
			continue
		}
		groups := int(h >> 1) // groups of eight values, bit-packed
		nbytes := groups * bitWidth
		if p+nbytes > len(b) {
			return nil, errors.New("parquet: bit-packed data ends early")
		}
		var acc uint64
		var bits int
		q := p
		for i := 0; i < groups*8; i++ {
			for bits < bitWidth {
				acc |= uint64(b[q]) << bits
				q++
				bits += 8
			}
			v := uint32(acc & (1<<bitWidth - 1))
			acc >>= bitWidth
			bits -= bitWidth
			if len(out) < count {
				out = append(out, v)
			}
		}
		p += nbytes
	}
	return out, nil
}

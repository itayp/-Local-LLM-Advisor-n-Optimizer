// Package parquet reads the one kind of Parquet file the advisor needs:
// Arena's leaderboard tables on Hugging Face (build-plan step 9b,
// ARCHITECTURE.md D-56). It is deliberately small — the standard library
// only, like the GGUF reader (D-26):
//
//   - a flat schema (no nested or repeated columns);
//   - BYTE_ARRAY (read as UTF-8 strings), DOUBLE, FLOAT, INT32 and INT64;
//   - PLAIN, PLAIN_DICTIONARY and RLE_DICTIONARY encodings;
//   - data pages of version 1 and 2, and dictionary pages;
//   - no compression, or Snappy.
//
// Anything else — another codec, a nested schema, an encoding not listed —
// is refused with an error that says what it met, never guessed at: a
// changed file is a failed read in words, not a wrong number.
package parquet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// Limits on what one file may ask of the reader.
const (
	MaxRows     = 1 << 22 // rows in a file
	maxPageSize = 64 << 20
)

// Physical types (parquet.thrift Type).
const (
	typeBoolean   = 0
	typeInt32     = 1
	typeInt64     = 2
	typeInt96     = 3
	typeFloat     = 4
	typeDouble    = 5
	typeByteArray = 6
	typeFixed     = 7
)

// Encodings (parquet.thrift Encoding).
const (
	encPlain           = 0
	encPlainDictionary = 2
	encRLE             = 3
	encRLEDictionary   = 8
)

// Codecs (parquet.thrift CompressionCodec).
const (
	codecUncompressed = 0
	codecSnappy       = 1
)

// Page types (parquet.thrift PageType).
const (
	pageData       = 0
	pageDictionary = 2
	pageDataV2     = 3
)

// Repetition (parquet.thrift FieldRepetitionType).
const (
	repRequired = 0
	repOptional = 1
	repRepeated = 2
)

var magic = []byte("PAR1")

// Column is one column of the file's schema.
type Column struct {
	Name     string
	Type     string // "string", "double", "float", "int32", "int64"
	Optional bool
	physical int64
}

// File is a parsed file: its columns and row count; Rows reads the values.
type File struct {
	Columns []Column
	NumRows int64
	data    []byte
	groups  []rowGroup
}

type rowGroup struct {
	rows   int64
	chunks []chunk // by column, in schema order
}

type chunk struct {
	path      string
	codec     int64
	numValues int64
	start     int64 // the dictionary page if there is one, else the first data page
	size      int64 // total compressed size, headers included
}

// Open parses the footer of a whole file held in memory.
func Open(data []byte) (*File, error) {
	if len(data) < 12 || !bytes.Equal(data[:4], magic) || !bytes.Equal(data[len(data)-4:], magic) {
		return nil, errors.New("parquet: not a Parquet file (no PAR1 at both ends)")
	}
	n := int64(binary.LittleEndian.Uint32(data[len(data)-8:]))
	if n <= 0 || n > int64(len(data)-12) {
		return nil, errors.New("parquet: the footer length is out of range")
	}
	meta, _, err := decodeStruct(data[int64(len(data)-8)-n : len(data)-8])
	if err != nil {
		return nil, err
	}
	f := &File{data: data}
	f.NumRows, _ = meta.int(3)
	if f.NumRows < 0 || f.NumRows > MaxRows {
		return nil, fmt.Errorf("parquet: %d rows is more than the reader takes", f.NumRows)
	}

	schema := meta.list(2)
	if len(schema) == 0 {
		return nil, errors.New("parquet: the file has no schema")
	}
	root, _ := schema[0].(tstruct)
	children, _ := root.int(5)
	if int(children) != len(schema)-1 {
		return nil, errors.New("parquet: the schema is nested; only flat tables are read")
	}
	for _, el := range schema[1:] {
		e, _ := el.(tstruct)
		if nc, ok := e.int(5); ok && nc > 0 {
			return nil, fmt.Errorf("parquet: column %q is nested; only flat tables are read", e.str(4))
		}
		rep, _ := e.int(3)
		if rep == repRepeated {
			return nil, fmt.Errorf("parquet: column %q is repeated; only flat tables are read", e.str(4))
		}
		phys, _ := e.int(1)
		c := Column{Name: e.str(4), Optional: rep == repOptional, physical: phys}
		switch phys {
		case typeByteArray:
			c.Type = "string"
		case typeDouble:
			c.Type = "double"
		case typeFloat:
			c.Type = "float"
		case typeInt32:
			c.Type = "int32"
		case typeInt64:
			c.Type = "int64"
		default:
			c.Type = fmt.Sprintf("unsupported(%d)", phys)
		}
		f.Columns = append(f.Columns, c)
	}

	var total int64
	for _, g := range meta.list(4) {
		rg, _ := g.(tstruct)
		rows, _ := rg.int(3)
		group := rowGroup{rows: rows}
		cols := rg.list(1)
		if len(cols) != len(f.Columns) {
			return nil, errors.New("parquet: a row group does not have one chunk per column")
		}
		for _, c := range cols {
			cc, _ := c.(tstruct)
			md := cc.sub(3)
			if md == nil {
				return nil, errors.New("parquet: a column chunk has no metadata (external chunk files are not read)")
			}
			var path string
			for _, p := range md.list(3) {
				if b, ok := p.([]byte); ok {
					path = string(b)
				}
			}
			ch := chunk{path: path}
			ch.codec, _ = md.int(4)
			ch.numValues, _ = md.int(5)
			ch.size, _ = md.int(7)
			ch.start, _ = md.int(9)
			if dict, ok := md.int(11); ok && dict > 0 && dict < ch.start {
				ch.start = dict
			}
			if ch.start < 4 || ch.size < 0 || ch.start+ch.size > int64(len(data)-8)-n {
				return nil, fmt.Errorf("parquet: column %q's pages lie outside the file", path)
			}
			group.chunks = append(group.chunks, ch)
		}
		total += rows
		f.groups = append(f.groups, group)
	}
	if total != f.NumRows {
		return nil, errors.New("parquet: the row groups do not add up to the file's row count")
	}
	return f, nil
}

// Rows reads the named columns (all of them when names is empty) of every
// row. A value is a string, float64 or int64; a null is nil. A column not in
// the file is an error, as is one of a type the reader does not take.
func (f *File) Rows(names ...string) ([]map[string]any, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	all := len(want) == 0
	var idx []int
	for i, c := range f.Columns {
		if all || want[c.Name] {
			if c.Type == "" || c.Type[0] == 'u' {
				return nil, fmt.Errorf("parquet: column %q is of a type the reader does not take (%s)", c.Name, c.Type)
			}
			idx = append(idx, i)
			delete(want, c.Name)
		}
	}
	for n := range want {
		return nil, fmt.Errorf("parquet: the file has no column %q", n)
	}
	rows := make([]map[string]any, 0, f.NumRows)
	for _, g := range f.groups {
		base := len(rows)
		for i := int64(0); i < g.rows; i++ {
			rows = append(rows, make(map[string]any, len(idx)))
		}
		for _, i := range idx {
			col := f.Columns[i]
			vals, err := f.readChunk(col, g.chunks[i], g.rows)
			if err != nil {
				return nil, fmt.Errorf("parquet: column %q: %w", col.Name, err)
			}
			for r, v := range vals {
				rows[base+r][col.Name] = v
			}
		}
	}
	return rows, nil
}

// readChunk reads one column chunk: an optional dictionary page, then data
// pages until the chunk's values are all read.
func (f *File) readChunk(col Column, ch chunk, rows int64) ([]any, error) {
	if ch.codec != codecUncompressed && ch.codec != codecSnappy {
		return nil, fmt.Errorf("compressed with codec %d; the reader takes none or Snappy", ch.codec)
	}
	if ch.numValues != rows {
		return nil, fmt.Errorf("%d values for %d rows", ch.numValues, rows)
	}
	out := make([]any, 0, rows)
	var dict []any
	p := ch.start
	end := ch.start + ch.size
	for int64(len(out)) < rows {
		if p >= end {
			return nil, errors.New("the pages end before the values do")
		}
		hdr, n, err := decodeStruct(f.data[p:end])
		if err != nil {
			return nil, err
		}
		p += int64(n)
		typ, _ := hdr.int(1)
		usize, _ := hdr.int(2)
		csize, _ := hdr.int(3)
		if csize < 0 || usize < 0 || usize > maxPageSize || p+csize > end {
			return nil, errors.New("a page's size is out of range")
		}
		body := f.data[p : p+csize]
		p += csize

		switch typ {
		case pageDictionary:
			dh := hdr.sub(7)
			count, _ := dh.int(1)
			enc, _ := dh.int(2)
			if enc != encPlain && enc != encPlainDictionary {
				return nil, fmt.Errorf("a dictionary page encoded with %d", enc)
			}
			raw, err := decompress(ch.codec, body, int(usize))
			if err != nil {
				return nil, err
			}
			if dict, _, err = plain(col.physical, raw, int(count)); err != nil {
				return nil, err
			}
		case pageData:
			dh := hdr.sub(5)
			count, _ := dh.int(1)
			enc, _ := dh.int(2)
			raw, err := decompress(ch.codec, body, int(usize))
			if err != nil {
				return nil, err
			}
			var defs []uint32
			if col.Optional {
				if len(raw) < 4 {
					return nil, errors.New("a data page ends before its definition levels")
				}
				l := int(binary.LittleEndian.Uint32(raw))
				if 4+l > len(raw) {
					return nil, errors.New("a data page's definition levels run past it")
				}
				if defs, err = rleDecode(raw[4:4+l], 1, int(count)); err != nil {
					return nil, err
				}
				raw = raw[4+l:]
			}
			vals, err := values(col.physical, enc, raw, int(count), defs, dict)
			if err != nil {
				return nil, err
			}
			out = append(out, vals...)
		case pageDataV2:
			dh := hdr.sub(8)
			count, _ := dh.int(1)
			enc, _ := dh.int(4)
			dlen, _ := dh.int(5)
			rlen, _ := dh.int(6)
			if rlen != 0 {
				return nil, errors.New("a data page has repetition levels; only flat tables are read")
			}
			if dlen < 0 || dlen > int64(len(body)) {
				return nil, errors.New("a data page's definition levels run past it")
			}
			var defs []uint32
			if col.Optional {
				if defs, err = rleDecode(body[:dlen], 1, int(count)); err != nil {
					return nil, err
				}
			}
			rest := body[dlen:]
			if dh.boolean(7, true) {
				if rest, err = decompress(ch.codec, rest, int(usize-dlen)); err != nil {
					return nil, err
				}
			}
			vals, err := values(col.physical, enc, rest, int(count), defs, dict)
			if err != nil {
				return nil, err
			}
			out = append(out, vals...)
		default:
			// An index page, or a type added later: not needed, skipped.
		}
	}
	if int64(len(out)) != rows {
		return nil, fmt.Errorf("%d values read for %d rows", len(out), rows)
	}
	return out, nil
}

func decompress(codec int64, b []byte, size int) ([]byte, error) {
	if codec == codecSnappy {
		return snappyDecode(b, size)
	}
	if len(b) != size {
		return nil, errors.New("an uncompressed page's size does not match its header")
	}
	return b, nil
}

// values decodes one page's values; defs (optional columns) says which of
// count slots hold one.
func values(phys, enc int64, raw []byte, count int, defs []uint32, dict []any) ([]any, error) {
	present := count
	if defs != nil {
		present = 0
		for _, d := range defs {
			if d == 1 {
				present++
			}
		}
	}
	var got []any
	switch enc {
	case encPlain:
		var err error
		if got, _, err = plain(phys, raw, present); err != nil {
			return nil, err
		}
	case encPlainDictionary, encRLEDictionary:
		if dict == nil {
			return nil, errors.New("a dictionary-encoded page with no dictionary")
		}
		if len(raw) < 1 {
			return nil, errors.New("a dictionary-encoded page is empty")
		}
		ids, err := rleDecode(raw[1:], int(raw[0]), present)
		if err != nil {
			return nil, err
		}
		got = make([]any, len(ids))
		for i, id := range ids {
			if int(id) >= len(dict) {
				return nil, errors.New("a dictionary index is out of range")
			}
			got[i] = dict[id]
		}
	default:
		return nil, fmt.Errorf("values encoded with %d; the reader takes plain and dictionary encodings", enc)
	}
	if defs == nil {
		return got, nil
	}
	out := make([]any, count)
	j := 0
	for i, d := range defs {
		if d == 1 {
			out[i] = got[j]
			j++
		}
	}
	return out, nil
}

// plain decodes count PLAIN values of a physical type, and says how many
// bytes they took.
func plain(phys int64, b []byte, count int) ([]any, int, error) {
	out := make([]any, 0, count)
	p := 0
	short := errors.New("plain values end early")
	for i := 0; i < count; i++ {
		switch phys {
		case typeByteArray:
			if p+4 > len(b) {
				return nil, 0, short
			}
			l := int(binary.LittleEndian.Uint32(b[p:]))
			p += 4
			if l < 0 || p+l > len(b) {
				return nil, 0, short
			}
			s := b[p : p+l]
			if !utf8.Valid(s) {
				return nil, 0, errors.New("a text value is not UTF-8")
			}
			out = append(out, string(s))
			p += l
		case typeDouble:
			if p+8 > len(b) {
				return nil, 0, short
			}
			out = append(out, math.Float64frombits(binary.LittleEndian.Uint64(b[p:])))
			p += 8
		case typeFloat:
			if p+4 > len(b) {
				return nil, 0, short
			}
			out = append(out, float64(math.Float32frombits(binary.LittleEndian.Uint32(b[p:]))))
			p += 4
		case typeInt32:
			if p+4 > len(b) {
				return nil, 0, short
			}
			out = append(out, int64(int32(binary.LittleEndian.Uint32(b[p:]))))
			p += 4
		case typeInt64:
			if p+8 > len(b) {
				return nil, 0, short
			}
			out = append(out, int64(binary.LittleEndian.Uint64(b[p:])))
			p += 8
		default:
			return nil, 0, fmt.Errorf("physical type %d is not read", phys)
		}
	}
	return out, p, nil
}

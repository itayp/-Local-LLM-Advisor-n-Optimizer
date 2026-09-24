package parquet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func read(t *testing.T, name string) *File {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Open(b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return f
}

type expected struct {
	Rows    int              `json:"rows"`
	Columns []string         `json:"columns"`
	Values  []map[string]any `json:"values"`
}

func want(t *testing.T) map[string]expected {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]expected
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// normalise turns the reader's int64s into float64s, as JSON decodes numbers.
func normalise(rows []map[string]any) []map[string]any {
	for _, r := range rows {
		for k, v := range r {
			if i, ok := v.(int64); ok {
				r[k] = float64(i)
			}
		}
	}
	return rows
}

// Every file reads to exactly what pyarrow reads from it (testdata/gen.py).
func TestReadsWhatPyarrowReads(t *testing.T) {
	exp := want(t)
	arena := exp["arena-text.parquet"]
	nulls := exp["nulls.parquet"]
	for file, w := range map[string]expected{
		"arena-text.parquet":   arena, // Arena's own settings: Snappy, dictionary pages, data page v1
		"v2.parquet":           arena, // data page v2, several row groups
		"uncompressed.parquet": arena,
		"plain.parquet":        arena, // no dictionary
		"nulls.parquet":        nulls, // nulls, int32 and int64, several pages per chunk
		"nulls-v2.parquet":     nulls,
	} {
		f := read(t, file)
		var names []string
		for _, c := range f.Columns {
			names = append(names, c.Name)
		}
		if !reflect.DeepEqual(names, w.Columns) || f.NumRows != int64(w.Rows) {
			t.Errorf("%s: columns %v, %d rows", file, names, f.NumRows)
			continue
		}
		rows, err := f.Rows()
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if got := normalise(rows); !reflect.DeepEqual(got, w.Values) {
			for i := range got {
				if !reflect.DeepEqual(got[i], w.Values[i]) {
					t.Errorf("%s: row %d = %v, want %v", file, i, got[i], w.Values[i])
					break
				}
			}
		}
	}
}

func TestReadsOnlyTheColumnsAskedFor(t *testing.T) {
	f := read(t, "arena-text.parquet")
	rows, err := f.Rows("model_name", "rating")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows[0]) != 2 || rows[0]["model_name"] == nil {
		t.Errorf("row 0 = %v", rows[0])
	}
	if _, err := f.Rows("model_name", "no_such_column"); err == nil || !strings.Contains(err.Error(), `no column "no_such_column"`) {
		t.Errorf("a missing column: %v", err)
	}
}

// What the reader does not take, it refuses in words.
func TestRefusesWhatItDoesNotRead(t *testing.T) {
	b, _ := os.ReadFile(filepath.Join("testdata", "nulls.parquet"))
	for name, bad := range map[string][]byte{
		"not parquet":     []byte("hello, world"),
		"a cut file":      b[:len(b)/2],
		"a broken footer": append(append([]byte{}, b[:len(b)-8]...), 0xff, 0xff, 0xff, 0x7f, 'P', 'A', 'R', '1'),
	} {
		if _, err := Open(bad); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	// A corrupt page fails the read, never a wrong value.
	c := append([]byte{}, b...)
	for i := 40; i < 80; i++ {
		c[i] ^= 0x55
	}
	if f, err := Open(c); err == nil {
		if _, err := f.Rows(); err == nil {
			t.Error("a corrupt page was read")
		}
	}
}

func TestSnappyAndRLE(t *testing.T) {
	// "abcabcabcabc": a literal of three, then a copy of nine at offset three.
	got, err := snappyDecode([]byte{12, 2 << 2, 'a', 'b', 'c', 1 | (5 << 2), 3}, 12)
	if err != nil || string(got) != "abcabcabcabc" {
		t.Errorf("snappy: %q %v", got, err)
	}
	if _, err := snappyDecode([]byte{12, 2 << 2, 'a', 'b', 'c', 1 | (5 << 2), 9}, 12); err == nil {
		t.Error("a copy from before the start was decoded")
	}
	// A run of five 1s, then one group of eight bit-packed 2-bit values.
	vals, err := rleDecode([]byte{5 << 1, 1, 1<<1 | 1, 0b11100100, 0b00011011}, 2, 13)
	if err != nil || !reflect.DeepEqual(vals, []uint32{1, 1, 1, 1, 1, 0, 1, 2, 3, 3, 2, 1, 0}) {
		t.Errorf("rle: %v %v", vals, err)
	}
}

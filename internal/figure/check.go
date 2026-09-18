package figure

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Tag is the struct tag that exempts a numeric field from the figure rule.
// The only accepted value is "n/a": the field is neither estimated nor
// measured (an id, a count, a timestamp, a configuration value, a value read
// from the OS). The comment next to the field says which.
const Tag = "source"

// Check walks the exported struct types in vals — recursively through
// nested structs, pointers, slices, arrays and maps — and returns one
// message per numeric field that is neither part of a figure type from this
// package nor tagged `source:"n/a"`.
//
// It is the machine-checkable half of product rule 4. internal/server keeps
// the list of API types and runs Check over it in a test, so a new numeric
// field in any API type is a failing build until its provenance is stated.
func Check(vals ...any) []string {
	c := &checker{seen: map[reflect.Type]bool{}}
	for _, v := range vals {
		c.walk(reflect.TypeOf(v), "")
	}
	sort.Strings(c.problems)
	return c.problems
}

type checker struct {
	seen     map[reflect.Type]bool
	problems []string
}

var figureTypes = map[reflect.Type]bool{
	reflect.TypeOf(Bytes{}): true,
	reflect.TypeOf(Rate{}):  true,
}

func (c *checker) walk(t reflect.Type, path string) {
	if t == nil {
		return
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		c.walk(t.Elem(), path)
		return
	case reflect.Map:
		c.walk(t.Elem(), path)
		return
	case reflect.Struct:
		// fall through to the struct walk below
	default:
		return
	}
	if figureTypes[t] || c.seen[t] {
		return
	}
	c.seen[t] = true

	name := t.String()
	if path != "" {
		name = path
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		fieldPath := name + "." + f.Name
		if isNumeric(leaf(f.Type)) {
			if strings.TrimSpace(f.Tag.Get(Tag)) != "n/a" {
				c.problems = append(c.problems, fmt.Sprintf(
					"%s is %s: make it a figure.Bytes / figure.Rate, or tag it `%s:\"n/a\"` and say why",
					fieldPath, f.Type, Tag))
			}
			continue
		}
		c.walk(f.Type, fieldPath)
	}
}

// leaf strips pointers, slices, arrays and map values so that *float64,
// []uint64 and map[string]int are judged by the number underneath.
func leaf(t reflect.Type) reflect.Type {
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			t = t.Elem()
		default:
			return t
		}
	}
}

func isNumeric(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

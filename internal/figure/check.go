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

// figureTypes are the provenance-carrying types: a local figure (Bytes,
// Rate: estimated or measured) or a public value (Public: someone else's,
// with its origin).
var figureTypes = map[reflect.Type]bool{
	reflect.TypeOf(Bytes{}):  true,
	reflect.TypeOf(Rate{}):   true,
	reflect.TypeOf(Public{}): true,
}

var (
	localTypes = map[reflect.Type]bool{reflect.TypeOf(Bytes{}): true, reflect.TypeOf(Rate{}): true}
	publicType = reflect.TypeOf(Public{})
)

// CheckSeparation walks the struct types in vals, as Check does, and
// returns one message per struct that directly holds both a public value
// (Public, or a pointer, slice or map of them) and a local figure (Bytes or
// Rate, likewise). A struct that holds them in sibling sub-objects passes:
// the rule is that no struct, and so no JSON object, puts someone else's
// number beside one about this machine (research/EXTERNAL_SOURCES.md P-2).
func CheckSeparation(vals ...any) []string {
	seen := map[reflect.Type]bool{}
	var problems []string
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		t = leaf(t)
		if t.Kind() != reflect.Struct || seen[t] || figureTypes[t] {
			return
		}
		seen[t] = true
		var public, local []string
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			switch ft := leaf(f.Type); {
			case ft == publicType:
				public = append(public, f.Name)
			case localTypes[ft]:
				local = append(local, f.Name)
			default:
				walk(f.Type)
			}
		}
		if len(public) > 0 && len(local) > 0 {
			problems = append(problems, fmt.Sprintf(
				"%s holds public value(s) %v beside local figure(s) %v: put them in sibling sub-objects (\"public\", \"local\")",
				t, public, local))
		}
	}
	for _, v := range vals {
		walk(reflect.TypeOf(v))
	}
	sort.Strings(problems)
	return problems
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

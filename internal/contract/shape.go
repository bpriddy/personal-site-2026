package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"slices"
	"strings"
)

// The published shape of the content: which collections have items, and
// which of their fields carry a value in at least one item. The observer
// compares it with what each front end reads (docs/observer.md, "Content
// drift"), so a front end made before a collection or field existed gets
// rebuilt to show it.

// IgnoredFields are presentational-only fields, left out of the shape: a front
// end that doesn't use them still shows the content. Keep this list small.
//
//   - palette: a project's own colours, an optional styling hint.
//
// A media item's own keys (kind, src, poster, width, height, alt) are not
// fields of the shape at all: "media" counts as one field.
var IgnoredFields = map[string]bool{"palette": true}

// FieldShape is one field of a collection and how many items have a value.
type FieldShape struct {
	Name     string `json:"name"`
	NonEmpty int    `json:"nonEmpty"`
}

// CollectionShape is one collection with at least one published item.
type CollectionShape struct {
	Name   string       `json:"name"`
	Items  int          `json:"items"`
	Fields []FieldShape `json:"fields"` // non-empty in at least one item, by name
}

// Field returns the named field's shape.
func (c CollectionShape) Field(name string) (FieldShape, bool) {
	for _, f := range c.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldShape{}, false
}

// Shape is the published shape of a payload.
type Shape struct {
	Collections []CollectionShape `json:"collections"` // pages, experiments, projects (those with items)
}

// Collection returns the named collection's shape; false if it has no items.
func (s Shape) Collection(name string) (CollectionShape, bool) {
	for _, c := range s.Collections {
		if c.Name == name {
			return c, true
		}
	}
	return CollectionShape{}, false
}

// Fingerprint identifies the shape's collections and field names (not the
// counts): it changes when a collection gains its first item or a field its
// first value, and not when items are edited.
func (s Shape) Fingerprint() string {
	h := sha256.New()
	for _, c := range s.Collections {
		h.Write([]byte(c.Name))
		h.Write([]byte{0})
		for _, f := range c.Fields {
			h.Write([]byte(f.Name))
			h.Write([]byte{1})
		}
		h.Write([]byte{2})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// NamedItems is one collection of a payload.
type NamedItems struct {
	Name  string
	Items []Item
}

// Collections returns the payload's collections by name, in a fixed order.
func (s Site) Collections() []NamedItems {
	return []NamedItems{{Pages, s.Pages}, {Experiments, s.Experiments}, {Projects, s.Projects}}
}

// ShapeOf computes a payload's published shape. Structural keys (the slug,
// and any "_"-prefixed key such as _generated or _collection) and
// IgnoredFields are left out. Extra (generated) fields count like any other.
func ShapeOf(site Site) Shape {
	var out Shape
	for _, col := range site.Collections() {
		if len(col.Items) == 0 {
			continue
		}
		counts := map[string]int{}
		for _, it := range col.Items {
			for k, v := range it {
				if !shapeField(col.Name, k) {
					continue
				}
				if _, ok := counts[k]; !ok {
					counts[k] = 0
				}
				if NonEmpty(v) {
					counts[k]++
				}
			}
		}
		cs := CollectionShape{Name: col.Name, Items: len(col.Items), Fields: []FieldShape{}}
		for k, n := range counts {
			if n > 0 {
				cs.Fields = append(cs.Fields, FieldShape{Name: k, NonEmpty: n})
			}
		}
		slices.SortFunc(cs.Fields, func(a, b FieldShape) int { return strings.Compare(a.Name, b.Name) })
		out.Collections = append(out.Collections, cs)
	}
	return out
}

// shapeField reports whether key is part of a collection's shape.
func shapeField(collection, key string) bool {
	if key == "" || strings.HasPrefix(key, "_") || IgnoredFields[key] {
		return false
	}
	if d := Declared[collection]; len(d) > 0 && key == d[0] {
		return false // the slug
	}
	return key != "slug"
}

// NonEmpty reports whether a payload value carries content: a non-blank
// string, a non-empty list or map, a non-zero number, true.
func NonEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	case bool:
		return x
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() > 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return !rv.IsZero()
	}
	return true
}

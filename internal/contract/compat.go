package contract

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Kinds of breaking change reported by Breaking.
const (
	RemovedCollection = "removed collection"
	RemovedProperty   = "removed property"
	ChangedType       = "changed type"
	NewlyRequired     = "newly required"
	NoLongerRequired  = "no longer required"
)

// A Change is one non-additive difference between two versions of the
// contract schema.
type Change struct {
	Kind string
	Path string // JSON-pointer-ish location in the old schema's instance shape, e.g. "pages[].title"
}

func (c Change) String() string { return c.Kind + ": " + c.Path }

// Breaking compares two JSON Schemas of the content contract and returns every
// change that could break a front end written against oldSchema: a removed
// collection or property, a changed type (including "type", "const",
// "items" and "additionalProperties" narrowing or widening), a property that
// became required, or one that stopped being required. New optional
// properties, new collections, descriptions and other annotations are
// additive and pass. Local "$ref"s ("#/$defs/...") are followed.
func Breaking(oldSchema, newSchema []byte) ([]Change, error) {
	var o, n map[string]any
	if err := json.Unmarshal(oldSchema, &o); err != nil {
		return nil, fmt.Errorf("old schema: %w", err)
	}
	if err := json.Unmarshal(newSchema, &n); err != nil {
		return nil, fmt.Errorf("new schema: %w", err)
	}
	c := &comparer{oldRoot: o, newRoot: n, active: map[[2]string]bool{}}
	c.compare("", o, n)
	slices.SortFunc(c.out, func(a, b Change) int {
		return strings.Compare(a.Path+"\x00"+a.Kind, b.Path+"\x00"+b.Kind)
	})
	return c.out, c.err
}

type comparer struct {
	oldRoot, newRoot map[string]any
	active           map[[2]string]bool // $ref pairs being compared (cycle guard)
	out              []Change
	err              error
}

func (c *comparer) add(kind, path string) {
	if path == "" {
		path = "(root)"
	}
	c.out = append(c.out, Change{kind, path})
}

// resolve follows a local $ref; it returns the target and the ref (or "").
func resolve(root, s map[string]any) (map[string]any, string, error) {
	ref, ok := s["$ref"].(string)
	if !ok {
		return s, "", nil
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, ref, fmt.Errorf("unsupported $ref %q (only local refs)", ref)
	}
	var cur any = root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, ref, fmt.Errorf("$ref %q: not found", ref)
		}
		cur = m[part]
	}
	target, ok := cur.(map[string]any)
	if !ok {
		return nil, ref, fmt.Errorf("$ref %q: not a schema object", ref)
	}
	return target, ref, nil
}

func (c *comparer) compare(path string, o, n map[string]any) {
	o, oref, err := resolve(c.oldRoot, o)
	if err == nil {
		var nref string
		n, nref, err = resolve(c.newRoot, n)
		if oref != "" || nref != "" {
			key := [2]string{oref, nref}
			if c.active[key] {
				return // a recursive schema: already being compared further up
			}
			c.active[key] = true
			defer delete(c.active, key)
		}
	}
	if err != nil {
		if c.err == nil {
			c.err = fmt.Errorf("%s: %w", path, err)
		}
		return
	}

	if !reflect.DeepEqual(typeSet(o["type"]), typeSet(n["type"])) {
		c.add(ChangedType, path)
	}
	if !reflect.DeepEqual(o["const"], n["const"]) {
		c.add(ChangedType, path)
	}

	oreq, nreq := stringSet(o["required"]), stringSet(n["required"])
	for name := range nreq {
		if !oreq[name] {
			c.add(NewlyRequired, join(path, name))
		}
	}
	for name := range oreq {
		if !nreq[name] {
			c.add(NoLongerRequired, join(path, name))
		}
	}

	oprops, _ := o["properties"].(map[string]any)
	nprops, _ := n["properties"].(map[string]any)
	for name, op := range oprops {
		np, ok := nprops[name]
		if !ok {
			if path == "" {
				c.add(RemovedCollection, name)
			} else {
				c.add(RemovedProperty, join(path, name))
			}
			continue
		}
		c.compareSub(join(path, name), op, np)
	}

	switch oi, ni := o["items"], n["items"]; {
	case oi == nil && ni == nil:
	case oi == nil || ni == nil:
		c.add(ChangedType, path+"[]")
	default:
		c.compareSub(path+"[]", oi, ni)
	}

	// additionalProperties: absent means true (anything goes)
	oa, na := o["additionalProperties"], n["additionalProperties"]
	if oa == nil {
		oa = true
	}
	if na == nil {
		na = true
	}
	_, oschema := oa.(map[string]any)
	_, nschema := na.(map[string]any)
	switch {
	case oschema && nschema:
		c.compareSub(join(path, "*"), oa, na)
	case !reflect.DeepEqual(oa, na):
		c.add(ChangedType, join(path, "*"))
	}
}

// compareSub compares two subschemas that may be booleans.
func (c *comparer) compareSub(path string, o, n any) {
	om, ook := o.(map[string]any)
	nm, nok := n.(map[string]any)
	if ook && nok {
		c.compare(path, om, nm)
		return
	}
	if !reflect.DeepEqual(o, n) {
		c.add(ChangedType, path)
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// typeSet normalizes "type" (a string or a list) to a sorted list; nil if absent.
func typeSet(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return nil
}

func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	if l, ok := v.([]any); ok {
		for _, x := range l {
			out[fmt.Sprint(x)] = true
		}
	}
	return out
}

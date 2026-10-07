package observer

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/bpriddy/personal-site-2026/internal/contract"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
)

// The static scan behind content-drift detection (docs/observer.md, "Content
// drift"): which collections and fields a prompted front end's code reads.
// It is deliberately lenient: anything that looks like a read counts, so the
// observer only rebuilds a front end that plainly never touches the content.

// FieldThreshold: a field is only expected of a front end that reads its
// collection when at least this share of the collection's items have a value
// (a rare field isn't worth a rebuild).
const FieldThreshold = 0.25

// builtinReads lists the collections each built-in front end renders. Their
// code is in the repo (Rust/WASM), so it can't be scanned here; one missing
// from this table is taken to render none. Keep it in step with the code.
var builtinReads = map[string][]string{
	"builtin/site": {contract.Pages, contract.Experiments, contract.Projects, contract.Experience},
	// the site version of Particle Stream (frontends/stream/content.js)
	"builtin/stream": {contract.Pages, contract.Experiments, contract.Projects, contract.Experience},
}

// oneAccessor names the host API's one-item accessor for a collection.
var oneAccessor = map[string]string{
	contract.Pages:       "page",
	contract.Experiments: "experiment",
	contract.Projects:    "project",
}

var (
	scriptBlock  = regexp.MustCompile(`(?is)<script\b[^>]*>(.*?)</script\s*>`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	htmlComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	// a line comment: "//" at the start of a line or after a space or
	// punctuation, so "https://..." inside a string survives
	lineComment = regexp.MustCompile(`(?m)(^|[\s;{}(),])//[^\n]*`)
)

// frontendCode returns the code a front end runs: its .js/.mjs files and the
// inline scripts of its HTML files, without comments. Styles and markup are
// left out, so a CSS class or a heading named "projects" doesn't count as
// reading the projects.
func frontendCode(files revfiles.Files) string {
	var sb strings.Builder
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		body := string(files[n])
		switch strings.ToLower(path.Ext(n)) {
		case ".js", ".mjs":
			sb.WriteString(body)
			sb.WriteByte('\n')
		case ".html", ".htm":
			body = htmlComment.ReplaceAllString(body, "")
			for _, m := range scriptBlock.FindAllStringSubmatch(body, -1) {
				sb.WriteString(m[1])
				sb.WriteByte('\n')
			}
		}
	}
	code := blockComment.ReplaceAllString(sb.String(), "")
	return lineComment.ReplaceAllString(code, "$1")
}

var (
	patMu    sync.Mutex
	patCache = map[string]*regexp.Regexp{}
)

// pattern compiles (once) the regexp for reading name: a string literal
// ("projects", 'projects.0.title', `projects`), a property access
// (.projects, site.content.projects), or a destructured name
// ({ pages, projects }). With call set, a method call (site.project(...))
// counts too.
func pattern(name string, call string) *regexp.Regexp {
	key := name + "\x00" + call
	patMu.Lock()
	defer patMu.Unlock()
	if re, ok := patCache[key]; ok {
		return re
	}
	n := regexp.QuoteMeta(name)
	alts := []string{
		"[\"'`]" + n + "[\"'`.\\[]",
		`\.\s*` + n + `\b`,
		`[{,]\s*` + n + `\s*[,}:=]`,
	}
	if call != "" {
		alts = append(alts, `\.\s*`+regexp.QuoteMeta(call)+`\s*\(`)
	}
	re := regexp.MustCompile(strings.Join(alts, "|"))
	patCache[key] = re
	return re
}

// readsCollection reports whether code reads the collection: by name
// (site.projects(), site.collection("projects"), content.projects, ...) or
// through its one-item accessor (site.project(slug)).
func readsCollection(code, collection string) bool {
	return pattern(collection, oneAccessor[collection]).MatchString(code)
}

// readsField reports whether code reads a field: site.field(item, "client"),
// item.client, { client } = item, ...
func readsField(code, field string) bool {
	return pattern(field, "").MatchString(code)
}

// missing is one piece of content a front end doesn't show: a whole
// collection (Field == "") or one field of a collection it does read.
type missing struct {
	Collection string `json:"collection"`
	Field      string `json:"field,omitempty"`
	Items      int    `json:"items"`              // published items in the collection
	NonEmpty   int    `json:"nonEmpty,omitempty"` // items with a value (fields)
}

// key is the missing piece's name in a fingerprint: "projects" or
// "experiments.link".
func (m missing) key() string {
	if m.Field == "" {
		return m.Collection
	}
	return m.Collection + "." + m.Field
}

// expected reports whether a front end that reads a collection should show
// the field: enough of the items have a value.
func expected(c contract.CollectionShape, f contract.FieldShape) bool {
	return c.Items > 0 && float64(f.NonEmpty) >= FieldThreshold*float64(c.Items)
}

// scanDrift compares a prompted front end's code with the published shape:
// collections it never reads, and widely used fields of the ones it does.
// The result is sorted by key.
func scanDrift(files revfiles.Files, shape contract.Shape) []missing {
	code := frontendCode(files)
	var out []missing
	for _, c := range shape.Collections {
		if !readsCollection(code, c.Name) {
			out = append(out, missing{Collection: c.Name, Items: c.Items})
			continue
		}
		for _, f := range c.Fields {
			if expected(c, f) && !readsField(code, f.Name) {
				out = append(out, missing{Collection: c.Name, Field: f.Name, Items: c.Items, NonEmpty: f.NonEmpty})
			}
		}
	}
	sortMissing(out)
	return out
}

// builtinDrift is scanDrift for a built-in front end, from builtinReads
// (collections only: its fields can't be scanned).
func builtinDrift(id string, shape contract.Shape) []missing {
	var out []missing
	for _, c := range shape.Collections {
		if !slices.Contains(builtinReads[id], c.Name) {
			out = append(out, missing{Collection: c.Name, Items: c.Items})
		}
	}
	sortMissing(out)
	return out
}

func sortMissing(m []missing) {
	slices.SortFunc(m, func(a, b missing) int { return strings.Compare(a.key(), b.key()) })
}

// fingerprint identifies a missing set (its sorted keys).
func fingerprint(m []missing) string {
	keys := make([]string, len(m))
	for i, x := range m {
		keys[i] = x.key()
	}
	return signature(append([]string{"drift"}, keys...)...)
}

// unresolved returns the pieces of after that were part of before: the same
// key, or a field of a collection that was missing as a whole. A rebuild is
// resolved when this is empty.
func unresolved(before, after []missing) []missing {
	var out []missing
	for _, a := range after {
		for _, b := range before {
			if a.key() == b.key() || (b.Field == "" && a.Collection == b.Collection) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

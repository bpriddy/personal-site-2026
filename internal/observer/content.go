package observer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// item is one content item as the observer sees it: its human fields, in a
// fixed order (the contract's declared fields for its collection).
type item struct {
	collection string
	slug       string
	published  bool
	names      []string
	fields     map[string]string
}

var errNoItem = errors.New("observer: no such published item")

func pageItem(p content.Page) item {
	return item{collection: "pages", slug: p.Slug, published: p.Published,
		names: []string{"title", "body"}, fields: map[string]string{"title": p.Title, "body": p.Body}}
}

func experimentItem(e content.Experiment) item {
	return item{collection: "experiments", slug: e.Slug, published: e.Published,
		names: []string{"title", "summary"}, fields: map[string]string{"title": e.Title, "summary": e.Summary}}
}

// projectItem: a project's own generatable text. Its factual fields (client,
// year, link, ...; contract.Factual) aren't the observer's to fill, so they
// aren't part of the item: a gap on one goes to review.
func projectItem(p content.Project) item {
	return item{collection: "projects", slug: p.Slug, published: p.Published,
		names: []string{"title", "summary"}, fields: map[string]string{"title": p.Title, "summary": p.Summary}}
}

// loadItem returns a published item; errNoItem if it is absent or unpublished
// (front ends only ever see published content).
func (o *Observer) loadItem(ctx context.Context, collection, slug string) (item, error) {
	var it item
	switch collection {
	case "pages":
		p, err := o.content.Page(ctx, slug)
		if err != nil {
			return it, notFoundAs(err)
		}
		it = pageItem(p)
	case "experiments":
		e, err := o.content.Experiment(ctx, slug)
		if err != nil {
			return it, notFoundAs(err)
		}
		it = experimentItem(e)
	case "projects":
		ps := store.ProjectsOf(o.content)
		if ps == nil {
			return it, errNoItem
		}
		p, err := ps.Project(ctx, slug)
		if err != nil {
			return it, notFoundAs(err)
		}
		it = projectItem(p)
	default:
		return it, errNoItem
	}
	if !it.published {
		return it, errNoItem
	}
	return it, nil
}

func notFoundAs(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return errNoItem
	}
	return err
}

// allItems returns every item, published or not, keyed by collection and slug.
func (o *Observer) allItems(ctx context.Context) (map[[2]string]item, error) {
	pages, err := o.content.Pages(ctx)
	if err != nil {
		return nil, err
	}
	exps, err := o.content.Experiments(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[[2]string]item, len(pages)+len(exps))
	for _, p := range pages {
		out[[2]string{"pages", p.Slug}] = pageItem(p)
	}
	for _, e := range exps {
		out[[2]string{"experiments", e.Slug}] = experimentItem(e)
	}
	if ps := store.ProjectsOf(o.content); ps != nil {
		projects, err := ps.Projects(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range projects {
			out[[2]string{"projects", p.Slug}] = projectItem(p)
		}
	}
	return out, nil
}

// native reports whether field is one of the item's own (human) fields.
func (it item) native(field string) bool {
	_, ok := it.fields[field]
	return ok
}

// hash identifies the item's human content; a generated value records the
// hash it came from, and a mismatch later means it is stale.
func (it item) hash() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", it.collection, it.slug)
	for _, n := range it.names {
		fmt.Fprintf(h, "%s\x00%s\x00", n, it.fields[n])
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// others returns the item's non-empty human fields except field.
func (it item) others(field string) map[string]string {
	out := map[string]string{}
	for _, n := range it.names {
		if v := strings.TrimSpace(it.fields[n]); n != field && v != "" {
			out[n] = v
		}
	}
	return out
}

// minSourceChars is how much other content (non-space characters) an item
// needs before the observer generates from it; below that it asks for review.
const minSourceChars = 40

func enoughSource(src map[string]string) bool {
	n := 0
	for _, v := range src {
		for _, c := range v {
			if !unicode.IsSpace(c) {
				n++
			}
		}
	}
	return n >= minSourceChars
}

func (it item) label() string {
	if it.collection == "pages" && it.slug == "" {
		return "the home page"
	}
	return fmt.Sprintf("%q", it.slug)
}

// Limits on generated values.
const (
	maxTextValue = 400 // runes
	maxListItems = 12
	maxListItem  = 80 // runes
)

// encodeValue validates v against expect and encodes it for storage: text as
// is, list/number/bool as JSON.
func encodeValue(expect string, v any) (string, error) {
	switch expect {
	case "text":
		s, ok := v.(string)
		s = strings.TrimSpace(s)
		if !ok || s == "" {
			return "", errors.New("expected non-empty text")
		}
		if utf8.RuneCountInString(s) > maxTextValue {
			return "", errors.New("text too long")
		}
		return s, nil
	case "list":
		var items []string
		switch l := v.(type) {
		case []string:
			items = l
		case []any:
			for _, x := range l {
				s, ok := x.(string)
				if !ok {
					return "", errors.New("list of non-strings")
				}
				items = append(items, s)
			}
		default:
			return "", errors.New("expected a list")
		}
		var out []string
		for _, s := range items {
			if s = strings.TrimSpace(s); s != "" {
				if utf8.RuneCountInString(s) > maxListItem {
					return "", errors.New("list item too long")
				}
				out = append(out, s)
			}
		}
		if len(out) == 0 || len(out) > maxListItems {
			return "", errors.New("list must have 1-12 items")
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	case "number":
		f, ok := v.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return "", errors.New("expected a number")
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return "", errors.New("expected a bool")
		}
		return strconv.FormatBool(b), nil
	}
	return "", errors.New("unknown type")
}

// decodeValue is the typed form of a stored value.
func decodeValue(expect, stored string) any {
	switch expect {
	case "list":
		var l []string
		if json.Unmarshal([]byte(stored), &l) == nil {
			return l
		}
	case "number":
		if f, err := strconv.ParseFloat(stored, 64); err == nil {
			return f
		}
	case "bool":
		if b, err := strconv.ParseBool(stored); err == nil {
			return b
		}
	}
	return stored
}

// coerce converts a text value to expect when that loses nothing: a number or
// bool spelled exactly, or text as a one-item list. It returns the encoded value.
func coerce(text, expect string) (string, bool) {
	text = strings.TrimSpace(text)
	switch expect {
	case "text":
		return text, text != ""
	case "number":
		f, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || strconv.FormatFloat(f, 'f', -1, 64) != text {
			return "", false
		}
		return text, true
	case "bool":
		if text == "true" || text == "false" {
			return text, true
		}
	case "list":
		if text != "" && utf8.RuneCountInString(text) <= maxListItem && !strings.ContainsAny(text, "\n,;") {
			b, _ := json.Marshal([]string{text})
			return string(b), true
		}
	}
	return "", false
}

// parseEdit parses a value typed in the admin: text as is; a list one item per
// line (or comma-separated); a number or bool as spelled.
func parseEdit(expect, in string) (string, error) {
	in = strings.TrimSpace(in)
	switch expect {
	case "list":
		sep := "\n"
		if !strings.Contains(in, "\n") {
			sep = ","
		}
		var items []any
		for _, s := range strings.Split(in, sep) {
			items = append(items, s)
		}
		return encodeValue("list", items)
	case "number":
		f, err := strconv.ParseFloat(in, 64)
		if err != nil {
			return "", errors.New("not a number")
		}
		return encodeValue("number", f)
	case "bool":
		b, err := strconv.ParseBool(in)
		if err != nil {
			return "", errors.New("not true or false")
		}
		return encodeValue("bool", b)
	}
	if in == "" {
		return "", errors.New("empty value")
	}
	return in, nil
}

// display shortens a value for an admin summary.
func display(v string) string {
	if utf8.RuneCountInString(v) > 120 {
		return string([]rune(v)[:117]) + "…"
	}
	return v
}

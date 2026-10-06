package contract

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// Version is the contract's major version (contractVersion in the payload and
// in docs/content-contract.json). Bump it only for a breaking change, together
// with a new docs/content-contract.v<N>.snapshot.json.
const Version = 1

// Collection names.
const (
	Pages       = "pages"
	Experiments = "experiments"
	Projects    = "projects"   // since v1.4
	Experience  = "experience" // since v1.8
)

// GeneratedKey is the per-item field listing which fields hold
// observer-generated values.
const GeneratedKey = "_generated"

// Declared lists each collection's text fields, in order. Every declared
// field is a string and is always present in the payload ("" by default). The
// first field is the item's key (its slug).
var Declared = map[string][]string{
	Pages:       {"slug", "title", "body"},
	Experiments: {"slug", "title", "summary", "link"},
	Projects:    {"slug", "title", "client", "agency", "year", "summary", "contribution", "body", "link", "youtube"},
	Experience:  {"slug", "role", "company", "start", "end", "note"},
}

// Lists are each collection's list-of-strings fields, always present ([] by
// default; blank entries dropped).
var Lists = map[string][]string{
	Projects: {"tags", "roles", "palette"},
}

// Bools are each collection's boolean fields, always present (false by
// default). Never generated.
var Bools = map[string][]string{
	Experience: {"current"},
}

// MediaFields are each collection's lists of media items (MediaItem), always
// present ([] by default).
var MediaFields = map[string][]string{
	Experiments: {"media"},
	Projects:    {"media"},
}

// factual fields are never filled with generated values: an invented link,
// client, year, video or account of Ben's work would be a false statement,
// not a gap filled.
var factual = map[string]map[string]bool{
	Experiments: {"link": true},
	Projects: {"client": true, "agency": true, "year": true, "contribution": true, "body": true,
		"link": true, "youtube": true},
	// all of it: a role, a company, a date or a line about a job is a fact
	Experience: {"role": true, "company": true, "start": true, "end": true, "current": true, "note": true},
}

// Factual reports whether name, in collection, is a field generated values
// may never fill (a link, a date, a client, a list, media...). The observer
// asks for review instead of generating one.
func Factual(collection, name string) bool {
	return factual[collection][name] || slices.Contains(Lists[collection], name) || slices.Contains(Bools[collection], name) ||
		slices.Contains(MediaFields[collection], name)
}

// Item is one content item as front ends see it: the declared fields, the
// _generated list, and any extra (generated) fields as top-level keys.
type Item map[string]any

// MediaItem is one still or loop in a media list. Every key is always
// present; src and poster are paths on the site's origins ("/media/...").
type MediaItem struct {
	Kind   string `json:"kind"` // "image" or "loop"
	Src    string `json:"src"`
	Poster string `json:"poster"` // "" for images
	Width  int    `json:"width"`  // 0 if unknown
	Height int    `json:"height"`
	Alt    string `json:"alt"`
}

// Site is the contract payload served at /api/site.json.
type Site struct {
	ContractVersion int    `json:"contractVersion"`
	Pages           []Item `json:"pages"`
	Experiments     []Item `json:"experiments"`
	Projects        []Item `json:"projects"`
	Experience      []Item `json:"experience"` // since v1.8
	// Copy is the site-wide editable copy (since v1.10): tagline, concept,
	// experimentsEmpty, each defaulted. Front ends read it with site.text.
	Copy map[string]string `json:"copy"`

	// GeneratedErr is the error from Generated, if any. Build still succeeds
	// (without generated values); it's exposed for callers and tests.
	GeneratedErr error `json:"-"`
}

// Build produces the contract payload from the store's published content,
// with gen's values merged beneath the human values:
//
//   - a declared field keeps its human value unless that is empty (or only
//     whitespace); then a non-empty generated value fills it, unless the
//     field is Factual;
//   - a generated field the item doesn't declare is added as a top-level key;
//   - every merged field name is listed, sorted, in the item's _generated;
//   - the slug, _generated and other "_"-prefixed names are never generated.
//
// Pages are ordered by slug (home first), experiments and projects by their
// order, then slug. Projects come from the store's Projects, if it has them.
// Links that aren't http(s), YouTube ids that aren't ids and media that isn't
// under /media/ are served as "" or left out. gen may be nil. A gen error (or
// panic) is logged and the payload is built without generated values; store
// errors are returned.
func Build(ctx context.Context, st store.Store, gen Generated) (Site, error) {
	site := Site{ContractVersion: Version, Pages: []Item{}, Experiments: []Item{}, Projects: []Item{}, Experience: []Item{}}
	var stored map[string]string
	if cs := store.CopyOf(st); cs != nil {
		var err error
		if stored, err = cs.SiteCopy(ctx); err != nil {
			return Site{}, fmt.Errorf("copy: %w", err)
		}
	}
	site.Copy = content.ResolveSiteCopy(stored)

	pages, err := st.Pages(ctx)
	if err != nil {
		return Site{}, fmt.Errorf("pages: %w", err)
	}
	exps, err := st.Experiments(ctx)
	if err != nil {
		return Site{}, fmt.Errorf("experiments: %w", err)
	}
	var projects []content.Project
	if ps := store.ProjectsOf(st); ps != nil {
		if projects, err = ps.Projects(ctx); err != nil {
			return Site{}, fmt.Errorf("projects: %w", err)
		}
	}

	var experience []content.Experience
	if es := store.ExperienceOf(st); es != nil {
		if experience, err = es.Experience(ctx); err != nil {
			return Site{}, fmt.Errorf("experience: %w", err)
		}
	}

	generated, genErr := fetchGenerated(ctx, gen)
	if genErr != nil {
		site.GeneratedErr = genErr
		slog.Default().WarnContext(ctx, "content contract: generated fields unavailable; serving human content only", "err", genErr)
		generated = nil
	}

	// don't reorder the store's slices
	pages, exps, projects = slices.Clone(pages), slices.Clone(exps), slices.Clone(projects)
	slices.SortStableFunc(pages, func(a, b content.Page) int { return strings.Compare(a.Slug, b.Slug) })
	for _, p := range pages {
		if !p.Published {
			continue
		}
		site.Pages = append(site.Pages, merge(Pages, []string{p.Slug, p.Title, p.Body}, generated))
	}
	slices.SortStableFunc(exps, func(a, b content.Experiment) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Slug, b.Slug))
	})
	for _, e := range exps {
		if !e.Published {
			continue
		}
		it := merge(Experiments, []string{e.Slug, e.Title, e.Summary, link(e.Link)}, generated)
		it["media"] = Media(e.Media)
		site.Experiments = append(site.Experiments, it)
	}
	slices.SortStableFunc(projects, func(a, b content.Project) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Slug, b.Slug))
	})
	for _, p := range projects {
		if !p.Published {
			continue
		}
		yt := ""
		if content.ValidYouTube(p.YouTube) {
			yt = p.YouTube
		}
		it := merge(Projects, []string{p.Slug, p.Title, p.Client, p.Agency, p.Year, p.Summary,
			p.Contribution, p.Body, link(p.Link), yt}, generated)
		it["tags"] = texts(p.Tags)
		it["roles"] = texts(p.Roles)
		palette := []string{}
		for _, c := range p.Palette {
			if content.ValidHex(c) {
				palette = append(palette, strings.ToLower(c))
			}
		}
		it["palette"] = palette
		it["media"] = Media(p.Media)
		site.Projects = append(site.Projects, it)
	}
	experience = slices.Clone(experience)
	slices.SortStableFunc(experience, func(a, b content.Experience) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Slug, b.Slug))
	})
	for _, e := range experience {
		if !e.Published {
			continue
		}
		start, end := month(e.Start), month(e.End)
		if e.Current {
			end = ""
		}
		it := merge(Experience, []string{e.Slug, e.Role, e.Company, start, end, e.Note}, generated)
		it["current"] = e.Current
		site.Experience = append(site.Experience, it)
	}
	return site, nil
}

// month is s if it's "YYYY-MM" or "YYYY", else "".
func month(s string) string {
	if s = strings.TrimSpace(s); content.ValidMonth(s) {
		return s
	}
	return ""
}

// link is u if it's an http(s) URL, else "".
func link(u string) string {
	if u = strings.TrimSpace(u); content.ValidLink(u) {
		return u
	}
	return ""
}

// texts trims a list and drops blank entries; never nil.
func texts(l []string) []string {
	out := []string{}
	for _, s := range l {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Media is a media list as the contract serves it: valid items only (a known
// kind, /media/ paths), every key present; never nil.
func Media(l []content.Media) []MediaItem {
	out := []MediaItem{}
	for _, m := range l {
		if !m.Valid() {
			continue
		}
		poster := m.Poster
		if m.Kind != content.MediaLoop {
			poster = ""
		}
		out = append(out, MediaItem{Kind: m.Kind, Src: m.Src, Poster: poster, Width: m.Width, Height: m.Height, Alt: strings.TrimSpace(m.Alt)})
	}
	return out
}

// fetchGenerated calls gen, turning a panic (e.g. a typed-nil implementation)
// into an error: generated content must never take the contract down.
func fetchGenerated(ctx context.Context, gen Generated) (out map[Key]map[string]string, err error) {
	if gen == nil {
		return nil, nil
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("generated fields: panic: %v", r)
		}
	}()
	return gen.GeneratedFields(ctx)
}

// merge builds one item from its declared values (in Declared order) and the
// generated values for it.
func merge(collection string, values []string, generated map[Key]map[string]string) Item {
	fields := Declared[collection]
	it := make(Item, len(fields)+1)
	for i, f := range fields {
		it[f] = values[i]
	}
	names := []string{}
	for name, v := range generated[Key{Collection: collection, Item: values[0]}] {
		if !generatable(collection, name) || strings.TrimSpace(v) == "" {
			continue
		}
		if human, declared := it[name].(string); declared && strings.TrimSpace(human) != "" {
			continue // human values always win
		}
		it[name] = v
		names = append(names, name)
	}
	slices.Sort(names)
	it[GeneratedKey] = names
	return it
}

// generatable reports whether a generated value may be merged into field name:
// not the key field, not a reserved "_" name, and not a Factual field.
func generatable(collection, name string) bool {
	if name == "" || strings.HasPrefix(name, "_") || Factual(collection, name) {
		return false
	}
	return name != Declared[collection][0]
}

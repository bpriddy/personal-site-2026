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
)

// GeneratedKey is the per-item field listing which fields hold
// observer-generated values.
const GeneratedKey = "_generated"

// Declared lists each collection's declared fields, in order. Every declared
// field is a string and is always present in the payload ("" by default). The
// first field is the item's key (its slug).
var Declared = map[string][]string{
	Pages:       {"slug", "title", "body"},
	Experiments: {"slug", "title", "summary"},
}

// Item is one content item as front ends see it: the declared fields, the
// _generated list, and any extra (generated) fields as top-level keys.
type Item map[string]any

// Site is the contract payload served at /api/site.json.
type Site struct {
	ContractVersion int    `json:"contractVersion"`
	Pages           []Item `json:"pages"`
	Experiments     []Item `json:"experiments"`

	// GeneratedErr is the error from Generated, if any. Build still succeeds
	// (without generated values); it's exposed for callers and tests.
	GeneratedErr error `json:"-"`
}

// Build produces the contract payload from the store's published content,
// with gen's values merged beneath the human values:
//
//   - a declared field keeps its human value unless that is empty (or only
//     whitespace); then a non-empty generated value fills it;
//   - a generated field the item doesn't declare is added as a top-level key;
//   - every merged field name is listed, sorted, in the item's _generated;
//   - the slug, _generated and other "_"-prefixed names are never generated.
//
// Pages are ordered by slug (home first), experiments by their order, then
// slug. gen may be nil. A gen error (or panic) is logged and the payload is
// built without generated values; store errors are returned.
func Build(ctx context.Context, st store.Store, gen Generated) (Site, error) {
	site := Site{ContractVersion: Version, Pages: []Item{}, Experiments: []Item{}}

	pages, err := st.Pages(ctx)
	if err != nil {
		return Site{}, fmt.Errorf("pages: %w", err)
	}
	exps, err := st.Experiments(ctx)
	if err != nil {
		return Site{}, fmt.Errorf("experiments: %w", err)
	}

	generated, genErr := fetchGenerated(ctx, gen)
	if genErr != nil {
		site.GeneratedErr = genErr
		slog.Default().WarnContext(ctx, "content contract: generated fields unavailable; serving human content only", "err", genErr)
		generated = nil
	}

	pages, exps = slices.Clone(pages), slices.Clone(exps) // don't reorder the store's slices
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
		site.Experiments = append(site.Experiments, merge(Experiments, []string{e.Slug, e.Title, e.Summary}, generated))
	}
	return site, nil
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
// not the key field, and not a reserved "_" name.
func generatable(collection, name string) bool {
	if name == "" || strings.HasPrefix(name, "_") {
		return false
	}
	return name != Declared[collection][0]
}

package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

type genFunc func(context.Context) (map[Key]map[string]string, error)

func (f genFunc) GeneratedFields(ctx context.Context) (map[Key]map[string]string, error) {
	return f(ctx)
}

func genValues(m map[Key]map[string]string) Generated {
	return genFunc(func(context.Context) (map[Key]map[string]string, error) { return m, nil })
}

// testStore: the memory store's seed (home page + particle-stream) plus
// drafts, an empty page and extra experiments, saved in scrambled order.
func testStore(t *testing.T) *store.Memory {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	for _, p := range []content.Page{
		{Slug: "zeta", Title: "Zeta", Body: "z", Published: true},
		{Slug: "draft", Title: "Draft", Body: "secret", Published: false},
		{Slug: "about", Title: "", Body: "", Published: true},
	} {
		if err := st.SavePage(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []content.Experiment{
		{Slug: "b-exp", Title: "B", Summary: "b", Published: true, Order: 1},
		{Slug: "a-exp", Title: "A", Summary: "", Published: true, Order: 1},
		{Slug: "hidden", Title: "Hidden", Published: false, Order: -5},
		{Slug: "first", Title: "First", Summary: "f", Published: true, Order: -1},
	} {
		if err := st.SaveExperiment(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func slugs(items []Item) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it["slug"].(string))
	}
	return out
}

func find(t *testing.T, items []Item, slug string) Item {
	t.Helper()
	for _, it := range items {
		if it["slug"] == slug {
			return it
		}
	}
	t.Fatalf("no item %q in %v", slug, slugs(items))
	return nil
}

func TestBuildShapeAndOrder(t *testing.T) {
	site, err := Build(context.Background(), testStore(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if site.ContractVersion != Version || site.GeneratedErr != nil {
		t.Errorf("version %d, generated err %v", site.ContractVersion, site.GeneratedErr)
	}
	if got, want := slugs(site.Pages), []string{"", "about", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pages %q, want %q (published only, by slug)", got, want)
	}
	if got, want := slugs(site.Experiments), []string{"first", "particle-stream", "a-exp", "b-exp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("experiments %q, want %q (published only, by order then slug)", got, want)
	}
	// every declared field present and a string, _generated an empty list
	for coll, items := range map[string][]Item{Pages: site.Pages, Experiments: site.Experiments} {
		for _, it := range items {
			if len(it) != len(Declared[coll])+1 {
				t.Errorf("%s/%v: unexpected keys %v", coll, it["slug"], it)
			}
			for _, f := range Declared[coll] {
				if _, ok := it[f].(string); !ok {
					t.Errorf("%s/%v.%s = %#v, want a string", coll, it["slug"], f, it[f])
				}
			}
			if g, ok := it[GeneratedKey].([]string); !ok || len(g) != 0 {
				t.Errorf("%s/%v._generated = %#v", coll, it["slug"], it[GeneratedKey])
			}
		}
	}

	// JSON: no nulls anywhere, the documented top-level keys
	b, err := json.Marshal(site)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("null")) {
		t.Errorf("payload has null: %s", b)
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(b, &top)
	if len(top) != 3 || top["contractVersion"] == nil || top["pages"] == nil || top["experiments"] == nil {
		t.Errorf("top-level keys: %s", b)
	}
	about := string(mustJSON(t, find(t, site.Pages, "about")))
	if about != `{"_generated":[],"body":"","slug":"about","title":""}` {
		t.Errorf("empty page JSON: %s", about)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBuildEmptyCollections(t *testing.T) {
	st := &emptyStore{Store: store.NewMemory()}
	site, err := Build(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(mustJSON(t, site)); got != `{"contractVersion":1,"pages":[],"experiments":[]}` {
		t.Errorf("empty site: %s", got)
	}
}

// emptyStore has no content at all (nil slices, as a driver may return).
type emptyStore struct{ store.Store }

func (emptyStore) Pages(context.Context) ([]content.Page, error)             { return nil, nil }
func (emptyStore) Experiments(context.Context) ([]content.Experiment, error) { return nil, nil }

type failingStore struct {
	store.Store
	pagesErr, expsErr error
}

func (f failingStore) Pages(ctx context.Context) ([]content.Page, error) {
	if f.pagesErr != nil {
		return nil, f.pagesErr
	}
	return f.Store.Pages(ctx)
}

func (f failingStore) Experiments(ctx context.Context) ([]content.Experiment, error) {
	if f.expsErr != nil {
		return nil, f.expsErr
	}
	return f.Store.Experiments(ctx)
}

func TestBuildStoreErrors(t *testing.T) {
	boom := errors.New("boom")
	for _, st := range []failingStore{
		{Store: store.NewMemory(), pagesErr: boom},
		{Store: store.NewMemory(), expsErr: boom},
	} {
		if _, err := Build(context.Background(), st, nil); !errors.Is(err, boom) {
			t.Errorf("err = %v, want boom", err)
		}
	}
}

func TestBuildMergesGenerated(t *testing.T) {
	gen := genValues(map[Key]map[string]string{
		{Pages, "about"}: {
			"title":    "About (generated)", // human empty → generated fills
			"body":     "   ",               // generated blank → ignored
			"subtitle": "A subtitle",        // extra field
			"slug":     "hijack",            // the key is never generated
			"_meta":    "x",                 // reserved names are never generated
			"":         "x",
		},
		{Pages, "zeta"}: {
			"title": "Not Zeta", // human non-empty wins
			"body":  "also not",
			"tags":  "t",
		},
		{Pages, "draft"}:                 {"title": "unpublished items stay hidden"},
		{Pages, "nope"}:                  {"title": "no such item"},
		{Experiments, "a-exp"}:           {"summary": "Generated summary"},
		{Experiments, "about"}:           {"summary": "wrong collection"},
		{"projects", "about"}:            {"title": "unknown collection"},
		{Experiments, "particle-stream"}: nil,
	})
	st := testStore(t)
	// whitespace-only human value counts as empty
	if err := st.SavePage(context.Background(), content.Page{Slug: "", Title: " \n", Body: "Home", Published: true}); err != nil {
		t.Fatal(err)
	}
	gen2 := genFunc(func(ctx context.Context) (map[Key]map[string]string, error) {
		m, _ := gen.GeneratedFields(ctx)
		m[Key{Pages, ""}] = map[string]string{"title": "Home (generated)"}
		return m, nil
	})
	site, err := Build(context.Background(), st, gen2)
	if err != nil {
		t.Fatal(err)
	}

	about := find(t, site.Pages, "about")
	want := Item{"slug": "about", "title": "About (generated)", "body": "", "subtitle": "A subtitle", GeneratedKey: []string{"subtitle", "title"}}
	if !reflect.DeepEqual(about, want) {
		t.Errorf("about:\n got %#v\nwant %#v", about, want)
	}
	zeta := find(t, site.Pages, "zeta")
	want = Item{"slug": "zeta", "title": "Zeta", "body": "z", "tags": "t", GeneratedKey: []string{"tags"}}
	if !reflect.DeepEqual(zeta, want) {
		t.Errorf("zeta:\n got %#v\nwant %#v", zeta, want)
	}
	home := find(t, site.Pages, "")
	if home["title"] != "Home (generated)" || !reflect.DeepEqual(home[GeneratedKey], []string{"title"}) {
		t.Errorf("home: %#v", home)
	}
	aexp := find(t, site.Experiments, "a-exp")
	if aexp["summary"] != "Generated summary" || !reflect.DeepEqual(aexp[GeneratedKey], []string{"summary"}) {
		t.Errorf("a-exp: %#v", aexp)
	}
	if got := slugs(site.Pages); !reflect.DeepEqual(got, []string{"", "about", "zeta"}) {
		t.Errorf("generated values changed the item set: %q", got)
	}
	for _, it := range site.Experiments {
		if it["slug"] != "a-exp" && len(it[GeneratedKey].([]string)) != 0 {
			t.Errorf("experiment %v got generated values: %#v", it["slug"], it)
		}
	}
}

// panicGen is a typed-nil implementation: calling it panics.
type panicGen struct{ m map[Key]map[string]string }

func (p *panicGen) GeneratedFields(context.Context) (map[Key]map[string]string, error) {
	return p.m, nil
}

func TestBuildGeneratedFailures(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	boom := errors.New("observer db down")
	partial := genFunc(func(context.Context) (map[Key]map[string]string, error) {
		// values alongside an error are not trusted
		return map[Key]map[string]string{{Pages, "about"}: {"title": "partial"}}, boom
	})
	var typedNil *panicGen
	for name, gen := range map[string]Generated{"error": partial, "panic": typedNil} {
		logs.Reset()
		site, err := Build(context.Background(), testStore(t), gen)
		if err != nil {
			t.Fatalf("%s: Build failed: %v", name, err)
		}
		if site.GeneratedErr == nil {
			t.Errorf("%s: GeneratedErr not set", name)
		}
		if !strings.Contains(logs.String(), "generated fields unavailable") {
			t.Errorf("%s: not logged: %q", name, logs.String())
		}
		if about := find(t, site.Pages, "about"); about["title"] != "" || len(about[GeneratedKey].([]string)) != 0 {
			t.Errorf("%s: generated values used despite error: %#v", name, about)
		}
		if len(site.Pages) != 3 || len(site.Experiments) != 4 {
			t.Errorf("%s: content missing: %d pages, %d experiments", name, len(site.Pages), len(site.Experiments))
		}
	}
}

func TestBuildDoesNotReorderStoreSlices(t *testing.T) {
	st := &fixedStore{Store: store.NewMemory(), pages: []content.Page{
		{Slug: "b", Published: true}, {Slug: "a", Published: true},
	}}
	if _, err := Build(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	if st.pages[0].Slug != "b" {
		t.Error("Build sorted the store's slice in place")
	}
}

type fixedStore struct {
	store.Store
	pages []content.Page
}

func (f *fixedStore) Pages(context.Context) ([]content.Page, error) { return f.pages, nil }

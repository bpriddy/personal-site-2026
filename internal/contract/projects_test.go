package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

func projectStore(t *testing.T) *store.Memory {
	t.Helper()
	ctx := context.Background()
	st := testStore(t)
	for _, p := range []content.Project{
		{
			Slug: "qledecode", Title: "QLEDecode", Client: "Samsung", Agency: "Adam & Eve", Year: "2020",
			Tags: []string{"ARG", " ", " Installation "}, Roles: []string{"Creative Director"},
			Summary: "s", Contribution: "c", Body: "p1\n\np2", Link: "https://example.com/x",
			Palette: []string{"#001D48", "red", "#12345"}, YouTube: "e6PKBbvRYV0",
			Media: []content.Media{
				{Kind: "image", Src: "/media/projects/qledecode/hero.jpg", Poster: "/media/x.jpg", Width: 735, Height: 413},
				{Kind: "loop", Src: "/media/projects/qledecode/loop-1.mp4", Poster: "/media/projects/qledecode/loop-1.jpg", Width: 600, Height: 200, Alt: " A loop "},
				{Kind: "loop", Src: "https://evil.example/x.mp4"},            // hotlink: dropped
				{Kind: "image", Src: "/media/../etc/passwd.jpg"},             // traversal: dropped
				{Kind: "audio", Src: "/media/projects/qledecode/a.mp4"},      // unknown kind: dropped
				{Kind: "image", Src: "/media/projects/qledecode/hero.svg"},   // not a media type: dropped
				{Kind: "loop", Src: "/media/p/l.mp4", Poster: "javascript:"}, // bad poster: dropped
			},
			Order: 1, Published: true,
		},
		{Slug: "empty", Order: 1, Published: true, Link: "javascript:alert(1)", YouTube: "not-an-id"},
		{Slug: "first", Title: "First", Order: 0, Published: true},
		{Slug: "draft", Title: "Draft", Order: -10, Published: false},
	} {
		if err := st.SaveProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestBuildProjects(t *testing.T) {
	site, err := Build(context.Background(), projectStore(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := slugs(site.Projects), []string{"first", "empty", "qledecode"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("projects %q, want %q (published only, by order then slug)", got, want)
	}
	q := find(t, site.Projects, "qledecode")
	want := Item{
		"slug": "qledecode", "title": "QLEDecode", "client": "Samsung", "agency": "Adam & Eve", "year": "2020",
		"tags": []string{"ARG", "Installation"}, "roles": []string{"Creative Director"},
		"summary": "s", "contribution": "c", "body": "p1\n\np2", "link": "https://example.com/x",
		"palette": []string{"#001d48"}, "youtube": "e6PKBbvRYV0",
		"media": []MediaItem{
			{Kind: "image", Src: "/media/projects/qledecode/hero.jpg", Width: 735, Height: 413},
			{Kind: "loop", Src: "/media/projects/qledecode/loop-1.mp4", Poster: "/media/projects/qledecode/loop-1.jpg", Width: 600, Height: 200, Alt: "A loop"},
		},
		GeneratedKey: []string{},
	}
	if !reflect.DeepEqual(q, want) {
		t.Errorf("qledecode:\n got %#v\nwant %#v", q, want)
	}

	// defaults: every field present, "" / []; unsafe links and ids blanked
	b := mustJSON(t, find(t, site.Projects, "empty"))
	const wantEmpty = `{"_generated":[],"agency":"","body":"","client":"","contribution":"","link":"","media":[],"palette":[],"roles":[],"slug":"empty","summary":"","tags":[],"title":"","year":"","youtube":""}`
	if string(b) != wantEmpty {
		t.Errorf("empty project JSON:\n got %s\nwant %s", b, wantEmpty)
	}
	if all := mustJSON(t, site); bytes.Contains(all, []byte("null")) || bytes.Contains(all, []byte("draft")) {
		t.Errorf("payload has null or the draft: %s", all)
	}

	// media items always carry every key
	var round struct {
		Projects []struct {
			Media []map[string]any `json:"media"`
		} `json:"projects"`
	}
	_ = json.Unmarshal(mustJSON(t, site), &round)
	for _, m := range round.Projects[2].Media {
		if len(m) != 6 {
			t.Errorf("media item keys: %v", m)
		}
	}
}

func TestBuildExperimentLinkAndMedia(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	e, _ := st.Experiment(ctx, "a-exp")
	e.Link = "https://codepen.io/colorandsound/full/eJGKxG"
	e.Media = []content.Media{{Kind: "loop", Src: "/media/projects/300-cubes/loop-1.mp4", Poster: "/media/projects/300-cubes/loop-1.jpg", Width: 800, Height: 100}}
	if err := st.SaveExperiment(ctx, e); err != nil {
		t.Fatal(err)
	}
	site, err := Build(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := find(t, site.Experiments, "a-exp")
	if a["link"] != e.Link || !reflect.DeepEqual(a["media"], []MediaItem{{Kind: "loop", Src: e.Media[0].Src, Poster: e.Media[0].Poster, Width: 800, Height: 100}}) {
		t.Errorf("a-exp: %#v", a)
	}
	if got := string(mustJSON(t, find(t, site.Experiments, "b-exp"))); got != `{"_generated":[],"link":"","media":[],"slug":"b-exp","summary":"b","title":"B"}` {
		t.Errorf("b-exp JSON: %s", got)
	}
}

func TestBuildProjectsGenerated(t *testing.T) {
	gen := genValues(map[Key]map[string]string{
		{Projects, "empty"}: {
			"title":    "Generated title", // fills an empty title
			"summary":  "Generated summary",
			"tagline":  "An extra field",
			"client":   "Invented client", // factual: never
			"year":     "1999",
			"link":     "https://invented.example",
			"youtube":  "dQw4w9WgXcQ",
			"body":     "Invented body",
			"tags":     "a,b", // a list field: never overwritten by text
			"media":    "x",
			"palette":  "#000000",
			"_private": "x",
		},
		{Experiments, "a-exp"}: {"link": "https://invented.example", "summary": "ok"},
		{Projects, "draft"}:    {"title": "drafts stay hidden"},
	})
	site, err := Build(context.Background(), projectStore(t), gen)
	if err != nil {
		t.Fatal(err)
	}
	e := find(t, site.Projects, "empty")
	if e["title"] != "Generated title" || e["summary"] != "Generated summary" || e["tagline"] != "An extra field" {
		t.Errorf("generatable fields not merged: %#v", e)
	}
	for _, f := range []string{"client", "year", "link", "youtube", "body"} {
		if e[f] != "" {
			t.Errorf("factual %s was generated: %q", f, e[f])
		}
	}
	if !reflect.DeepEqual(e["tags"], []string{}) || !reflect.DeepEqual(e["media"], []MediaItem{}) || !reflect.DeepEqual(e["palette"], []string{}) {
		t.Errorf("typed fields overwritten: %#v", e)
	}
	if !reflect.DeepEqual(e[GeneratedKey], []string{"summary", "tagline", "title"}) {
		t.Errorf("_generated = %v", e[GeneratedKey])
	}
	a := find(t, site.Experiments, "a-exp")
	if a["link"] != "" || a["summary"] != "ok" {
		t.Errorf("experiment link was generated: %#v", a)
	}
	for _, f := range []string{"client", "agency", "year", "link", "youtube", "body", "contribution", "tags", "roles", "palette", "media"} {
		if !Factual(Projects, f) {
			t.Errorf("Factual(projects, %s) = false", f)
		}
	}
	if Factual(Projects, "title") || Factual(Projects, "summary") || Factual(Pages, "body") {
		t.Error("Factual too broad")
	}
}

// a store without Projects (an old implementation) serves none
func TestBuildWithoutProjectsStore(t *testing.T) {
	st := &fixedStore{Store: projectStore(t), pages: []content.Page{{Slug: "", Published: true}}}
	site, err := Build(context.Background(), st, nil)
	if err != nil || len(site.Projects) != 0 || site.Projects == nil {
		t.Fatalf("projects = %#v, %v", site.Projects, err)
	}
}

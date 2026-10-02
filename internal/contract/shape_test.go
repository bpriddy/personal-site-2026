package contract

import (
	"strings"
	"testing"
)

func TestShapeOf(t *testing.T) {
	site := Site{
		Pages: []Item{
			{"slug": "", "title": "Ben", "body": "Hi", GeneratedKey: []string{}},
			{"slug": "about", "title": "About", "body": "", "subtitle": "gen", GeneratedKey: []string{"subtitle"}},
		},
		Experiments: []Item{},
		Projects: []Item{
			{"slug": "a", "title": "A", "client": "", "tags": []string{}, "palette": []string{"#000000"},
				"media": []MediaItem{{Kind: "image", Src: "/media/a.jpg", Width: 10}}, "_collection": "projects"},
			{"slug": "b", "title": "B", "client": "Nike", "tags": []string{"x"}, "palette": []string{},
				"media": []MediaItem{}, "year": "  "},
		},
	}
	s := ShapeOf(site)
	if len(s.Collections) != 2 || s.Collections[0].Name != Pages || s.Collections[1].Name != Projects {
		t.Fatalf("collections = %+v", s.Collections)
	}
	if _, ok := s.Collection(Experiments); ok {
		t.Error("an empty collection is part of the shape")
	}
	pages, _ := s.Collection(Pages)
	if pages.Items != 2 || len(pages.Fields) != 3 {
		t.Fatalf("pages = %+v", pages)
	}
	if f, _ := pages.Field("body"); f.NonEmpty != 1 {
		t.Errorf("body = %+v", f)
	}
	if f, ok := pages.Field("subtitle"); !ok || f.NonEmpty != 1 {
		t.Errorf("generated extra field = %+v %v", f, ok)
	}
	projects, _ := s.Collection(Projects)
	var names []string
	for _, f := range projects.Fields {
		names = append(names, f.Name)
	}
	// slug, _-keys and palette (ignored) are out; blank year has no value
	if got := strings.Join(names, ","); got != "client,media,tags,title" {
		t.Fatalf("project fields = %s", got)
	}

	// the fingerprint follows names, not counts
	fp := s.Fingerprint()
	site.Projects[0]["client"] = "Adidas"
	if ShapeOf(site).Fingerprint() != fp {
		t.Error("fingerprint changed with a count")
	}
	site.Projects[0]["year"] = "2020"
	if ShapeOf(site).Fingerprint() == fp {
		t.Error("fingerprint unchanged by a new field")
	}
	site.Experiments = []Item{{"slug": "x", "title": "X"}}
	if ShapeOf(site).Fingerprint() == fp {
		t.Error("fingerprint unchanged by a new collection")
	}
	if ShapeOf(site).Fingerprint() != ShapeOf(site).Fingerprint() {
		t.Error("fingerprint not stable")
	}
}

func TestNonEmpty(t *testing.T) {
	for _, v := range []any{"x", []string{"a"}, []MediaItem{{}}, 3, 1.5, true, map[string]any{"a": 1}} {
		if !NonEmpty(v) {
			t.Errorf("NonEmpty(%#v) = false", v)
		}
	}
	for _, v := range []any{nil, "", " \n", []string{}, []MediaItem{}, 0, 0.0, false, map[string]any{}} {
		if NonEmpty(v) {
			t.Errorf("NonEmpty(%#v) = true", v)
		}
	}
}

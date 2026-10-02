package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

// projectsConformance runs the Projects behavior every store must share
// (migration 0005), plus experiments' link and media.
func projectsConformance(t *testing.T, newStore func(t *testing.T) Store) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	hero := content.Media{Kind: content.MediaImage, Src: "/media/projects/a/hero.jpg", Width: 735, Height: 413}
	loop := content.Media{Kind: content.MediaLoop, Src: "/media/projects/a/loop-1.mp4", Poster: "/media/projects/a/loop-1.jpg", Width: 600, Height: 200, Alt: "a loop"}

	t.Run("projects", func(t *testing.T) {
		st := newStore(t)
		ps := ProjectsOf(st)
		if ps == nil {
			t.Fatal("store doesn't implement Projects")
		}
		if got, err := ps.Projects(ctx); err != nil || len(got) != 0 {
			t.Fatalf("seed projects = %+v, %v; want none", got, err)
		}
		if _, err := ps.Project(ctx, "nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing project err = %v", err)
		}
		full := content.Project{
			Slug: "a", Title: "A", Client: "Samsung", Agency: "Adam & Eve", Year: "2020",
			Tags: []string{"ARG", "Installation"}, Roles: []string{"Creative Director"},
			Summary: "s", Contribution: "c", Body: "p1\n\np2", Link: "https://example.com",
			Palette: []string{"#001d48"}, YouTube: "e6PKBbvRYV0", Media: []content.Media{hero, loop},
			Order: 2, Published: true,
		}
		must(t, ps.SaveProject(ctx, full))
		must(t, ps.SaveProject(ctx, content.Project{Slug: "b", Title: "B", Order: 2}))       // nil lists
		must(t, ps.SaveProject(ctx, content.Project{Slug: "z", Title: "Z", Order: -1}))      // first by order
		must(t, ps.SaveProject(ctx, content.Project{Slug: "d", Title: "Draft", Order: 100})) // unpublished

		got, err := ps.Project(ctx, "a")
		must(t, err)
		if got.UpdatedAt.IsZero() {
			t.Error("UpdatedAt not set")
		}
		got.UpdatedAt = time.Time{}
		if !reflect.DeepEqual(got, full) {
			t.Fatalf("round trip:\n got %+v\nwant %+v", got, full)
		}
		b, _ := ps.Project(ctx, "b")
		if b.Tags == nil || b.Roles == nil || b.Palette == nil || b.Media == nil || len(b.Tags)+len(b.Media) != 0 {
			t.Fatalf("empty lists must come back as [], got %+v", b)
		}

		all, err := ps.Projects(ctx)
		must(t, err)
		var slugs []string
		for _, p := range all {
			slugs = append(slugs, p.Slug)
		}
		if want := []string{"z", "a", "b", "d"}; !reflect.DeepEqual(slugs, want) {
			t.Fatalf("order = %v, want %v (order, then slug)", slugs, want)
		}

		// update in place; the returned slices are copies
		got.Tags[0] = "mutated"
		again, _ := ps.Project(ctx, "a")
		if again.Tags[0] != "ARG" {
			t.Fatal("caller mutation reached the store")
		}
		time.Sleep(5 * time.Millisecond)
		again.Published = false
		again.Media = nil
		again.Title = "A2"
		must(t, ps.SaveProject(ctx, again))
		upd, _ := ps.Project(ctx, "a")
		if upd.Title != "A2" || upd.Published || len(upd.Media) != 0 || !upd.UpdatedAt.After(again.UpdatedAt) {
			t.Fatalf("after update = %+v", upd)
		}
		if all, _ := ps.Projects(ctx); len(all) != 4 {
			t.Fatalf("update duplicated a row: %d projects", len(all))
		}
	})

	t.Run("experiment link and media", func(t *testing.T) {
		st := newStore(t)
		seed, err := st.Experiment(ctx, "particle-stream")
		must(t, err)
		if seed.Link != "" || seed.Media == nil || len(seed.Media) != 0 {
			t.Fatalf("seed experiment link/media = %q %#v; want \"\" and []", seed.Link, seed.Media)
		}
		seed.Link = "https://codepen.io/x"
		seed.Media = []content.Media{loop}
		must(t, st.SaveExperiment(ctx, seed))
		got, _ := st.Experiment(ctx, "particle-stream")
		if got.Link != seed.Link || !reflect.DeepEqual(got.Media, seed.Media) {
			t.Fatalf("experiment = %+v", got)
		}
		list, _ := st.Experiments(ctx)
		if len(list) != 1 || !reflect.DeepEqual(list[0].Media, seed.Media) {
			t.Fatalf("experiments = %+v", list)
		}
	})
}

func TestMemoryProjects(t *testing.T) {
	projectsConformance(t, func(*testing.T) Store { return NewMemory() })
}

func TestPostgresProjects(t *testing.T) {
	projectsConformance(t, func(t *testing.T) Store { return openPG(t, freshDB(t)) })
}

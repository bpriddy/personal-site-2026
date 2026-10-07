package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

// conformance runs the behavior every Store must share. newStore returns a
// fresh store holding only the seed data.
func conformance(t *testing.T, newStore func(t *testing.T) Store) {
	ctx := context.Background()

	t.Run("seed", func(t *testing.T) {
		st := newStore(t)
		home, err := st.Page(ctx, "")
		if err != nil || home.Title != "Ben Priddy" || !home.Published {
			t.Fatalf("home = %+v, %v", home, err)
		}
		exps, err := st.Experiments(ctx)
		if err != nil || len(exps) != 1 || exps[0].Slug != "particle-stream" {
			t.Fatalf("experiments = %+v, %v", exps, err)
		}
		fes, err := st.Frontends(ctx)
		if err != nil || len(fes) != 3 || fes[2].Ref != "builtin/stream" || fes[2].InRotation {
			t.Fatalf("frontends = %+v, %v", fes, err)
		}
		if fes[0].Ref != "builtin/particle-stream" || fes[1].Ref != "builtin/site" || !fes[0].InRotation || !fes[1].InRotation {
			t.Fatalf("frontends not seeded in ref order, in rotation: %+v", fes)
		}
	})

	t.Run("pages", func(t *testing.T) {
		st := newStore(t)
		if _, err := st.Page(ctx, "about"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing page err = %v, want ErrNotFound", err)
		}
		if err := st.SavePage(ctx, content.Page{Slug: "about", Title: "About", Body: "Hi", Published: true}); err != nil {
			t.Fatal(err)
		}
		first, err := st.Page(ctx, "about")
		if err != nil || first.Title != "About" || first.Body != "Hi" || !first.Published || first.UpdatedAt.IsZero() {
			t.Fatalf("after insert = %+v, %v", first, err)
		}
		time.Sleep(5 * time.Millisecond)
		if err := st.SavePage(ctx, content.Page{Slug: "about", Title: "About me", Body: "", Published: false}); err != nil {
			t.Fatal(err)
		}
		second, _ := st.Page(ctx, "about")
		if second.Title != "About me" || second.Body != "" || second.Published || !second.UpdatedAt.After(first.UpdatedAt) {
			t.Fatalf("after update = %+v (first %+v)", second, first)
		}
		pages, err := st.Pages(ctx)
		if err != nil || len(pages) != 2 || pages[0].Slug != "" || pages[1].Slug != "about" {
			t.Fatalf("pages = %+v, %v", pages, err)
		}
	})

	t.Run("experiments", func(t *testing.T) {
		st := newStore(t)
		if _, err := st.Experiment(ctx, "nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing experiment err = %v", err)
		}
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		must(st.SaveExperiment(ctx, content.Experiment{Slug: "b", Title: "B", Order: 1}))
		must(st.SaveExperiment(ctx, content.Experiment{Slug: "a", Title: "A", Order: 1, Summary: "s", Published: true}))
		must(st.SaveExperiment(ctx, content.Experiment{Slug: "z", Title: "Z", Order: -1}))
		exps, err := st.Experiments(ctx)
		must(err)
		var got []string
		for _, e := range exps {
			got = append(got, e.Slug)
		}
		want := []string{"z", "particle-stream", "a", "b"} // by order, then slug
		if len(got) != len(want) {
			t.Fatalf("order = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("order = %v, want %v", got, want)
			}
		}
		a, _ := st.Experiment(ctx, "a")
		if a.Summary != "s" || !a.Published || a.Order != 1 {
			t.Fatalf("a = %+v", a)
		}
	})

	t.Run("frontends", func(t *testing.T) {
		st := newStore(t)
		if err := st.SetFrontendInRotation(ctx, "builtin/particle-stream", false); err != nil {
			t.Fatal(err)
		}
		fes, _ := st.Frontends(ctx)
		if fes[0].InRotation || !fes[1].InRotation {
			t.Fatalf("after toggle off = %+v", fes)
		}
		if err := st.SetFrontendInRotation(ctx, "builtin/particle-stream", true); err != nil {
			t.Fatal(err)
		}
		fes, _ = st.Frontends(ctx)
		if !fes[0].InRotation {
			t.Fatalf("after toggle on = %+v", fes)
		}
		if err := st.SetFrontendInRotation(ctx, "builtin/nope", true); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown ref err = %v, want ErrNotFound", err)
		}
	})
}

func TestMemoryConformance(t *testing.T) {
	conformance(t, func(*testing.T) Store { return NewMemory() })
}

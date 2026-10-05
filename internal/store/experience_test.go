package store

import (
	"context"
	"errors"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

func experienceConformance(t *testing.T, newStore func(t *testing.T) Store) {
	ctx := context.Background()
	st := newStore(t)
	es := ExperienceOf(st)
	if es == nil {
		t.Fatal("store doesn't implement ExperienceStore")
	}
	if got, err := es.Experience(ctx); err != nil || len(got) != 0 {
		t.Fatalf("seed = %+v, %v; want none", got, err)
	}
	if _, err := es.ExperienceItem(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	now := content.Experience{Slug: "a-now", Role: "Global Head", Company: "Anomaly", Start: "2026-03", Current: true, Order: 1, Published: true}
	old := content.Experience{Slug: "b-old", Role: "Technical Director", Company: "Stink Studios", Start: "2020-02", End: "2022-04", Note: "one line", Order: 2}
	for _, e := range []content.Experience{old, now} {
		if err := es.SaveExperience(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := es.Experience(ctx)
	if err != nil || len(got) != 2 || got[0].Slug != "a-now" || got[1].Slug != "b-old" {
		t.Fatalf("list = %+v, %v (want by order)", got, err)
	}
	g := got[1]
	if g.Role != old.Role || g.Company != old.Company || g.Start != old.Start || g.End != old.End || g.Note != old.Note || g.Published || g.Current || g.UpdatedAt.IsZero() {
		t.Fatalf("round trip = %+v", g)
	}
	if !got[0].Current || !got[0].Published || got[0].End != "" {
		t.Fatalf("current role = %+v", got[0])
	}
	old.Role = "TD"
	if err := es.SaveExperience(ctx, old); err != nil {
		t.Fatal(err)
	}
	if e, _ := es.ExperienceItem(ctx, "b-old"); e.Role != "TD" {
		t.Fatalf("update = %+v", e)
	}
	if err := es.DeleteExperience(ctx, "b-old"); err != nil {
		t.Fatal(err)
	}
	if got, _ := es.Experience(ctx); len(got) != 1 {
		t.Fatalf("after delete: %+v", got)
	}
}

func TestMemoryExperience(t *testing.T) {
	experienceConformance(t, func(*testing.T) Store { return NewMemory() })
}

func TestPostgresExperience(t *testing.T) {
	experienceConformance(t, func(t *testing.T) Store { return openPG(t, freshDB(t)) })
}

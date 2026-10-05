package contract

import (
	"context"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

func TestBuildExperience(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	for _, e := range []content.Experience{
		{Slug: "b-tool", Role: "Creative Director of Technology", Company: "Tool of North America", Start: "2014-09", End: "2019-06", Order: 5, Published: true},
		{Slug: "a-anomaly", Role: "Global Head of AI Technology", Company: "Anomaly", Start: "2026-03", End: "2027-01", Current: true, Order: 1, Published: true},
		{Slug: "c-hidden", Role: "Draft", Company: "X", Order: 2},
		{Slug: "d-bad", Role: "Bad dates", Company: "Y", Start: "March 2020", End: "2021-13", Order: 3, Published: true},
	} {
		if err := st.SaveExperience(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	site, err := Build(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(site.Experience) != 3 {
		t.Fatalf("published experience = %v", site.Experience)
	}
	first, bad, last := site.Experience[0], site.Experience[1], site.Experience[2]
	if first["slug"] != "a-anomaly" || first["current"] != true || first["end"] != "" || first["start"] != "2026-03" {
		t.Errorf("current role: %v (a current role has no end)", first)
	}
	if bad["start"] != "" || bad["end"] != "" {
		t.Errorf("invalid months should be served as \"\": %v", bad)
	}
	if last["company"] != "Tool of North America" || last["current"] != false || last["note"] != "" {
		t.Errorf("past role: %v", last)
	}
	for _, f := range []string{"role", "company", "start", "end", "current", "note"} {
		if !Factual(Experience, f) {
			t.Errorf("experience.%s must be factual (never generated)", f)
		}
	}
}

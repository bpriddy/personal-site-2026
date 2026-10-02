package observer

import (
	"context"
	"strings"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/contract"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// Projects are observed like pages (v1.4), but facts (a client, a year, a
// link, a film, lists, media) are never generated: they go to review.
func TestProjectGaps(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	if err := f.content.SaveProject(ctx, content.Project{Slug: "qled", Title: "QLEDecode", Summary: longBody, Published: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.content.SaveProject(ctx, content.Project{Slug: "hidden", Title: "Hidden", Summary: longBody}); err != nil {
		t.Fatal(err)
	}
	projectGap := func(item, field, expect string) Report {
		r := gap(item, field)
		r.Collection, r.Expect, r.Route = "projects", expect, "work/"+item
		return r
	}

	// a generatable extra field is generated from the project's text
	d := f.process(t, projectGap("qled", "tagline", "text"))
	if d.Status != store.StatusFixed || f.model.n() != 1 || f.model.calls[0].Content["summary"] != longBody {
		t.Fatalf("tagline: %+v (calls %d)", d, f.model.n())
	}
	if f.generated(t)[contract.Key{Collection: "projects", Item: "qled"}]["tagline"] == "" {
		t.Error("tagline not served")
	}
	// facts aren't
	for _, field := range []string{"client", "year", "link", "youtube", "agency", "body", "contribution"} {
		d := f.process(t, projectGap("qled", field, "text"))
		if d.Status != store.StatusNeedsReview || !strings.Contains(d.Action.Summary, "it's a fact") {
			t.Errorf("%s: %+v", field, d)
		}
	}
	if d := f.process(t, projectGap("qled", "tags", "list")); d.Status != store.StatusNeedsReview {
		t.Errorf("tags: %+v", d)
	}
	e := gap("particle-stream", "link")
	e.Collection = "experiments"
	if d := f.process(t, e); d.Status != store.StatusNeedsReview {
		t.Errorf("experiment link: %+v", d)
	}
	if f.model.n() != 1 {
		t.Errorf("model called %d times; facts must never reach it", f.model.n())
	}
	// unpublished projects are dropped like other items
	if d, err := f.o.Process(ctx, projectGap("hidden", "tagline", "text")); err == nil && d.ID != 0 {
		t.Errorf("unpublished project recorded: %+v", d)
	}
}

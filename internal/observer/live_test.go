package observer

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/llm"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// TestLiveGeneration calls the real Claude API: it generates a subtitle for
// the seeded particle-stream experiment and for a sample page. It is skipped
// unless OBSERVER_LIVE=1 and ANTHROPIC_API_KEY are set:
//
//	OBSERVER_LIVE=1 go test ./internal/observer -run TestLiveGeneration -v
func TestLiveGeneration(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if os.Getenv("OBSERVER_LIVE") != "1" || key == "" {
		t.Skip("set OBSERVER_LIVE=1 and ANTHROPIC_API_KEY to call the Claude API")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cs := store.NewMemory()
	cs.SavePage(ctx, content.Page{Slug: "about", Title: "About", Published: true,
		Body: "I'm Ben Priddy, a creative technologist in Portland. I build interactive pieces with WebGPU and Rust, " +
			"and I've spent the last decade making tools that help designers prototype in code.\n\n" +
			"This site is where I put experiments, notes on what I learned, and the occasional essay."})
	o := New(Config{Content: cs, Obs: store.NewObserverMemory(), Model: &Claude{Client: llm.NewClient(key), Model: llm.Model},
		ModelName: llm.Model})
	for _, r := range []Report{
		{Kind: "content-gap", Frontend: "builtin/site", Collection: "experiments", Item: "particle-stream", Field: "subtitle", Expect: "text", Got: "missing"},
		{Kind: "content-gap", Frontend: "builtin/site", Collection: "pages", Item: "about", Field: "subtitle", Expect: "text", Got: "missing"},
	} {
		if err := r.normalize(); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		d, err := o.Process(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		g, gerr := o.obs.GeneratedField(ctx, r.Collection, r.Item, r.Field)
		t.Logf("%s/%s.%s → status %s in %s\n  action: %s\n  stored: %q (model %s, err %v)",
			r.Collection, r.Item, r.Field, d.Status, time.Since(start).Round(time.Millisecond), d.Action.Summary, g.Value, g.Model, gerr)
		if d.Status != store.StatusFixed || g.Value == "" {
			t.Errorf("not generated: %+v", d)
		}
	}
}

package observer

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/contract"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// fakeModel answers from a function and counts calls.
type fakeModel struct {
	mu    sync.Mutex
	calls []GenerateRequest
	fn    func(GenerateRequest) (Generation, error)
}

func (f *fakeModel) Generate(_ context.Context, req GenerateRequest) (Generation, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	n := len(f.calls)
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(req)
	}
	var v any = "Generated " + req.Field + " " + string(rune('0'+n))
	switch req.Expect {
	case "list":
		v = []any{"a", "b"}
	case "number":
		v = 3.0
	case "bool":
		v = true
	}
	return Generation{Value: v, Confidence: 0.9, EnoughContent: true, Model: "fake-model"}, nil
}

func (f *fakeModel) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

const longBody = "Ben builds interactive WebGPU experiments and writes about creative coding and design systems."

type fixture struct {
	o       *Observer
	content *store.Memory
	obs     *store.ObserverMemory
	model   *fakeModel
	now     time.Time
}

func newFixture(t *testing.T, withModel bool, mod ...func(*Config)) *fixture {
	t.Helper()
	f := &fixture{content: store.NewMemory(), obs: store.NewObserverMemory(), model: &fakeModel{},
		now: time.Unix(1_900_000_000, 0)}
	ctx := context.Background()
	f.content.SavePage(ctx, content.Page{Slug: "about", Title: "About", Body: longBody, Published: true})
	f.content.SavePage(ctx, content.Page{Slug: "thin", Title: "Thin", Body: "Hi.", Published: true})
	f.content.SavePage(ctx, content.Page{Slug: "draft", Title: "Draft", Body: longBody, Published: false})
	cfg := Config{Content: f.content, Obs: f.obs, ModelName: "cfg-model", Now: func() time.Time { return f.now }}
	if withModel {
		cfg.Model = f.model
	}
	for _, m := range mod {
		m(&cfg)
	}
	f.o = New(cfg)
	return f
}

func gap(item, field string) Report {
	return Report{Kind: "content-gap", Frontend: "builtin/site", Serve: "builtin/site", Route: item,
		Collection: "pages", Item: item, Field: field, Expect: "text", Got: "missing"}
}

func (f *fixture) process(t *testing.T, r Report) store.Detection {
	t.Helper()
	if err := r.normalize(); err != nil {
		t.Fatal(err)
	}
	d, err := f.o.Process(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (f *fixture) generated(t *testing.T) map[contract.Key]map[string]string {
	t.Helper()
	g, err := f.o.GeneratedFields(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGapGeneratesValue(t *testing.T) {
	f := newFixture(t, true)
	d := f.process(t, gap("about", "subtitle"))
	if d.Status != store.StatusFixed || d.Action.Type != "generate" || !d.Action.Auto || d.Action.Value != "Generated subtitle 1" {
		t.Fatalf("detection = %+v", d)
	}
	if !strings.Contains(d.Action.Summary, `generated subtitle for "about": "Generated subtitle 1"`) {
		t.Errorf("summary = %q", d.Action.Summary)
	}
	req := f.model.calls[0]
	if req.Field != "subtitle" || req.Content["body"] != longBody || req.Content["title"] != "About" {
		t.Errorf("request = %+v", req)
	}
	got := f.generated(t)[contract.Key{Collection: "pages", Item: "about"}]["subtitle"]
	if got != "Generated subtitle 1" {
		t.Fatalf("contract value = %q", got)
	}
	g, _ := f.obs.GeneratedField(context.Background(), "pages", "about", "subtitle")
	if g.Model != "fake-model" || g.Status != store.GenActive || g.SourceHash == "" {
		t.Errorf("row = %+v", g)
	}
	// repeats (other visitors, other front ends) don't generate again
	f.process(t, gap("about", "subtitle"))
	other := gap("about", "subtitle")
	other.Frontend = "builtin/particle-stream"
	f.process(t, other)
	if f.model.n() != 1 {
		t.Fatalf("model called %d times, want 1", f.model.n())
	}
	if d, _ := f.obs.DetectionBySignature(context.Background(), gap("about", "subtitle").Signature()); d.Count != 2 {
		t.Errorf("count = %d, want 2", d.Count)
	}
}

func TestTypedGeneration(t *testing.T) {
	f := newFixture(t, true)
	r := gap("about", "tags")
	r.Expect = "list"
	if d := f.process(t, r); d.Status != store.StatusNeedsReview || f.model.n() != 0 {
		t.Fatalf("list without typed serving: %+v", d)
	}
	f = newFixture(t, true, func(c *Config) { c.TypedValues = true })
	f.process(t, r)
	vals, err := f.o.GeneratedValues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	l, ok := vals[contract.Key{Collection: "pages", Item: "about"}]["tags"].([]string)
	if !ok || len(l) != 2 || l[0] != "a" {
		t.Fatalf("typed value = %#v", vals)
	}
	if s := f.generated(t)[contract.Key{Collection: "pages", Item: "about"}]["tags"]; s != `["a","b"]` {
		t.Fatalf("stored = %q", s)
	}
}

func TestHumanWins(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	// the front end had stale content: the body exists, so nothing is generated
	d := f.process(t, gap("about", "body"))
	if d.Status != store.StatusFixed || d.Action.Type != "resolved" || f.model.n() != 0 {
		t.Fatalf("detection = %+v, calls %d", d, f.model.n())
	}
	// an empty title is generated, then your title replaces it in the contract
	f.content.SavePage(ctx, content.Page{Slug: "notitle", Title: "", Body: longBody, Published: true})
	f.process(t, gap("notitle", "title"))
	k := contract.Key{Collection: "pages", Item: "notitle"}
	if f.generated(t)[k]["title"] == "" {
		t.Fatal("title not generated")
	}
	f.content.SavePage(ctx, content.Page{Slug: "notitle", Title: "Mine", Body: longBody, Published: true})
	if v, ok := f.generated(t)[k]["title"]; ok {
		t.Fatalf("generated title %q still served over yours", v)
	}
}

func TestTypeBreakOnHumanValue(t *testing.T) {
	f := newFixture(t, true)
	r := gap("about", "title")
	r.Expect, r.Got = "number", "string"
	d := f.process(t, r)
	if d.Kind != store.KindTypeBreak || d.Status != store.StatusNeedsReview || f.model.n() != 0 {
		t.Fatalf("detection = %+v", d)
	}
	r.Expect = "list"
	d = f.process(t, r)
	if d.Status != store.StatusNeedsReview || d.Action.Value != `["About"]` || f.model.n() != 0 {
		t.Fatalf("lossless coercion proposal = %+v", d)
	}
}

func TestStaleRegeneration(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	f.process(t, gap("about", "subtitle"))
	k := contract.Key{Collection: "pages", Item: "about"}
	if f.generated(t)[k]["subtitle"] != "Generated subtitle 1" {
		t.Fatal("not generated")
	}
	f.content.SavePage(ctx, content.Page{Slug: "about", Title: "About", Body: longBody + " Now also teaching.", Published: true})
	if _, ok := f.generated(t)[k]["subtitle"]; ok {
		t.Fatal("stale value still served")
	}
	if g, _ := f.obs.GeneratedField(ctx, "pages", "about", "subtitle"); g.Status != store.GenStale {
		t.Fatalf("status = %s, want stale", g.Status)
	}
	d := f.process(t, gap("about", "subtitle"))
	if f.model.n() != 2 || d.Status != store.StatusFixed || f.generated(t)[k]["subtitle"] != "Generated subtitle 2" {
		t.Fatalf("not regenerated: calls %d, %+v, %v", f.model.n(), d, f.generated(t))
	}
	if !strings.Contains(f.model.calls[1].Content["body"], "teaching") {
		t.Error("regenerated from old content")
	}
}

func TestTooLittleContent(t *testing.T) {
	f := newFixture(t, true)
	d := f.process(t, gap("thin", "subtitle"))
	if d.Status != store.StatusNeedsReview || !strings.Contains(d.Action.Summary, "too little content") || f.model.n() != 0 {
		t.Fatalf("detection = %+v, calls %d", d, f.model.n())
	}
	if _, ok := f.generated(t)[contract.Key{Collection: "pages", Item: "thin"}]; ok {
		t.Fatal("value generated from too little content")
	}
}

func TestModelDoubts(t *testing.T) {
	cases := map[string]func(GenerateRequest) (Generation, error){
		"low confidence": func(GenerateRequest) (Generation, error) {
			return Generation{Value: "x", Confidence: 0.2, EnoughContent: true}, nil
		},
		"not enough": func(GenerateRequest) (Generation, error) {
			return Generation{Value: "", Confidence: 0.9, EnoughContent: false}, nil
		},
		"invalid": func(GenerateRequest) (Generation, error) {
			return Generation{Value: 7.0, Confidence: 0.9, EnoughContent: true}, nil
		},
		"refused": func(GenerateRequest) (Generation, error) { return Generation{}, ErrRefused },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, true)
			f.model.fn = fn
			d := f.process(t, gap("about", "subtitle"))
			if d.Status != store.StatusNeedsReview {
				t.Fatalf("detection = %+v", d)
			}
			if len(f.generated(t)) != 0 {
				t.Fatal("value stored")
			}
			calls := f.model.n()                   // 2 when the model's answer was unusable (one retry)
			f.process(t, gap("about", "subtitle")) // not retried until the content changes
			if f.model.n() != calls {
				t.Fatalf("model called %d times", f.model.n())
			}
		})
	}
	t.Run("one retry on noise", func(t *testing.T) {
		f := newFixture(t, true)
		f.model.fn = func(GenerateRequest) (Generation, error) {
			if f.model.n() == 1 {
				return Generation{Value: "x", Confidence: 0, EnoughContent: true}, nil
			}
			return Generation{Value: "Good", Confidence: 0.9, EnoughContent: true}, nil
		}
		if d := f.process(t, gap("about", "subtitle")); d.Status != store.StatusFixed || d.Action.Value != "Good" {
			t.Fatalf("detection = %+v", d)
		}
	})
	t.Run("transient error retries", func(t *testing.T) {
		f := newFixture(t, true)
		f.model.fn = func(GenerateRequest) (Generation, error) { return Generation{}, errors.New("overloaded") }
		d := f.process(t, gap("about", "subtitle"))
		if d.Status != store.StatusNew || d.Action.Type != "error" {
			t.Fatalf("detection = %+v", d)
		}
		f.model.fn = nil
		if d = f.process(t, gap("about", "subtitle")); d.Status != store.StatusFixed {
			t.Fatalf("retry = %+v", d)
		}
	})
}

func TestHealingDisabled(t *testing.T) {
	f := newFixture(t, false)
	if f.o.HealingEnabled() {
		t.Fatal("enabled without a model")
	}
	d := f.process(t, gap("about", "subtitle"))
	if d.ID == 0 || d.Status != store.StatusNew || d.Action.Type != "disabled" {
		t.Fatalf("detection = %+v", d)
	}
}

func TestUnknownContentDropped(t *testing.T) {
	f := newFixture(t, true)
	for _, r := range []Report{gap("nope", "subtitle"), gap("draft", "subtitle")} {
		if err := r.normalize(); err != nil {
			t.Fatal(err)
		}
		if _, err := f.o.Process(context.Background(), r); !errors.Is(err, errNoItem) {
			t.Fatalf("%s: err = %v", r.Item, err)
		}
	}
	fe := Report{Kind: "frontend-error", Frontend: "fe/unregistered", Message: "boom"}
	f.process(t, fe)
	if all, _ := f.obs.Detections(context.Background(), 0); len(all) != 0 {
		t.Fatalf("recorded %d detections, want 0", len(all))
	}
}

func TestLimits(t *testing.T) {
	f := newFixture(t, true, func(c *Config) { c.GenPerHour = 1; c.MaxGenItem = 2 })
	f.process(t, gap("about", "subtitle"))
	d := f.process(t, gap("about", "tagline"))
	if d.Action.Type != "deferred" || f.model.n() != 1 {
		t.Fatalf("budget: %+v, calls %d", d, f.model.n())
	}
	f.now = f.now.Add(time.Hour)
	f.process(t, gap("about", "tagline"))
	f.now = f.now.Add(time.Hour)
	d = f.process(t, gap("about", "caption"))
	if d.Status != store.StatusNeedsReview || !strings.Contains(d.Action.Summary, "already has 2 generated fields") {
		t.Fatalf("per-item cap: %+v", d)
	}
}

func feError(fe, msg string) Report {
	return Report{Kind: "frontend-error", Frontend: fe, Serve: fe, Message: msg}
}

func inRotation(t *testing.T, f *fixture, ref string) bool {
	t.Helper()
	in, err := f.o.inRotation(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestFrontendErrorsPullFromRotation(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	const fe = "builtin/particle-stream"
	for i := range 4 {
		d := f.process(t, feError(fe, "TypeError: x is undefined at line "+string(rune('0'+i))))
		if d.Status != store.StatusNeedsReview {
			t.Fatalf("status = %s", d.Status)
		}
		f.now = f.now.Add(time.Second)
	}
	if !inRotation(t, f, fe) {
		t.Fatal("pulled before the threshold")
	}
	if all, _ := f.obs.Detections(ctx, 0); len(all) != 1 || all[0].Count != 4 {
		t.Fatalf("errors differing only in numbers should dedupe: %+v", all)
	}
	d := f.process(t, feError(fe, "TypeError: x is undefined at line 9"))
	if inRotation(t, f, fe) {
		t.Fatal("not pulled after 5 errors")
	}
	if d.Action.Type != "pull" || d.Action.Pulled != fe || !d.Action.Auto || d.Status != store.StatusNeedsReview {
		t.Fatalf("detection = %+v", d)
	}
	if err := f.o.Revert(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if !inRotation(t, f, fe) {
		t.Fatal("revert didn't re-add it")
	}
	if d, _ := f.obs.Detection(ctx, d.ID); d.Status != store.StatusReverted {
		t.Fatalf("status = %s", d.Status)
	}
}

func TestErrorWindowAndNotReady(t *testing.T) {
	f := newFixture(t, true)
	const fe = "builtin/particle-stream"
	for range 4 {
		f.process(t, feError(fe, "boom"))
		f.now = f.now.Add(4 * time.Minute) // spread past the 10-minute window
	}
	f.process(t, feError(fe, "boom"))
	if !inRotation(t, f, fe) {
		t.Fatal("errors outside the window counted")
	}
	for range 3 {
		f.process(t, feError(fe, "front end timed out before ready"))
	}
	if inRotation(t, f, fe) {
		t.Fatal("not pulled after 3 never-ready reports")
	}
}

func TestDefaultFrontendNeverPulled(t *testing.T) {
	f := newFixture(t, true)
	for range 20 {
		f.process(t, feError("builtin/site", "boom"))
	}
	if !inRotation(t, f, "builtin/site") {
		t.Fatal("the default front end was pulled")
	}
}

func TestAdminActions(t *testing.T) {
	ctx := context.Background()
	t.Run("accept into your content", func(t *testing.T) {
		f := newFixture(t, true)
		f.content.SavePage(ctx, content.Page{Slug: "notitle", Body: longBody, Published: true})
		d := f.process(t, gap("notitle", "title"))
		if err := f.o.Accept(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		p, _ := f.content.Page(ctx, "notitle")
		if p.Title != "Generated title 1" || p.Body != longBody || !p.Published {
			t.Fatalf("page = %+v", p)
		}
		if g, _ := f.obs.GeneratedField(ctx, "pages", "notitle", "title"); g.Status != store.GenAccepted {
			t.Fatalf("generated status = %s", g.Status)
		}
		if err := f.o.Accept(ctx, d.ID); !errors.Is(err, ErrAction) {
			t.Fatalf("second accept err = %v", err)
		}
	})
	t.Run("accept, edit, revert, regenerate an extra field", func(t *testing.T) {
		f := newFixture(t, true)
		d := f.process(t, gap("about", "subtitle"))
		k := contract.Key{Collection: "pages", Item: "about"}
		if err := f.o.Accept(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		if f.generated(t)[k]["subtitle"] != "Generated subtitle 1" {
			t.Fatal("accepted value not served")
		}
		if err := f.o.Regenerate(ctx, d.ID); !errors.Is(err, ErrAction) {
			t.Fatalf("regenerating an accepted value: err = %v", err)
		}
		if err := f.o.Edit(ctx, d.ID, "  My own subtitle "); err != nil {
			t.Fatal(err)
		}
		if f.generated(t)[k]["subtitle"] != "My own subtitle" {
			t.Fatalf("edit not served: %v", f.generated(t))
		}
		if err := f.o.Revert(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		if _, ok := f.generated(t)[k]["subtitle"]; ok {
			t.Fatal("reverted value still served")
		}
		f.process(t, gap("about", "subtitle")) // a cleared value isn't regenerated by reports
		if f.model.n() != 1 {
			t.Fatalf("regenerated after revert: %d calls", f.model.n())
		}
		if err := f.o.Regenerate(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		if f.generated(t)[k]["subtitle"] != "Generated subtitle 2" {
			t.Fatalf("regenerate: %v", f.generated(t))
		}
		if err := f.o.Dismiss(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		if d, _ := f.obs.Detection(ctx, d.ID); d.Status != store.StatusDismissed {
			t.Fatalf("status = %s", d.Status)
		}
	})
	t.Run("edit validates by type", func(t *testing.T) {
		f := newFixture(t, true, func(c *Config) { c.TypedValues = true })
		r := gap("about", "year")
		r.Expect = "number"
		d := f.process(t, r)
		if err := f.o.Edit(ctx, d.ID, "soon"); !errors.Is(err, ErrAction) {
			t.Fatalf("err = %v", err)
		}
		if err := f.o.Edit(ctx, d.ID, "2026"); err != nil {
			t.Fatal(err)
		}
		vals, _ := f.o.GeneratedValues(ctx)
		if vals[contract.Key{Collection: "pages", Item: "about"}]["year"] != 2026.0 {
			t.Fatalf("value = %v", vals)
		}
	})
}

func TestContentChecks(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	f.content.SavePage(ctx, content.Page{Slug: "empty", Title: "Empty", Body: " ", Published: true})
	f.content.SaveExperiment(ctx, content.Experiment{Slug: "nosum", Title: "No summary", Published: true})
	f.content.SavePage(ctx, content.Page{Slug: "", Title: "Home", Body: "x", Published: false})
	f.o.CheckContent(ctx)
	f.o.CheckContent(ctx) // idempotent
	open, _ := f.obs.Detections(ctx, 0, store.StatusNeedsReview)
	if len(open) != 3 {
		t.Fatalf("detections = %+v", open)
	}
	for _, d := range open {
		if d.Kind != store.KindContentInvalid || d.Count != 1 {
			t.Fatalf("detection = %+v", d)
		}
	}
	f.content.SavePage(ctx, content.Page{Slug: "empty", Title: "Empty", Body: "Now with words.", Published: true})
	f.content.SavePage(ctx, content.Page{Slug: "", Title: "Home", Body: "x", Published: true})
	f.o.CheckContent(ctx)
	open, _ = f.obs.Detections(ctx, 0, store.StatusNeedsReview)
	if len(open) != 1 || open[0].Collection != "experiments" || open[0].Field != "summary" {
		t.Fatalf("open after fixes = %+v", open)
	}
	fixed, _ := f.obs.Detections(ctx, 0, store.StatusFixed)
	if len(fixed) != 2 {
		t.Fatalf("fixed = %+v", fixed)
	}
	// it comes back: reopened
	f.content.SavePage(ctx, content.Page{Slug: "empty", Title: "Empty", Body: "", Published: true})
	f.o.CheckContent(ctx)
	if open, _ = f.obs.Detections(ctx, 0, store.StatusNeedsReview); len(open) != 2 {
		t.Fatalf("not reopened: %+v", open)
	}
}

func TestAsyncIngestion(t *testing.T) {
	f := newFixture(t, true, func(c *Config) { c.CheckEvery = time.Hour })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.o.Start(ctx)
	f.o.Start(ctx) // no-op
	r := gap("about", "subtitle")
	r.normalize()
	for range 3 {
		if !f.o.Submit("1.2.3.4", r) {
			t.Fatal("submit refused")
		}
	}
	k := contract.Key{Collection: "pages", Item: "about"}
	eventually(t, func() bool {
		d, err := f.obs.DetectionBySignature(ctx, r.Signature())
		return err == nil && d.Count == 3 && f.generated(t)[k]["subtitle"] != ""
	})
	if f.model.n() != 1 {
		t.Fatalf("model called %d times", f.model.n())
	}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestParseReport(t *testing.T) {
	good := `{"kind":"content-gap","frontend":"fe/my-site","serve":"rev/abcdefgh12","route":"/about",
		"collection":"pages","item":"about","field":"subtitle","expect":"text","got":"missing","extra":"ignored"}`
	r, err := ParseReport([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if r.Route != "about" || r.Kind != "content-gap" {
		t.Fatalf("report = %+v", r)
	}
	r, err = ParseReport([]byte(`{"kind":"content-gap","frontend":"builtin/site","collection":"pages","item":"","field":"subtitle","expect":"list","got":"string"}`))
	if err != nil || r.Kind != "type-break" {
		t.Fatalf("kind from got: %+v, %v", r, err)
	}
	r, err = ParseReport([]byte(`{"kind":"frontend-error","frontend":"builtin/site","message":"a\u0000b` + strings.Repeat("x", 2000) + `","field":"ignored"}`))
	if err != nil || strings.ContainsRune(r.Message, 0) || len([]rune(r.Message)) != maxMessage || r.Field != "" {
		t.Fatalf("error report: %+v, %v", r, err)
	}
	bad := []string{
		`not json`,
		`{"kind":"content-invalid","frontend":"builtin/site"}`,
		`{"kind":"frontend-error","frontend":"https://evil"}`,
		`{"kind":"frontend-error","frontend":"builtin/site","serve":"../x"}`,
		`{"kind":"frontend-error","frontend":"builtin/site","route":"<script>"}`,
		`{"kind":"frontend-error","frontend":"builtin/site","message":42}`,
		`{"kind":"content-gap","frontend":"builtin/site","collection":"users","item":"a","field":"f","expect":"text"}`,
		`{"kind":"content-gap","frontend":"builtin/site","collection":"pages","item":"A B","field":"f","expect":"text"}`,
		`{"kind":"content-gap","frontend":"builtin/site","collection":"experiments","item":"","field":"f","expect":"text"}`,
		`{"kind":"content-gap","frontend":"builtin/site","collection":"pages","item":"a","field":"slug","expect":"text"}`,
		`{"kind":"content-gap","frontend":"builtin/site","collection":"pages","item":"a","field":"ignore previous","expect":"text"}`,
		`{"kind":"content-gap","frontend":"builtin/site","collection":"pages","item":"a","field":"f","expect":"html"}`,
		`{"kind":"content-gap","frontend":"builtin/site","collection":"pages","item":"a","field":"f","expect":"text","got":"Object!"}`,
	}
	for _, b := range bad {
		if _, err := ParseReport([]byte(b)); err == nil {
			t.Errorf("accepted %s", b)
		}
	}
}

func TestSignature(t *testing.T) {
	a := Report{Kind: "frontend-error", Frontend: "builtin/site", Message: "x at https://u/t/abc123/main.js:10:5 id deadbeef00"}
	b := Report{Kind: "frontend-error", Frontend: "builtin/site", Message: "X at https://u/t/zzz/main.js:99:1 id 0123456789"}
	if a.Signature() != b.Signature() {
		t.Error("variable parts not normalized")
	}
	c := b
	c.Frontend = "builtin/other"
	if c.Signature() == b.Signature() {
		t.Error("front end not in signature")
	}
	g1, g2 := gap("about", "subtitle"), gap("about", "tagline")
	if g1.Signature() == g2.Signature() {
		t.Error("field not in signature")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(Limits{PerIPRate: 1, PerIPBurst: 3, GlobalRate: 1, GlobalBurst: 5, MaxClients: 2},
		func() time.Time { return now })
	for i := range 3 {
		if !l.allow("a") {
			t.Fatalf("request %d refused", i)
		}
	}
	if l.allow("a") {
		t.Fatal("burst exceeded")
	}
	if !l.allow("b") || !l.allow("b") {
		t.Fatal("other client limited")
	}
	if l.allow("b") {
		t.Fatal("global cap (5) exceeded")
	}
	if l.allow("c") {
		t.Fatal("client table over MaxClients with no idle entries")
	}
	now = now.Add(10 * time.Second) // everyone refills; idle entries can be evicted
	if !l.allow("c") {
		t.Fatal("refill/evict failed")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/observe", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	if ip := ClientIP(r, 1); ip != "10.0.0.1" {
		t.Errorf("no XFF: %s", ip)
	}
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 1.2.3.4, 130.211.0.1")
	if ip := ClientIP(r, 2); ip != "1.2.3.4" {
		t.Errorf("hops 2: %s", ip)
	}
	if ip := ClientIP(r, 1); ip != "130.211.0.1" {
		t.Errorf("hops 1: %s", ip)
	}
	if ip := ClientIP(r, 0); ip != "10.0.0.1" {
		t.Errorf("hops 0: %s", ip)
	}
	if ip := ClientIP(r, 9); ip != "10.0.0.1" {
		t.Errorf("hops beyond header: %s", ip)
	}
}

package observer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/contract"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// A front end made before projects existed: it reads pages and experiments
// (and has a CSS class and a heading named "projects", which don't count).
const oldFrontend = `<!doctype html><html><head><script src="/site-host.js"></script>
<style>.projects { color: red } /* site.projects() */</style></head>
<body><h2>projects</h2><main id=m></main>
<script>
// TODO: site.projects() some day
(async () => {
  await site.loaded;
  for (const p of site.pages()) { site.field(p, "title"); site.field(p, "body"); }
  for (const e of site.experiments()) { site.field(e, "title"); site.field(e, "summary"); }
  site.ready();
})();
</script></body></html>`

// projectsJS reads the projects with the given fields.
func projectsJS(fields ...string) string {
	var sb strings.Builder
	sb.WriteString("export function work() { for (const p of site.projects()) {")
	for _, f := range fields {
		fmt.Fprintf(&sb, " site.field(p, %q, {optional: true});", f)
	}
	sb.WriteString(" } }\n")
	return sb.String()
}

var allProjectFields = []string{"title", "client", "agency", "year", "tags", "roles", "summary", "contribution", "link", "youtube", "media"}

// fakeRebuilder stands in for the builder: it writes a new revision from the
// parent's files plus what add returns.
type fakeRebuilder struct {
	b     store.Builder
	files revfiles.Store

	mu    sync.Mutex
	calls []RebuildRequest
	add   func() revfiles.Files
	err   error
	block chan struct{}
}

func (r *fakeRebuilder) Rebuild(ctx context.Context, req RebuildRequest) (store.Revision, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	block, err, add := r.block, r.err, r.add
	r.mu.Unlock()
	if block != nil {
		<-block
	}
	if err != nil {
		return store.Revision{}, err
	}
	files, err := r.files.Read(ctx, req.Parent)
	if err != nil {
		return store.Revision{}, err
	}
	if add == nil {
		add = func() revfiles.Files { return revfiles.Files{"work.js": []byte(projectsJS(allProjectFields...))} }
	}
	for n, b := range add() {
		files[n] = b
	}
	id := frontend.NewRevisionID()
	if err := r.files.Write(ctx, id, files); err != nil {
		return store.Revision{}, err
	}
	return r.b.AddRevision(ctx, store.Revision{ID: id, FrontendID: req.FrontendID, ParentID: req.Parent,
		Author: "observer", Summary: "Added the work."})
}

func (r *fakeRebuilder) n() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

type driftFixture struct {
	*fixture
	files revfiles.Store
	rb    *fakeRebuilder
	v1    store.Revision
}

// newDriftFixture: the memory store's content plus projects, and a prompted
// front end fe/layers (v1 = oldFrontend) active and in the rotation.
func newDriftFixture(t *testing.T, projects int, mod ...func(*Config)) *driftFixture {
	t.Helper()
	t.Setenv(frontend.RotationEnv, "")
	files := revfiles.NewDir(t.TempDir())
	f := &driftFixture{files: files}
	f.fixture = newFixture(t, true, append([]func(*Config){func(c *Config) { c.Files = files }}, mod...)...)
	ctx := context.Background()
	for i := range projects {
		p := content.Project{Slug: fmt.Sprintf("p%d", i), Title: fmt.Sprintf("Project %d", i), Client: "Nike",
			Year: "2020", Tags: []string{"a"}, Roles: []string{"CD"}, Summary: "A project.", Contribution: "Led it.",
			Palette: []string{"#000000"}, Media: []content.Media{{Kind: "image", Src: fmt.Sprintf("/media/projects/p%d/hero.jpg", i)}},
			Order: i, Published: true}
		if i%2 == 0 {
			p.Agency, p.Link, p.YouTube = "Adam & Eve", "https://example.com/", "e6PKBbvRYV0"
		}
		if i == 0 {
			p.Body = "A rare body."
		}
		if err := f.content.SaveProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	// built-in particle-stream reads nothing; keep it out of these tests
	f.content.SetFrontendInRotation(ctx, "builtin/particle-stream", false)
	f.v1 = f.addRevision(t, "fe/layers", "", revfiles.Files{"index.html": []byte(oldFrontend)})
	if err := f.content.SetActiveRevision(ctx, "fe/layers", f.v1.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.content.SetFrontendInRotation(ctx, "fe/layers", true); err != nil {
		t.Fatal(err)
	}
	f.rb = &fakeRebuilder{b: f.content, files: files}
	f.o.SetRebuilder(f.rb)
	return f
}

func (f *driftFixture) addRevision(t *testing.T, fe, parent string, files revfiles.Files) store.Revision {
	t.Helper()
	ctx := context.Background()
	if _, err := f.content.BuilderFrontend(ctx, fe); errors.Is(err, store.ErrNotFound) {
		if err := f.content.CreatePromptedFrontend(ctx, fe, "Layers"); err != nil {
			t.Fatal(err)
		}
	}
	id := frontend.NewRevisionID()
	if err := f.files.Write(ctx, id, files); err != nil {
		t.Fatal(err)
	}
	rev, err := f.content.AddRevision(ctx, store.Revision{ID: id, FrontendID: fe, ParentID: parent, Author: "ben"})
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func (f *driftFixture) check(t *testing.T) {
	t.Helper()
	if err := f.o.CheckDrift(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (f *driftFixture) drifts(t *testing.T) []store.Detection {
	t.Helper()
	all, err := f.obs.Detections(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Detection
	for _, d := range all {
		if d.Kind == store.KindContentDrift {
			out = append(out, d)
		}
	}
	return out
}

func (f *driftFixture) active(t *testing.T, fe string) string {
	t.Helper()
	info, err := f.content.BuilderFrontend(context.Background(), fe)
	if err != nil {
		t.Fatal(err)
	}
	return info.ActiveRevision
}

func shapeOf(t *testing.T, f *fixture) contract.Shape {
	t.Helper()
	site, err := contract.Build(context.Background(), f.content, f.o)
	if err != nil {
		t.Fatal(err)
	}
	return contract.ShapeOf(site)
}

func keys(m []missing) string {
	var out []string
	for _, x := range m {
		out = append(out, x.key())
	}
	return strings.Join(out, " ")
}

func TestStaticScan(t *testing.T) {
	f := newDriftFixture(t, 4)
	shape := shapeOf(t, f.fixture)

	// projects missing as a whole; the CSS class, the heading and the
	// comments don't count as reading them
	old := revfiles.Files{"index.html": []byte(oldFrontend)}
	if got := keys(scanDrift(old, shape)); got != "projects" {
		t.Fatalf("old front end: missing %q", got)
	}

	// reading projects but only some fields: the widely used ones are missing;
	// body (1 of 4) is below the threshold, palette is ignored
	some := revfiles.Files{"index.html": []byte(oldFrontend), "work.js": []byte(projectsJS("title", "client"))}
	got := keys(scanDrift(some, shape))
	// (summary counts as read: the experiments read a "summary"; the scan is lenient)
	want := "projects.agency projects.contribution projects.link projects.media projects.roles projects.tags projects.year projects.youtube"
	if got != want {
		t.Fatalf("partial:\n got %s\nwant %s", got, want)
	}
	all := revfiles.Files{"index.html": []byte(oldFrontend), "work.js": []byte(projectsJS(allProjectFields...))}
	if got := keys(scanDrift(all, shape)); got != "" {
		t.Fatalf("complete front end: missing %q", got)
	}

	// other ways of reading a collection and its fields
	for _, code := range []string{
		`const {pages, projects} = site.content; projects.map(p => [p.title, p.client]);`,
		`site.collection("projects")`,
		`site.get('projects.0.title', "")`,
		`const p = site.project(slug)`,
		"const w = site.content.projects;",
	} {
		if !readsCollection(frontendCode(revfiles.Files{"a.js": []byte(code)}), "projects") {
			t.Errorf("not a read of projects: %s", code)
		}
	}
	for _, code := range []string{`"workprojects"`, `const projectsCount = 0`, `.projectsList`, `// site.projects()`, `/* site.projects() */`} {
		if readsCollection(frontendCode(revfiles.Files{"a.js": []byte(code)}), "projects") {
			t.Errorf("counted as a read of projects: %s", code)
		}
	}
	if !readsField(`item.client`, "client") || !readsField(`{client, year} = p`, "client") || readsField(`clientele`, "client") {
		t.Error("field reads")
	}
	// URLs inside strings survive comment stripping
	if code := frontendCode(revfiles.Files{"a.js": []byte(`const u = "https://x.test/"; site.projects();`)}); !readsCollection(code, "projects") {
		t.Errorf("code = %q", code)
	}

	// the threshold (25%): a field in 1 of 5 items isn't expected; in 1 of 4 it is
	c := contract.CollectionShape{Name: "projects", Items: 5}
	if expected(c, contract.FieldShape{Name: "body", NonEmpty: 1}) {
		t.Error("1 of 5 expected")
	}
	c.Items = 4
	if !expected(c, contract.FieldShape{Name: "body", NonEmpty: 1}) {
		t.Error("1 of 4 not expected")
	}

	// built-ins: builtin/site renders every collection; others none
	if got := builtinDrift("builtin/site", shape); len(got) != 0 {
		t.Errorf("builtin/site drift = %+v", got)
	}
	if got := keys(builtinDrift("builtin/particle-stream", shape)); got != "experiments pages projects" {
		t.Errorf("particle-stream drift = %q", got)
	}
}

func TestDriftHealsByRebuild(t *testing.T) {
	f := newDriftFixture(t, 4)
	ctx := context.Background()
	f.check(t)

	if f.rb.n() != 1 {
		t.Fatalf("rebuilds = %d", f.rb.n())
	}
	req := f.rb.calls[0]
	if req.FrontendID != "fe/layers" || req.Parent != f.v1.ID {
		t.Fatalf("request = %+v", req)
	}
	for _, s := range []string{"projects: 4 published items", "client (4 of 4)", "agency (2 of 4)", "site.projects() / site.project(slug)",
		`"work" route`, "existing creative concept", `"client":"Nike"`} {
		if !strings.Contains(req.Prompt, s) {
			t.Errorf("prompt lacks %q:\n%s", s, req.Prompt)
		}
	}
	if strings.Contains(req.Prompt, "palette") {
		t.Error("prompt mentions the ignored palette")
	}

	ds := f.drifts(t)
	if len(ds) != 1 {
		t.Fatalf("drift detections = %+v", ds)
	}
	d := ds[0]
	a := d.Action
	if d.Status != store.StatusFixed || a.Type != "rebuild" || !a.Auto || !a.Activated || a.Previous != f.v1.ID ||
		a.PreviousNumber != 1 || a.RevisionNumber != 2 || a.Revision == "" {
		t.Fatalf("detection = %+v", d)
	}
	if !strings.HasPrefix(a.Summary, "Rebuilt v1 → v2 by the observer") || !a.ProbationUntil.Equal(f.now.Add(30*time.Minute)) {
		t.Errorf("action = %+v", a)
	}
	if !strings.Contains(string(d.Sample), `Layers v1 doesn't show projects`) || d.Frontend != "fe/layers" || d.Serve != "rev/"+f.v1.ID {
		t.Errorf("detection = %+v %s", d, d.Sample)
	}
	if got := f.active(t, "fe/layers"); got != a.Revision {
		t.Fatalf("active = %s, want %s", got, a.Revision)
	}
	v2, _ := f.content.Revision(ctx, a.Revision)
	if v2.Author != "observer" || v2.ParentID != f.v1.ID {
		t.Errorf("v2 = %+v", v2)
	}
	rbs, _ := f.obs.Rebuilds(ctx, 0)
	if len(rbs) != 1 || rbs[0].Status != store.RunDone || rbs[0].Revision != v2.ID {
		t.Errorf("rebuilds = %+v", rbs)
	}

	// the next check finds nothing: v2 reads the projects
	f.check(t)
	if f.rb.n() != 1 || len(f.drifts(t)) != 1 {
		t.Fatalf("second check: rebuilds %d, detections %d", f.rb.n(), len(f.drifts(t)))
	}

	// Revert: v1 is active again, and stays (one attempt per missing set)
	if err := f.o.Revert(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if f.active(t, "fe/layers") != f.v1.ID {
		t.Fatal("revert didn't activate v1")
	}
	d, _ = f.obs.Detection(ctx, d.ID)
	if d.Status != store.StatusReverted || d.Action.Activated {
		t.Fatalf("after revert = %+v", d)
	}
	f.check(t)
	d, _ = f.obs.Detection(ctx, d.ID)
	if f.rb.n() != 1 || d.Status != store.StatusReverted || d.Count != 2 {
		t.Fatalf("after revert and check: rebuilds %d, %+v", f.rb.n(), d)
	}
}

func TestDriftUnresolvedNeedsReview(t *testing.T) {
	f := newDriftFixture(t, 4)
	// the rebuild reads the projects but forgets most fields
	f.rb.add = func() revfiles.Files { return revfiles.Files{"work.js": []byte(projectsJS("title"))} }
	f.check(t)
	ds := f.drifts(t)
	if len(ds) != 1 || ds[0].Status != store.StatusNeedsReview || ds[0].Action.Activated || ds[0].Action.Revision == "" {
		t.Fatalf("detection = %+v", ds)
	}
	if !strings.Contains(ds[0].Action.Summary, "still doesn't read projects.agency") || !strings.Contains(ds[0].Action.Summary, "wasn't activated") {
		t.Errorf("summary = %q", ds[0].Action.Summary)
	}
	if f.active(t, "fe/layers") != f.v1.ID {
		t.Fatal("an unresolved rebuild was activated")
	}
	// one attempt ever for this missing set
	f.check(t)
	f.check(t)
	if f.rb.n() != 1 {
		t.Fatalf("rebuilds = %d", f.rb.n())
	}
	if d, _ := f.obs.Detection(context.Background(), ds[0].ID); d.Count != 3 || d.Status != store.StatusNeedsReview {
		t.Fatalf("detection = %+v", d)
	}
}

func TestDriftRebuildFailure(t *testing.T) {
	f := newDriftFixture(t, 2)
	f.rb.err = errors.New("the model declined this request")
	f.check(t)
	ds := f.drifts(t)
	if len(ds) != 1 || ds[0].Status != store.StatusNeedsReview || !strings.Contains(ds[0].Action.Summary, "rebuild failed: the model declined") {
		t.Fatalf("detection = %+v", ds)
	}
	rbs, _ := f.obs.Rebuilds(context.Background(), 0)
	if len(rbs) != 1 || rbs[0].Status != store.RunFailed {
		t.Fatalf("rebuilds = %+v", rbs)
	}
	f.rb.err = nil
	f.check(t)
	if f.rb.n() != 1 || f.active(t, "fe/layers") != f.v1.ID {
		t.Fatal("retried after a failure")
	}

	// busy (Ben is chatting): deferred, and retried at the next check
	g := newDriftFixture(t, 2)
	g.rb.err = ErrBusy
	g.check(t)
	if ds := g.drifts(t); len(ds) != 1 || ds[0].Status != store.StatusNew || ds[0].Action.Type != "deferred" {
		t.Fatalf("busy: %+v", ds)
	}
	g.rb.err = nil
	g.check(t)
	if ds := g.drifts(t); g.rb.n() != 2 || ds[0].Status != store.StatusFixed {
		t.Fatalf("after busy: rebuilds %d, %+v", g.rb.n(), ds)
	}
}

func TestDriftTargets(t *testing.T) {
	f := newDriftFixture(t, 2)
	ctx := context.Background()
	// prompted front ends out of the rotation, or without an active revision,
	// aren't checked
	f.addRevision(t, "fe/draft", "", revfiles.Files{"index.html": []byte(oldFrontend)})
	f.content.SetFrontendInRotation(ctx, "fe/draft", true) // no active revision
	other := f.addRevision(t, "fe/aside", "", revfiles.Files{"index.html": []byte(oldFrontend)})
	f.content.SetActiveRevision(ctx, "fe/aside", other.ID) // not in the rotation
	f.check(t)
	if f.rb.n() != 1 || f.rb.calls[0].FrontendID != "fe/layers" {
		t.Fatalf("calls = %+v", f.rb.calls)
	}

	// built-ins: needs review, never rebuilt; builtin/site shows everything
	f.content.SetFrontendInRotation(ctx, "builtin/particle-stream", true)
	f.check(t)
	var ps *store.Detection
	for _, d := range f.drifts(t) {
		if d.Frontend == "builtin/site" {
			t.Fatalf("builtin/site flagged: %+v", d)
		}
		if d.Frontend == "builtin/particle-stream" {
			ps = &d
		}
	}
	if ps == nil || ps.Status != store.StatusNeedsReview || !strings.Contains(ps.Action.Summary, "code is in the repo") ||
		!strings.Contains(string(ps.Sample), "doesn't show pages, experiments and projects") {
		t.Fatalf("particle-stream = %+v", ps)
	}
	if f.rb.n() != 1 {
		t.Fatal("a built-in was rebuilt")
	}

	// FRONTEND_ROTATION overrides the store's rotation
	t.Setenv(frontend.RotationEnv, "builtin/site,fe/aside")
	g := newDriftFixture(t, 2)
	if g.o.override != nil {
		t.Fatal("fixture should clear the override")
	}
	g.o.override = frontend.Rotation{"builtin/site", "fe/aside"} // as New reads it from the env
	aside := g.addRevision(t, "fe/aside", "", revfiles.Files{"index.html": []byte(oldFrontend)})
	g.content.SetActiveRevision(ctx, "fe/aside", aside.ID)
	g.check(t)
	if g.rb.n() != 1 || g.rb.calls[0].FrontendID != "fe/aside" {
		t.Fatalf("override calls = %+v", g.rb.calls)
	}
}

func TestDriftHealingOff(t *testing.T) {
	f := newDriftFixture(t, 2)
	f.o.SetRebuilder(nil)
	f.check(t)
	ds := f.drifts(t)
	if len(ds) != 1 || ds[0].Status != store.StatusNew || ds[0].Action.Type != "disabled" {
		t.Fatalf("detection = %+v", ds)
	}
	// enabled later: the pending detection is rebuilt
	f.o.SetRebuilder(f.rb)
	f.check(t)
	if ds := f.drifts(t); f.rb.n() != 1 || ds[0].Status != store.StatusFixed {
		t.Fatalf("after enabling: %d %+v", f.rb.n(), ds)
	}
}

func TestDriftDailyCap(t *testing.T) {
	f := newDriftFixture(t, 2, func(c *Config) { c.RebuildsPerDay = 1 })
	ctx := context.Background()
	other := f.addRevision(t, "fe/second", "", revfiles.Files{"index.html": []byte(oldFrontend)})
	f.content.SetActiveRevision(ctx, "fe/second", other.ID)
	f.content.SetFrontendInRotation(ctx, "fe/second", true)
	f.check(t)
	if f.rb.n() != 1 {
		t.Fatalf("rebuilds = %d", f.rb.n())
	}
	var deferred *store.Detection
	for _, d := range f.drifts(t) {
		if d.Action.Type == "deferred" {
			deferred = &d
		}
	}
	if deferred == nil || deferred.Status != store.StatusNew || !strings.Contains(deferred.Action.Summary, "daily rebuild budget (1)") {
		t.Fatalf("drifts = %+v", f.drifts(t))
	}
	// a day later the budget is back
	f.obs.RebuildNow = func() time.Time { return time.Now().Add(25 * time.Hour) }
	f.check(t)
	if f.rb.n() != 2 {
		t.Fatalf("rebuilds after a day = %d", f.rb.n())
	}
}

func TestDriftOneRebuildAtATime(t *testing.T) {
	f := newDriftFixture(t, 2)
	ctx := context.Background()
	f.rb.block = make(chan struct{})
	done := make(chan struct{})
	go func() { f.o.CheckDrift(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.rb.n() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// another instance's claim is refused while this one runs
	if _, err := f.obs.ClaimRebuild(ctx, store.Rebuild{Frontend: "fe/other", Fingerprint: "x"}, 6); !errors.Is(err, store.ErrRebuildBusy) {
		t.Fatalf("concurrent claim err = %v", err)
	}
	// and this instance's next check waits for it
	second := make(chan struct{})
	go func() { f.o.CheckDrift(ctx); close(second) }()
	select {
	case <-second:
		t.Fatal("a second check ran during a rebuild")
	case <-time.After(50 * time.Millisecond):
	}
	if ds := f.drifts(t); len(ds) != 1 || ds[0].Action.Type != "rebuilding" {
		t.Fatalf("while running: %+v", ds)
	}
	close(f.rb.block)
	<-done
	<-second
	if f.rb.n() != 1 {
		t.Fatalf("rebuilds = %d", f.rb.n())
	}
	// a claim left running by a dead instance is taken over after RebuildStale
	m := store.NewObserverMemory()
	start := time.Now()
	m.RebuildNow = func() time.Time { return start }
	if _, err := m.ClaimRebuild(ctx, store.Rebuild{Frontend: "fe/a", Fingerprint: "x"}, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimRebuild(ctx, store.Rebuild{Frontend: "fe/a", Fingerprint: "x"}, 6); !errors.Is(err, store.ErrRebuildDone) {
		t.Fatalf("again err = %v", err)
	}
	m.RebuildNow = func() time.Time { return start.Add(store.RebuildStale + time.Minute) }
	if _, err := m.ClaimRebuild(ctx, store.Rebuild{Frontend: "fe/a", Fingerprint: "x"}, 6); err != nil {
		t.Fatalf("stale takeover err = %v", err)
	}
}

func errReport(fe, serve, msg string) Report {
	return Report{Kind: store.KindFrontendError, Frontend: fe, Serve: serve, Message: msg}
}

func TestDriftProbationRollsBack(t *testing.T) {
	f := newDriftFixture(t, 2, func(c *Config) { c.ErrorLimit = 2 }) // the pull rule would fire first
	ctx := context.Background()
	f.check(t)
	d := f.drifts(t)[0]
	v2 := d.Action.Revision
	if f.active(t, "fe/layers") != v2 {
		t.Fatal("not activated")
	}
	// errors from an old tab on v1 don't count
	f.process(t, errReport("fe/layers", "rev/"+f.v1.ID, "old tab error 1"))
	f.process(t, errReport("fe/layers", "rev/"+f.v1.ID, "old tab error 2"))
	f.process(t, errReport("fe/layers", "rev/"+v2, "TypeError: x is undefined"))
	f.process(t, errReport("fe/layers", "rev/"+v2, "TypeError: y is undefined"))
	if f.active(t, "fe/layers") != v2 {
		t.Fatal("rolled back too early")
	}
	if !f.inRotation(t, "fe/layers") {
		t.Fatal("pulled from the rotation during probation")
	}
	f.process(t, errReport("fe/layers", "rev/"+v2, "TypeError: z is undefined"))
	if f.active(t, "fe/layers") != f.v1.ID {
		t.Fatal("not rolled back after 3 errors")
	}
	if !f.inRotation(t, "fe/layers") {
		t.Fatal("pulled instead of rolled back")
	}
	d, _ = f.obs.Detection(ctx, d.ID)
	if d.Status != store.StatusNeedsReview || d.Action.Type != "rollback" || d.Action.Activated || d.Action.Revision != v2 ||
		!strings.Contains(d.Action.Summary, "Rolled back v2 → v1 after 3 errors") {
		t.Fatalf("detection = %+v", d)
	}
	// no new rebuild for the same missing set
	f.check(t)
	if f.rb.n() != 1 || f.active(t, "fe/layers") != f.v1.ID {
		t.Fatal("rebuilt again after a rollback")
	}

	// never-ready reports: two roll back
	g := newDriftFixture(t, 2)
	g.check(t)
	v2 = g.drifts(t)[0].Action.Revision
	nr := errReport("fe/layers", "rev/"+v2, "never got ready")
	nr.Got = "timeout"
	g.process(t, nr)
	g.process(t, nr)
	if g.active(t, "fe/layers") != g.v1.ID {
		t.Fatal("not rolled back after 2 never-ready reports")
	}

	// after the probation, errors follow the normal rules again
	h := newDriftFixture(t, 2, func(c *Config) { c.ErrorLimit = 2 })
	h.check(t)
	v2 = h.drifts(t)[0].Action.Revision
	h.now = h.now.Add(31 * time.Minute)
	h.process(t, errReport("fe/layers", "rev/"+v2, "a"))
	h.process(t, errReport("fe/layers", "rev/"+v2, "b"))
	if h.active(t, "fe/layers") != v2 || h.inRotation(t, "fe/layers") {
		t.Fatal("after probation: expected a pull, not a rollback")
	}
}

func (f *driftFixture) inRotation(t *testing.T, fe string) bool {
	t.Helper()
	info, err := f.content.BuilderFrontend(context.Background(), fe)
	if err != nil {
		t.Fatal(err)
	}
	return info.InRotation
}

func TestContentChangedTriggersCheck(t *testing.T) {
	f := newDriftFixture(t, 0, func(c *Config) {
		c.DriftDelay, c.DriftEvery, c.DriftDebounce = time.Hour, time.Hour, time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.o.Start(ctx)
	if f.rb.n() != 0 {
		t.Fatal("rebuilt without projects")
	}
	f.content.SaveProject(ctx, content.Project{Slug: "new", Title: "New", Client: "Nike", Published: true})
	f.o.ContentChanged()
	deadline := time.Now().Add(5 * time.Second)
	for f.rb.n() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.rb.n() != 1 || f.o.ContentChanges() != 1 {
		t.Fatalf("rebuilds = %d, changes = %d", f.rb.n(), f.o.ContentChanges())
	}
}

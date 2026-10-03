package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/observer"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// A prompted front end made before projects existed.
const preProjectsIndex = `<!doctype html><html><head><script src="/site-host.js"></script></head><body><main id=m></main><script>
(async () => { await site.loaded;
  for (const p of site.pages()) { site.field(p, "title"); site.field(p, "body"); }
  for (const e of site.experiments()) { site.field(e, "title"); site.field(e, "summary"); site.field(e, "link", {optional: true}); site.field(e, "media", {expect: "list"}); }
  site.onRoute(() => {}); site.ready(); })();
</script></body></html>`

// workJS: what the scripted model adds (reads every widely used project field).
const workJS = `export function work(route) {
  if (route === "work") return site.projects().map(p => site.field(p, "title"));
  const p = site.project(route.slice(5));
  for (const f of ["client", "agency", "year", "tags", "roles", "summary", "contribution", "link", "youtube", "media"]) site.field(p, f, {optional: true});
}
`

type driftEnv struct {
	*builderEnv
	o   *observer.Observer
	obs *store.ObserverMemory
}

// newDriftServer: a server with the builder (scripted model) and the observer,
// wired as cmd/server does, and fe/layers v1 active and in the rotation.
func newDriftServer(t *testing.T) (*driftEnv, store.Revision) {
	t.Helper()
	t.Setenv(frontend.RotationEnv, "")
	dir := t.TempDir()
	files := revfiles.NewDir(dir)
	env := &builderEnv{st: store.NewMemory(), dir: dir, model: &builder.ScriptedModel{}}
	obs := store.NewObserverMemory()
	o := observer.New(observer.Config{Content: env.st, Obs: obs, Files: files, CheckEvery: time.Hour,
		DriftDelay: time.Hour, DriftEvery: time.Hour})
	agent := builder.New(env.model, builder.Config{Model: "claude-opus-5-5"})
	cfg := config.Config{Env: "dev", AdminUser: "admin", AdminPassword: "pw", SigningKey: testKey,
		MainOrigin: origin, UsercontentOrigin: "http://127.0.0.1:8081", FrontendsDir: dir}
	s, err := New(cfg, env.st, slog.New(slog.NewTextHandler(io.Discard, nil)), WithBuilder(agent, files), WithObserver(o))
	if err != nil {
		t.Fatal(err)
	}
	o.SetRebuilder(s)
	env.s = s
	ctx := context.Background()
	env.st.SetFrontendInRotation(ctx, "builtin/particle-stream", false)

	if rec := env.form("/admin/builder/new", url.Values{"slug": {"layers"}, "title": {"A set of webgpu layers"}}); rec.Code != 303 {
		t.Fatalf("new: %d", rec.Code)
	}
	body, _ := json.Marshal(map[string]any{"files": map[string]string{"index.html": preProjectsIndex}, "summary": "Noir layers."})
	rec := env.admin("POST", "/admin/builder/fe/layers/import", strings.NewReader(string(body)), "Content-Type", "application/json")
	if rec.Code != 200 {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	var imp struct{ ID string }
	json.Unmarshal(rec.Body.Bytes(), &imp)
	if rec := env.form("/admin/builder/rotation", url.Values{"id": {"fe/layers"}, "in_rotation": {"1"}}); rec.Code != 303 {
		t.Fatalf("rotation: %d", rec.Code)
	}
	v1, err := env.st.Revision(ctx, imp.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &driftEnv{builderEnv: env, o: o, obs: obs}, v1
}

func TestContentChangeHooks(t *testing.T) {
	e, _ := newDriftServer(t)
	n := e.o.ContentChanges() // the rotation toggle above counted
	if n == 0 {
		t.Fatal("the rotation toggle didn't notify the observer")
	}
	if rec := workAdmin(e.s, "POST", "/admin/import/projects", "application/json", importJSON, nil); rec.Code != 200 {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if e.o.ContentChanges() != n+1 {
		t.Fatalf("import: changes = %d", e.o.ContentChanges())
	}
	if rec := e.form("/admin/pages/edit", url.Values{"slug": {"about"}, "title": {"About"}, "body": {"Hi."}, "published": {"on"}}); rec.Code != 303 {
		t.Fatalf("page save: %d", rec.Code)
	}
	if rec := e.form("/admin/experiments/particle-stream", url.Values{"title": {"PS"}, "summary": {"s"}, "published": {"on"}}); rec.Code != 303 {
		t.Fatalf("experiment save: %d", rec.Code)
	}
	if rec := e.form("/admin/projects/second/publish", url.Values{"published": {"0"}}); rec.Code != 303 {
		t.Fatalf("publish toggle: %d", rec.Code)
	}
	if e.o.ContentChanges() != n+4 {
		t.Fatalf("saves: changes = %d, want %d", e.o.ContentChanges(), n+4)
	}
}

func TestDriftRebuildThroughBuilder(t *testing.T) {
	e, v1 := newDriftServer(t)
	ctx := context.Background()

	// no projects yet: nothing to do
	if err := e.o.CheckDrift(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.model.Requests) != 0 {
		t.Fatal("rebuilt without drift")
	}

	if rec := workAdmin(e.s, "POST", "/admin/import/projects", "application/json", importJSON, nil); rec.Code != 200 {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	e.model.Responses = []string{
		builder.ToolUse("1", "write_file", map[string]any{"path": "work.js", "content": workJS}),
		builder.ToolUse("2", "finish", map[string]any{"summary": "Work joins the layers: each project is a pane in the blinds."}),
		builder.ToolUse("3", "finish", map[string]any{"summary": "Work joins the layers: each project is a pane in the blinds."}),
	}
	if err := e.o.CheckDrift(ctx); err != nil {
		t.Fatal(err)
	}

	// the normal builder run: system prompt + quality brief + tools brief, history, the observer's request
	if len(e.model.Requests) == 0 {
		t.Fatal("no model call")
	}
	req := e.model.Requests[0]
	if len(req.System) != 3 || req.System[0].Text != builder.SystemPrompt || req.System[1].Text != builder.QualityBrief || req.System[2].Text != builder.ToolsBrief(nil) {
		t.Fatalf("system blocks = %d", len(req.System))
	}
	msgs, _ := json.Marshal(req.Messages)
	for _, s := range []string{"A request from Ben's site observer", "doesn't show at all", "Noir layers.", "QLEDecode", "existing creative concept"} {
		if !strings.Contains(string(msgs), s) {
			t.Errorf("messages lack %q", s)
		}
	}

	f, _ := e.st.BuilderFrontend(ctx, "fe/layers")
	v2, err := e.st.Revision(ctx, f.ActiveRevision)
	if err != nil || v2.Number != 2 || v2.Author != "observer" || v2.ParentID != v1.ID {
		t.Fatalf("active = %+v, %v", v2, err)
	}
	turns := builder.ParseConversation(v2.Conversation)
	if n := len(turns); n != 4 || turns[2].By != "observer" || !strings.Contains(turns[2].Text, "projects") || turns[3].Role != "assistant" {
		t.Fatalf("conversation = %+v", turns)
	}
	runs, _ := e.st.Runs(ctx, "fe/layers", 5)
	if len(runs) != 1 || runs[0].Status != store.RunDone || runs[0].RevisionID != v2.ID {
		t.Fatalf("runs = %+v", runs)
	}
	dets, _ := e.obs.Detections(ctx, 0)
	if len(dets) != 1 || dets[0].Kind != store.KindContentDrift || dets[0].Status != store.StatusFixed || !dets[0].Action.Activated {
		t.Fatalf("detections = %+v", dets)
	}
	d := dets[0]

	// the admin: the notification dot counts it; the row reads clearly
	if n := unseen(t, e.s); n != 1 {
		t.Fatalf("unseen = %d", n)
	}
	rec := adminReq(e.s, "GET", "/admin/observer/?tab=fixed", "")
	page := rec.Body.String()
	for _, s := range []string{"A set of webgpu layers v1 doesn&#39;t show projects", "Rebuilt v1 → v2 by the observer and activated it",
		`href="/admin/builder/fe/layers?rev=` + v2.ID + `"`, "Preview v2 in the builder", "Revert (activate v1)", "Dismiss"} {
		if !strings.Contains(page, s) {
			t.Errorf("observer page lacks %q", s)
		}
	}
	// the builder shows the observer as the author
	rec = adminReq(e.s, "GET", "/admin/builder/fe/layers?rev="+v2.ID, "")
	if !strings.Contains(rec.Body.String(), "<td>observer</td>") || !strings.Contains(rec.Body.String(), "Site observer") {
		t.Error("builder page doesn't show the observer as author")
	}

	// Revert from the admin activates v1 again
	if rec := adminReq(e.s, "POST", "/admin/observer/"+itoa(d.ID)+"/revert", "tab=fixed"); rec.Code != 303 {
		t.Fatalf("revert: %d %s", rec.Code, rec.Body)
	}
	if f, _ := e.st.BuilderFrontend(ctx, "fe/layers"); f.ActiveRevision != v1.ID {
		t.Fatalf("after revert active = %s", f.ActiveRevision)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestDriftRebuildBusyWhileChatting(t *testing.T) {
	e, v1 := newDriftServer(t)
	e.s.builder.mu.Lock()
	e.s.builder.running["fe/layers"] = true
	e.s.builder.mu.Unlock()
	_, err := e.s.Rebuild(context.Background(), observer.RebuildRequest{FrontendID: "fe/layers", Parent: v1.ID, Prompt: "x"})
	if err != observer.ErrBusy {
		t.Fatalf("err = %v", err)
	}
}

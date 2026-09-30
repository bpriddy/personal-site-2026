package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/fetoken"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

const fixtureIndex = `<!doctype html><html><head><script src="/site-host.js"></script></head><body><main id=m></main><script>
(async () => { await site.loaded; document.getElementById("m").textContent = site.field(site.page(site.route), "title", {expect: "text"});
site.onRoute(() => {}); site.navigate; site.ready(); })();
</script></body></html>`

type builderEnv struct {
	s     *Server
	st    *store.Memory
	dir   string
	model *builder.ScriptedModel
}

func newBuilderServer(t *testing.T, withModel bool) *builderEnv {
	t.Helper()
	t.Setenv(frontend.RotationEnv, "")
	dir := t.TempDir()
	cfg := config.Config{
		Env: "dev", AdminUser: "admin", AdminPassword: "pw", SigningKey: testKey,
		MainOrigin: "http://localhost:8080", UsercontentOrigin: "http://127.0.0.1:8081", FrontendsDir: dir,
	}
	env := &builderEnv{st: store.NewMemory(), dir: dir, model: &builder.ScriptedModel{}}
	var agent *builder.Builder
	if withModel {
		agent = builder.New(env.model, builder.Config{Model: "claude-opus-5-5", Fallbacks: true})
	}
	s, err := New(cfg, env.st, slog.New(slog.NewTextHandler(io.Discard, nil)), WithBuilder(agent, revfiles.NewDir(dir)))
	if err != nil {
		t.Fatal(err)
	}
	env.s = s
	return env
}

func (e *builderEnv) admin(method, target string, body io.Reader, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	req.SetBasicAuth("admin", "pw")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.s.ServeHTTP(rec, req)
	return rec
}

func (e *builderEnv) form(target string, vals url.Values) *httptest.ResponseRecorder {
	return e.admin("POST", target, strings.NewReader(vals.Encode()), "Content-Type", "application/x-www-form-urlencoded")
}

// sseEvents parses a text/event-stream body.
func sseEvents(t *testing.T, body string) []builder.Event {
	t.Helper()
	var out []builder.Event
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if line, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var ev builder.Event
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Fatalf("bad event %q: %v", line, err)
			}
			out = append(out, ev)
		}
	}
	return out
}

func eventOf(evs []builder.Event, typ string) *builder.Event {
	for i := range evs {
		if evs[i].Type == typ {
			return &evs[i]
		}
	}
	return nil
}

func (e *builderEnv) chat(t *testing.T, slug, prompt, parent string) []builder.Event {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"prompt": prompt, "parent": parent})
	rec := e.admin("POST", "/admin/builder/fe/"+slug+"/chat", strings.NewReader(string(body)),
		"Content-Type", "application/json", "Sec-Fetch-Site", "same-origin")
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body)
	}
	return sseEvents(t, rec.Body.String())
}

func TestBuilderChatRevisionsPublishAndRollback(t *testing.T) {
	e := newBuilderServer(t, true)
	ctx := context.Background()

	if rec := e.form("/admin/builder/new", url.Values{"slug": {"dark"}, "title": {"Dark"}}); rec.Code != 303 || rec.Header().Get("Location") != "/admin/builder/fe/dark" {
		t.Fatalf("new: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := e.form("/admin/builder/new", url.Values{"slug": {"dark"}}); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Fatalf("duplicate new: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := e.form("/admin/builder/new", url.Values{"slug": {"Bad Slug"}}); !strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Fatal("bad slug accepted")
	}

	// first revision from a prompt
	e.model.Responses = []string{
		builder.ToolUse("1", "write_file", map[string]any{"path": "index.html", "content": fixtureIndex}),
		builder.ToolUse("2", "finish", map[string]any{"summary": "A dark page."}),
		builder.ToolUse("3", "finish", map[string]any{"summary": "A dark page."}), // after the soft warnings
	}
	evs := e.chat(t, "dark", "make it dark", "")
	rev := eventOf(evs, "revision")
	if rev == nil || rev.Number != 1 || !frontend.ValidRevisionID(rev.Revision) || eventOf(evs, "done") == nil {
		t.Fatalf("events = %+v", evs)
	}
	r1, err := e.st.Revision(ctx, rev.Revision)
	if err != nil || r1.FrontendID != "fe/dark" || r1.Author != "ben" || r1.Summary != "A dark page." || r1.ParentID != "" {
		t.Fatalf("r1 = %+v, %v", r1, err)
	}
	if b, err := os.ReadFile(filepath.Join(e.dir, "rev", r1.ID, "index.html")); err != nil || string(b) != fixtureIndex {
		t.Fatalf("files on disk: %v", err)
	}
	if len(r1.Files) != 1 || r1.Files[0].Path != "index.html" || r1.Files[0].Size != int64(len(fixtureIndex)) {
		t.Fatalf("manifest = %+v", r1.Files)
	}
	turns := builder.ParseConversation(r1.Conversation)
	if len(turns) != 2 || turns[0].Text != "make it dark" || turns[1].Text != "A dark page." {
		t.Fatalf("conversation = %+v", turns)
	}
	// the site content went to the model for reference
	if first, _ := json.Marshal(e.model.Requests[0].Messages[0]); !strings.Contains(string(first), "Home page copy goes here.") {
		t.Error("site content not in the first message")
	}
	runs, _ := e.st.Runs(ctx, "fe/dark", 5)
	if len(runs) != 1 || runs[0].Status != store.RunDone || runs[0].RevisionID != r1.ID {
		t.Fatalf("runs = %+v", runs)
	}

	// reprompt from r1: continues its conversation and files
	e.model.Requests = nil
	e.model.Responses = []string{
		builder.ToolUse("4", "str_replace", map[string]any{"path": "index.html", "old_str": "<main id=m>", "new_str": "<main id=m class=big>"}),
		builder.ToolUse("5", "finish", map[string]any{"summary": "Bigger."}),
		builder.ToolUse("6", "finish", map[string]any{"summary": "Bigger."}),
	}
	evs = e.chat(t, "dark", "bigger", r1.ID)
	rev = eventOf(evs, "revision")
	if rev == nil || rev.Number != 2 {
		t.Fatalf("reprompt events = %+v", evs)
	}
	r2, _ := e.st.Revision(ctx, rev.Revision)
	if r2.ParentID != r1.ID || len(builder.ParseConversation(r2.Conversation)) != 4 {
		t.Fatalf("r2 = %+v", r2)
	}
	if msgs := e.model.Requests[0].Messages; len(msgs) != 3 {
		t.Fatalf("reprompt history: %d messages", len(msgs))
	}
	// r1 is untouched
	if b, _ := os.ReadFile(filepath.Join(e.dir, "rev", r1.ID, "index.html")); strings.Contains(string(b), "class=big") {
		t.Fatal("parent revision modified")
	}

	// no active revision yet: can't join the rotation, isn't served
	if rec := e.form("/admin/builder/rotation", url.Values{"id": {"fe/dark"}, "in_rotation": {"1"}}); rec.Code != http.StatusConflict {
		t.Fatalf("rotation without active: %d", rec.Code)
	}

	// publish r2, add to rotation: visitors can get it, served as rev/<r2>
	if rec := e.form("/admin/builder/fe/dark/activate", url.Values{"rev": {r2.ID}}); rec.Code != 303 {
		t.Fatalf("activate: %d", rec.Code)
	}
	if rec := e.form("/admin/builder/rotation", url.Values{"id": {"fe/dark"}, "in_rotation": {"1"}, "back": {"/admin/builder/fe/dark"}}); rec.Code != 303 || rec.Header().Get("Location") != "/admin/builder/fe/dark" {
		t.Fatalf("rotation: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	fe := decodeFrontend(t, get(e.s, "/api/frontend", &http.Cookie{Name: pickCookie, Value: "fe/dark"}))
	if fe.Ref != "fe/dark" || fe.Serve != "rev/"+r2.ID {
		t.Fatalf("api/frontend = %+v", fe)
	}
	tok := strings.TrimSuffix(strings.TrimPrefix(fe.URL, "http://127.0.0.1:8081/t/"), "/")
	if c, err := fetoken.Verify(testKey, tok); err != nil || c.Ref != "rev/"+r2.ID {
		t.Fatalf("token claims = %+v, %v", c, err)
	}
	// random picks include it: rotation is builtin/site, builtin/particle-stream, fe/dark
	e.s.intn = func(n int) int { return n - 1 }
	if fe := decodeFrontend(t, get(e.s, "/api/frontend")); fe.Ref != "fe/dark" || fe.Serve != "rev/"+r2.ID {
		t.Fatalf("random pick = %+v", fe)
	}

	// rollback to r1
	e.form("/admin/builder/fe/dark/activate", url.Values{"rev": {r1.ID}})
	if fe := decodeFrontend(t, get(e.s, "/api/frontend", &http.Cookie{Name: pickCookie, Value: "fe/dark"})); fe.Serve != "rev/"+r1.ID {
		t.Fatalf("after rollback serve = %q", fe.Serve)
	}
	// another front end's revision can't be activated
	e.form("/admin/builder/new", url.Values{"slug": {"other"}})
	if rec := e.form("/admin/builder/fe/other/activate", url.Values{"rev": {r1.ID}}); rec.Code != 404 {
		t.Fatalf("foreign activate: %d", rec.Code)
	}

	// pages render
	for _, p := range []string{"/admin/builder/", "/admin/builder/fe/dark", "/admin/builder/fe/dark?rev=" + r1.ID, "/admin/builder/builtin/site"} {
		rec := e.admin("GET", p, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", p, rec.Code, rec.Body)
		}
		if p == "/admin/builder/fe/dark" && (!strings.Contains(rec.Body.String(), `data-ref="rev/`+r1.ID+`"`) || !strings.Contains(rec.Body.String(), "Bigger.")) {
			t.Errorf("front-end page doesn't preview the active revision / show history")
		}
		if p == "/admin/builder/" && !strings.Contains(rec.Body.String(), "fe/dark") {
			t.Error("index doesn't list fe/dark")
		}
	}
	if rec := e.admin("GET", "/admin/builder/fe/nope", nil); rec.Code != 404 {
		t.Errorf("unknown front end page: %d", rec.Code)
	}

	// preview URLs and source view
	rec := e.admin("GET", "/admin/builder/preview?ref=rev/"+r2.ID, nil)
	var pv struct{ Ref, URL string }
	json.NewDecoder(rec.Body).Decode(&pv)
	if rec.Code != 200 || pv.Ref != "rev/"+r2.ID || !strings.HasPrefix(pv.URL, "http://127.0.0.1:8081/t/") {
		t.Fatalf("preview: %d %+v", rec.Code, pv)
	}
	for _, bad := range []string{"fe/dark", "rev/zzzzzzzz9", "../x"} {
		if rec := e.admin("GET", "/admin/builder/preview?ref="+url.QueryEscape(bad), nil); rec.Code == 200 {
			t.Errorf("preview of %q allowed", bad)
		}
	}
	if rec := e.admin("GET", "/admin/builder/rev/"+r2.ID+"/index.html", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "class=big") || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("source: %d", rec.Code)
	}
}

func TestBuilderChatFailuresAndGuards(t *testing.T) {
	e := newBuilderServer(t, true)
	e.form("/admin/builder/new", url.Values{"slug": {"x"}})

	// a refusal is reported and recorded; no revision
	e.model.Responses = []string{`{"id":"m","type":"message","role":"assistant","model":"x","stop_reason":"refusal","content":[]}`}
	evs := e.chat(t, "x", "something", "")
	if ev := eventOf(evs, "error"); ev == nil || !strings.Contains(ev.Text, "declined") || eventOf(evs, "revision") != nil {
		t.Fatalf("refusal events = %+v", evs)
	}
	runs, _ := e.st.Runs(context.Background(), "fe/x", 5)
	if len(runs) != 1 || runs[0].Status != store.RunFailed {
		t.Fatalf("runs = %+v", runs)
	}
	if revs, _ := e.st.Revisions(context.Background(), "fe/x"); len(revs) != 0 {
		t.Fatal("revision saved on refusal")
	}

	// bad requests
	for _, body := range []string{``, `{}`, `{"prompt":"  "}`, `{"prompt":"x","parent":"zzzzzzzz9"}`} {
		rec := e.admin("POST", "/admin/builder/fe/x/chat", strings.NewReader(body), "Content-Type", "application/json")
		if rec.Code != 400 {
			t.Errorf("chat %q: %d", body, rec.Code)
		}
	}
	if rec := e.admin("POST", "/admin/builder/fe/nope/chat", strings.NewReader(`{"prompt":"x"}`)); rec.Code != 404 {
		t.Errorf("chat unknown front end: %d", rec.Code)
	}
	// cross-site POSTs are refused (basic auth rides along cross-site)
	rec := e.admin("POST", "/admin/builder/fe/x/chat", strings.NewReader(`{"prompt":"x"}`), "Sec-Fetch-Site", "cross-site")
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site chat: %d", rec.Code)
	}
	// and without auth
	req := httptest.NewRequest("GET", "/admin/builder/", nil)
	rec = httptest.NewRecorder()
	e.s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("builder without auth: %d", rec.Code)
	}
}

func TestBuilderDisabledWithoutKey(t *testing.T) {
	e := newBuilderServer(t, false)
	e.form("/admin/builder/new", url.Values{"slug": {"x"}})
	rec := e.admin("POST", "/admin/builder/fe/x/chat", strings.NewReader(`{"prompt":"x"}`), "Content-Type", "application/json")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "ANTHROPIC_API_KEY") {
		t.Fatalf("chat without key: %d %s", rec.Code, rec.Body)
	}
	for _, p := range []string{"/admin/builder/", "/admin/builder/fe/x"} {
		if rec := e.admin("GET", p, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "ANTHROPIC_API_KEY is not set") {
			t.Errorf("%s: %d, no disabled notice", p, rec.Code)
		}
	}
	// import still works without the model, and publishing too
	rec = e.admin("POST", "/admin/builder/fe/x/import", strings.NewReader(`{"files":{"index.html":`+jsonString(fixtureIndex)+`},"summary":"by hand"}`))
	var out struct {
		ID     string
		Number int
		Ref    string
	}
	json.NewDecoder(rec.Body).Decode(&out)
	if rec.Code != 200 || out.Number != 1 || out.Ref != "rev/"+out.ID {
		t.Fatalf("import: %d %+v", rec.Code, out)
	}
	for _, bad := range []string{
		`{"files":{"index.html":"<p>no host script</p>"}}`,
		`{"files":{"../index.html":"x"}}`,
		`{"files":{}}`,
		`not json`,
	} {
		if rec := e.admin("POST", "/admin/builder/fe/x/import", strings.NewReader(bad)); rec.Code != 400 {
			t.Errorf("import %q: %d", bad, rec.Code)
		}
	}
	if rec := e.form("/admin/builder/fe/x/activate", url.Values{"rev": {out.ID}}); rec.Code != 303 {
		t.Fatalf("activate: %d", rec.Code)
	}
}

func TestRotationOverrideWithPromptedFrontEnd(t *testing.T) {
	e := newBuilderServer(t, false)
	ctx := context.Background()
	e.st.CreatePromptedFrontend(ctx, "fe/p", "P")
	// FRONTEND_ROTATION may name fe/* IDs; without an active revision they're skipped
	e.s.rotationOverride = frontend.Rotation{"fe/p"}
	if fe := decodeFrontend(t, get(e.s, "/api/frontend")); fe.Ref != frontend.DefaultRef || fe.Serve != frontend.DefaultRef {
		t.Fatalf("unservable override = %+v", fe)
	}
	rev, _ := e.st.AddRevision(ctx, store.Revision{ID: "pppppppp1", FrontendID: "fe/p", Author: "ben"})
	e.st.SetActiveRevision(ctx, "fe/p", rev.ID)
	if fe := decodeFrontend(t, get(e.s, "/api/frontend")); fe.Ref != "fe/p" || fe.Serve != "rev/pppppppp1" {
		t.Fatalf("override = %+v", fe)
	}
	// fallback is always the default
	if fe := decodeFrontend(t, get(e.s, "/api/frontend?fallback=1")); fe.Ref != frontend.DefaultRef || fe.Serve != frontend.DefaultRef {
		t.Fatalf("fallback = %+v", fe)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

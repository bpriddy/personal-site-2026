package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/media"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

const importJSON = `{
  "source": "test",
  "projects": [
    {"slug": "qledecode", "title": "QLEDecode", "client": "Samsung", "agency": "Adam & Eve", "year": "2020",
     "tags": ["Experiential", "ARG"], "roles": ["Creative Director", "Tech Director"],
     "summary": "To celebrate the launch.\n\nSecond paragraph.", "contribution": "I led it.", "body": "",
     "link": "https://example.com/q", "palette": ["#001d48", "#012c6c"], "youtube": "e6PKBbvRYV0",
     "media": [
       {"kind": "image", "src": "/media/projects/qledecode/hero.jpg", "width": 735, "height": 413},
       {"kind": "loop", "src": "/media/projects/qledecode/loop-1.mp4", "poster": "/media/projects/qledecode/loop-1.jpg", "width": 600, "height": 200}
     ],
     "order": 0, "published": true},
    {"slug": "second", "title": "Second", "client": "Nike", "year": "2021", "order": 1, "published": true,
     "media": [{"kind": "image", "src": "/media/projects/second/hero.jpg", "width": 10, "height": 10}]},
    {"slug": "draft", "title": "Draft Project", "order": 107, "published": false}
  ],
  "experiments": [
    {"slug": "particle-stream", "title": "IGNORED", "summary": "IGNORED", "link": "https://codepen.io/x",
     "media": [{"kind": "loop", "src": "/media/projects/ps/loop-1.mp4", "poster": "/media/projects/ps/loop-1.jpg", "width": 800, "height": 100}]},
    {"slug": "tracer", "title": "Tracer", "summary": "this is an experiment.", "link": "https://benpriddy.com/prototypes/tracer/", "media": []}
  ]
}`

// newWorkServer: the memory store, a media dir with one file, and admin auth.
func newWorkServer(t *testing.T) (*Server, *store.Memory) {
	t.Helper()
	t.Setenv(frontend.RotationEnv, "")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "projects", "qledecode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "projects", "qledecode", "hero.jpg"), []byte("jpeg bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	cfg := config.Config{
		Env: "dev", AdminUser: "admin", AdminPassword: "pw", SigningKey: testKey,
		MainOrigin: "http://localhost:8080", UsercontentOrigin: "http://127.0.0.1:8081", MediaDir: dir,
	}
	s, err := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func workAdmin(s *Server, method, target, ctype, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.SetBasicAuth("admin", "pw")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func importProjects(t *testing.T, s *Server, body string) map[string]map[string][]string {
	t.Helper()
	rec := workAdmin(s, "POST", "/admin/import/projects", "application/json", body, nil)
	if rec.Code != 200 {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	var res map[string]map[string][]string
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestImportProjectsIdempotent(t *testing.T) {
	s, st := newWorkServer(t)
	ctx := context.Background()
	// the bio must survive any import
	home := content.Page{Slug: "", Title: "Ben Priddy", Body: "My current bio.", Published: true}
	if err := st.SavePage(ctx, home); err != nil {
		t.Fatal(err)
	}
	before, _ := st.Page(ctx, "")

	res := importProjects(t, s, importJSON)
	if got := res["projects"]["created"]; !reflect.DeepEqual(got, []string{"qledecode", "second", "draft"}) {
		t.Errorf("created %v", got)
	}
	if got := res["experiments"]; !reflect.DeepEqual(got["updated"], []string{"particle-stream"}) || !reflect.DeepEqual(got["created"], []string{"tracer"}) {
		t.Errorf("experiments %v", got)
	}
	q, err := st.Project(ctx, "qledecode")
	if err != nil || q.Client != "Samsung" || len(q.Media) != 2 || q.Media[1].Poster != "/media/projects/qledecode/loop-1.jpg" || !q.Published || q.YouTube != "e6PKBbvRYV0" {
		t.Fatalf("qledecode = %+v, %v", q, err)
	}
	ps, _ := st.Experiment(ctx, "particle-stream")
	if ps.Title != "Particle Stream" || ps.Link != "https://codepen.io/x" || len(ps.Media) != 1 {
		t.Errorf("existing experiment: only link and media change: %+v", ps)
	}
	tr, _ := st.Experiment(ctx, "tracer")
	if tr.Title != "Tracer" || !tr.Published || tr.Order <= ps.Order {
		t.Errorf("new experiment: %+v", tr)
	}

	// again: nothing changes, not even updated_at
	stamp := q.UpdatedAt
	res = importProjects(t, s, importJSON)
	if len(res["projects"]["created"])+len(res["projects"]["updated"])+len(res["experiments"]["created"])+len(res["experiments"]["updated"]) != 0 ||
		len(res["projects"]["unchanged"]) != 3 || len(res["experiments"]["unchanged"]) != 2 {
		t.Errorf("second import changed things: %v", res)
	}
	if q2, _ := st.Project(ctx, "qledecode"); !q2.UpdatedAt.Equal(stamp) {
		t.Error("second import rewrote an unchanged project")
	}
	if after, _ := st.Page(ctx, ""); after != before {
		t.Errorf("import touched the home page: %+v", after)
	}
	if pages, _ := st.Pages(ctx); len(pages) != 1 {
		t.Errorf("import created pages: %+v", pages)
	}

	// a changed field updates just that project
	res = importProjects(t, s, strings.Replace(importJSON, `"title": "Second"`, `"title": "Second, renamed"`, 1))
	if !reflect.DeepEqual(res["projects"]["updated"], []string{"second"}) {
		t.Errorf("rename: %v", res)
	}
}

func TestImportProjectsRejects(t *testing.T) {
	s, st := newWorkServer(t)
	for name, body := range map[string]string{
		"hotlinked media": `{"projects":[{"slug":"a","media":[{"kind":"loop","src":"https://evil.example/x.mp4"}]}]}`,
		"traversal":       `{"projects":[{"slug":"a","media":[{"kind":"image","src":"/media/../secrets.jpg"}]}]}`,
		"bad link":        `{"projects":[{"slug":"ok"},{"slug":"a","link":"javascript:alert(1)"}]}`,
		"bad slug":        `{"projects":[{"slug":"Bad Slug"}]}`,
		"bad youtube":     `{"projects":[{"slug":"a","youtube":"https://youtu.be/x"}]}`,
		"bad palette":     `{"projects":[{"slug":"a","palette":["red"]}]}`,
		"duplicate":       `{"projects":[{"slug":"a"},{"slug":"a"}]}`,
		"bad experiment":  `{"experiments":[{"slug":"x","link":"data:text/html,hi"}]}`,
	} {
		rec := workAdmin(s, "POST", "/admin/import/projects", "application/json", body, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "problems") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if all, _ := st.Projects(context.Background()); len(all) != 0 {
		t.Errorf("a rejected import wrote %d projects", len(all))
	}
	if rec := workAdmin(s, "POST", "/admin/import/projects", "application/json", "{", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad JSON: %d", rec.Code)
	}
	if rec := workAdmin(s, "POST", "/admin/import/projects", "text/plain", importJSON, nil); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d", rec.Code)
	}
	// cross-site (basic-auth credentials ride along) and unauthenticated
	if rec := workAdmin(s, "POST", "/admin/import/projects", "application/json", importJSON, map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site: %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/admin/import/projects", strings.NewReader(importJSON))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no auth: %d", rec.Code)
	}
}

func TestWorkTranscript(t *testing.T) {
	s, _ := newWorkServer(t)
	importProjects(t, s, importJSON)

	rec := get(s, "/work/")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "QLEDecode") || !strings.Contains(body, "Second") || strings.Contains(body, "Draft Project") {
		t.Fatalf("/work: %d %s", rec.Code, body)
	}
	for _, want := range []string{
		`href="/work/qledecode"`, `Samsung · 2020`, `<span class="idx" aria-hidden="true">01</span>`,
		`<video src="/media/projects/qledecode/loop-1.mp4" poster="/media/projects/qledecode/loop-1.jpg" width="600" height="200" muted loop playsinline preload="none" data-loop="hover"`,
		`<img src="/media/projects/second/hero.jpg" width="10" height="10" alt="" loading="lazy"`,
		`( 02 projects )`, `<a href="/work/" aria-current="page"><span class="idx">02</span> Work</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/work: missing %s", want)
		}
	}
	if strings.Contains(body, " autoplay") {
		t.Error("/work: loops must not autoplay in the transcript (media.js plays them)")
	}
	if rec := get(s, "/work"); rec.Code != 200 {
		t.Errorf("/work without slash: %d", rec.Code)
	}

	rec = get(s, "/work/qledecode")
	body = rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("/work/qledecode: %d", rec.Code)
	}
	for _, want := range []string{
		`<h1 class="display" id="project-h">QLEDecode</h1>`, `( 01 / 02 )`,
		`<dd>Samsung</dd>`, `<dd>Adam &amp; Eve</dd>`, `<dd>2020</dd>`, `<dd>Creative Director, Tech Director</dd>`, `<dd>Experiential, ARG</dd>`,
		`<rect x="0" width="2" height="2" fill="#001d48"/>`,
		`<img src="/media/projects/qledecode/hero.jpg" width="735" height="413" alt="QLEDecode"`,
		`<p>To celebrate the launch.</p>`, `<p>Second paragraph.</p>`, `<p>I led it.</p>`,
		`<a href="https://example.com/q" target="_blank" rel="noopener noreferrer">( Visit the work )</a>`,
		`<a href="https://www.youtube.com/watch?v=e6PKBbvRYV0" target="_blank" rel="noopener noreferrer">`,
		`<div class="film" data-embed="https://www.youtube-nocookie.com/embed/e6PKBbvRYV0?rel=0"`, `( Play the film )`,
		`data-loop aria-label="QLEDecode, loop 1"`,
		`<a class="next-title" href="/work/second">Second</a>`,
		`<title>QLEDecode · Ben Priddy</title>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/work/qledecode: missing %s", want)
		}
	}
	if !strings.Contains(body, `<a href="/work/" aria-current="page">`) {
		t.Error("/work/qledecode: Work isn't the current nav item")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"media-src 'self'", "frame-src http://127.0.0.1:8081 https://www.youtube-nocookie.com", "img-src 'self' data:", "script-src 'self';"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(body, ` style="`) {
		t.Error("transcript needs inline styles; the CSP forbids them")
	}

	for _, p := range []string{"/work/draft", "/work/nope", "/work/qledecode/extra"} {
		if rec := get(s, p); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Not found") {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}

	// home: selected work after the bio; experiments show their loops and links
	body = get(s, "/").Body.String()
	for _, want := range []string{"( Selected work", `href="/work/qledecode"`, `( All work )`,
		`<a href="https://codepen.io/x" target="_blank" rel="noopener noreferrer">Particle Stream</a>`,
		`<video src="/media/projects/ps/loop-1.mp4" poster="/media/projects/ps/loop-1.jpg" width="800" height="100" muted loop playsinline preload="none" data-loop`} {
		if !strings.Contains(body, want) {
			t.Errorf("home: missing %s", want)
		}
	}
	if strings.Index(body, "( About )") > strings.Index(body, "( Selected work") {
		t.Error("selected work should follow the bio")
	}
	if body := get(s, "/experiments/").Body.String(); !strings.Contains(body, "/media/projects/ps/loop-1.mp4") {
		t.Error("experiments page doesn't show experiment media")
	}

	// the contract has the projects too
	var site struct {
		Projects []map[string]any `json:"projects"`
	}
	_ = json.Unmarshal(get(s, "/api/site.json").Body.Bytes(), &site)
	if len(site.Projects) != 2 || site.Projects[0]["slug"] != "qledecode" {
		t.Errorf("site.json projects: %v", site.Projects)
	}
}

func TestWorkWithoutProjects(t *testing.T) {
	s := newTestServer(t)
	if rec := get(s, "/work/"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Nothing published yet.") {
		t.Errorf("/work empty: %d", rec.Code)
	}
	if body := get(s, "/").Body.String(); strings.Contains(body, "Selected work") || strings.Contains(body, `href="/work/"`) {
		t.Error("no projects: no Work in the nav or on home")
	}
}

func TestMainSiteMedia(t *testing.T) {
	s, _ := newWorkServer(t)
	rec := get(s, "/media/projects/qledecode/hero.jpg")
	if rec.Code != 200 || rec.Body.String() != "jpeg bytes" || rec.Header().Get("Content-Type") != "image/jpeg" ||
		!strings.Contains(rec.Header().Get("Cache-Control"), "immutable") || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("media: %d %v", rec.Code, rec.Header())
	}
	if rec := get(s, "/media/projects/qledecode/missing.jpg"); rec.Code != http.StatusNotFound {
		t.Errorf("missing media: %d", rec.Code)
	}
	if rec := get(s, "/media/projects/qledecode/.hidden.jpg"); rec.Code != http.StatusNotFound {
		t.Errorf("dotfile: %d", rec.Code)
	}
	req := httptest.NewRequest("HEAD", "/media/projects/qledecode/hero.jpg", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d", rec.Code)
	}

	// WithMedia swaps the source (the bucket in prod)
	t.Setenv(frontend.RotationEnv, "")
	s2, err := New(config.Config{Env: "dev", SigningKey: testKey, UsercontentOrigin: "http://127.0.0.1:8081"}, store.NewMemory(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), WithMedia(media.Dir{Root: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	if rec := get(s2, "/media/projects/qledecode/hero.jpg"); rec.Code != http.StatusNotFound {
		t.Errorf("WithMedia: %d", rec.Code)
	}
}

func TestAdminProjects(t *testing.T) {
	s, st := newWorkServer(t)
	importProjects(t, s, importJSON)
	ctx := context.Background()

	rec := workAdmin(s, "GET", "/admin/", "", "", nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `href="/admin/projects/qledecode"`) || !strings.Contains(body, `<img class="thumb" src="/media/projects/qledecode/hero.jpg"`) {
		t.Fatalf("dashboard: %d", rec.Code)
	}
	rec = workAdmin(s, "GET", "/admin/projects/qledecode", "", "", nil)
	if body := rec.Body.String(); rec.Code != 200 || !strings.Contains(body, `value="Experiential, ARG"`) || !strings.Contains(body, `/media/projects/qledecode/loop-1.mp4`) {
		t.Fatalf("form: %d", rec.Code)
	}

	form := url.Values{
		"title": {"QLEDecode 2"}, "client": {"Samsung"}, "agency": {""}, "year": {"2021"},
		"roles": {"Director, , Dev"}, "tags": {"A,B"}, "palette": {"#ffffff"},
		"summary": {"s"}, "contribution": {"c"}, "body": {"b"}, "link": {""}, "youtube": {""},
		"order": {"3"}, "published": {"on"},
	}
	rec = workAdmin(s, "POST", "/admin/projects/qledecode", "application/x-www-form-urlencoded", form.Encode(), nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	q, _ := st.Project(ctx, "qledecode")
	if q.Title != "QLEDecode 2" || q.Agency != "" || !reflect.DeepEqual(q.Roles, []string{"Director", "Dev"}) || q.Order != 3 || len(q.Media) != 2 {
		t.Errorf("after save: %+v (media must be kept)", q)
	}
	form.Set("link", "javascript:alert(1)")
	if rec := workAdmin(s, "POST", "/admin/projects/qledecode", "application/x-www-form-urlencoded", form.Encode(), nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad link saved: %d", rec.Code)
	}

	// publish toggle
	rec = workAdmin(s, "POST", "/admin/projects/qledecode/publish", "application/x-www-form-urlencoded", "published=0", nil)
	if q, _ := st.Project(ctx, "qledecode"); rec.Code != http.StatusSeeOther || q.Published {
		t.Errorf("unpublish: %d %v", rec.Code, q.Published)
	}
	if rec := get(s, "/work/qledecode"); rec.Code != http.StatusNotFound {
		t.Errorf("unpublished project served: %d", rec.Code)
	}

	// new project from the dashboard
	if rec := workAdmin(s, "GET", "/admin/projects/?slug=brand-new", "", "", nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/projects/brand-new" {
		t.Errorf("new: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := workAdmin(s, "GET", "/admin/projects/brand-new", "", "", nil); rec.Code != 200 {
		t.Errorf("new form: %d", rec.Code)
	}
	if rec := workAdmin(s, "GET", "/admin/projects/Bad%20Slug", "", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("bad slug form: %d", rec.Code)
	}
}

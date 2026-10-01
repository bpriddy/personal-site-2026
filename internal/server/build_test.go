package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// visitor is one browser: a sid cookie, an IP, and any other cookies it holds.
type visitor struct {
	e       *builderEnv
	sid     string
	ip      string
	cookies map[string]string
}

func (e *builderEnv) visitor(n int, ip string) *visitor {
	sid := strings.Repeat(string("0123456789abcdef"[n%16]), 32)
	return &visitor{e: e, sid: sid, ip: ip, cookies: map[string]string{}}
}

func (v *visitor) do(method, target string, body io.Reader, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	if v.sid != "" {
		req.AddCookie(&http.Cookie{Name: sidCookie, Value: v.sid})
	}
	for k, val := range v.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: val})
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.200, "+v.ip)
	if method == "POST" {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	v.e.s.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == sidCookie {
			continue
		}
		if c.MaxAge < 0 {
			delete(v.cookies, c.Name)
		} else {
			v.cookies[c.Name] = c.Value
		}
	}
	return rec
}

// post sends v as JSON.
func (v *visitor) post(target string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	return v.do("POST", target, strings.NewReader(string(b)), "Content-Type", "application/json")
}

// apiError is a refusal's friendly message.
func apiError(rec *httptest.ResponseRecorder) string {
	var e struct{ Error string }
	json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Error
}

// create makes a front end from a prompt and returns its slug.
func (v *visitor) create(t *testing.T, prompt string) string {
	t.Helper()
	rec := v.post("/build/api/new", map[string]string{"prompt": prompt})
	var out struct{ ID, Slug, Title string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 201 || out.ID != "fe/"+out.Slug || out.Slug == "" {
		t.Fatalf("new: %d %s", rec.Code, rec.Body)
	}
	return out.Slug
}

func (v *visitor) chatRaw(slug, prompt, parent string) *httptest.ResponseRecorder {
	return v.post("/build/api/fe/"+slug+"/chat", map[string]string{"prompt": prompt, "parent": parent})
}

// chat runs a chat turn with the scripted model making a valid revision.
func (v *visitor) chat(t *testing.T, slug, prompt, parent string) *builder.Event {
	t.Helper()
	v.e.script()
	rec := v.chatRaw(slug, prompt, parent)
	if rec.Code != 200 {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body)
	}
	evs := sseEvents(t, rec.Body.String())
	rev := eventOf(evs, "revision")
	if rev == nil {
		t.Fatalf("no revision: %+v", evs)
	}
	return rev
}

// live shows a revision on the site ("" = the newest).
func (v *visitor) live(slug, rev string) *httptest.ResponseRecorder {
	return v.post("/build/api/fe/"+slug+"/live", map[string]string{"rev": rev})
}

// state is GET /build/api/frontends.
func (v *visitor) state(t *testing.T) buildState {
	t.Helper()
	rec := v.do("GET", "/build/api/frontends", nil)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("frontends: %d %v", rec.Code, rec.Header())
	}
	var st buildState
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func (e *builderEnv) script() {
	e.model.Responses = []string{
		builder.ToolUse("1", "write_file", map[string]any{"path": "index.html", "content": fixtureIndex}),
		builder.ToolUse("2", "finish", map[string]any{"summary": "A page."}),
		builder.ToolUse("3", "finish", map[string]any{"summary": "A page."}),
	}
}

func adminDo(e *builderEnv, method, target string) *httptest.ResponseRecorder {
	return e.admin(method, target, nil, "Sec-Fetch-Site", "same-origin")
}

func TestPublicBuilderFlow(t *testing.T) {
	e := newBuilderServer(t, true)
	ctx := context.Background()
	a := e.visitor(1, "198.51.100.1")

	// the list: a session cookie, JSON, nothing yet
	req := httptest.NewRequest("GET", "/build/api/frontends", nil)
	rec := httptest.NewRecorder()
	e.s.ServeHTTP(rec, req)
	var sid *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sidCookie {
			sid = c
		}
	}
	if rec.Code != 200 || sid == nil || !sidPattern.MatchString(sid.Value) || !sid.HttpOnly || sid.SameSite != http.SameSiteLaxMode ||
		sid.MaxAge != 30*24*3600 || sid.Path != "/" || sid.Secure {
		t.Fatalf("/build/api/frontends: %d, sid %+v", rec.Code, sid)
	}
	if st := a.state(t); !st.Enabled || st.Notice != "" || st.MaxPrompt != 4000 || st.Live != nil || len(st.Frontends) != 0 {
		t.Fatalf("empty state = %+v", st)
	}
	if body := a.do("GET", "/build/api/frontends", nil).Body.String(); !strings.Contains(body, `"frontends":[]`) || !strings.Contains(body, `"live":null`) {
		t.Fatalf("empty state JSON: %s", body)
	}
	// plain page views get no session cookie, and the shell carries the modal
	home := get(e.s, "/")
	if len(home.Result().Cookies()) != 0 {
		t.Fatalf("/ set cookies: %v", home.Result().Cookies())
	}
	if b := home.Body.String(); !strings.Contains(b, `src="/static/build-modal.js"`) || !strings.Contains(b, `src="/static/builder-stream.js"`) ||
		!strings.Contains(b, `href="/static/build-modal.css"`) || !strings.Contains(b, `class="make-own-link" href="/?build=1"`) || strings.Contains(b, "<script>") {
		t.Fatalf("shell lacks the modal:\n%s", b)
	}

	// create → chat → revision
	slug := a.create(t, "A calm green page")
	if slug != "a-calm-green-page" {
		t.Fatalf("slug = %q", slug)
	}
	if st := a.state(t); len(st.Frontends) != 1 || st.Frontends[0].Title != "A calm green page" || len(st.Frontends[0].Revisions) != 0 ||
		st.Frontends[0].Status.Key != "draft" {
		t.Fatalf("state after create = %+v", st)
	}
	rev := a.chat(t, slug, "A calm green page", "")
	r1, err := e.st.Revision(ctx, rev.Revision)
	if err != nil || r1.Author != "visitor" || r1.FrontendID != "fe/"+slug {
		t.Fatalf("r1 = %+v, %v", r1, err)
	}
	if sys := e.model.Requests[0].System; len(sys) != 3 || sys[1].Text != builder.QualityBrief || sys[2].Text != builder.VisitorNote {
		t.Fatal("visitor run without the visitor note")
	}
	runs, _ := e.st.Runs(ctx, "fe/"+slug, 5)
	if len(runs) != 1 || runs[0].Status != store.RunDone {
		t.Fatalf("runs = %+v", runs)
	}

	// View live names the revision: fe_live = "<front end>:<revision>"
	rec = a.live(slug, r1.ID)
	var lv buildLive
	json.Unmarshal(rec.Body.Bytes(), &lv)
	if rec.Code != 200 || lv != (buildLive{Frontend: "fe/" + slug, Slug: slug, Revision: r1.ID, Number: 1}) || a.cookies[liveCookie] != "fe/"+slug+":"+r1.ID {
		t.Fatalf("live on: %d %s %v", rec.Code, rec.Body, a.cookies)
	}
	// a reprompt makes r2; the site keeps showing r1 until the visitor picks r2
	rev2 := a.chat(t, slug, "greener", r1.ID)
	fe := decodeFrontend(t, a.do("GET", "/api/frontend", nil))
	if fe.Ref != "fe/"+slug || fe.Serve != "rev/"+r1.ID || !fe.Draft || fe.Exit != "/build/api/exit" || fe.Revision != r1.ID || fe.Number != 1 || fe.Title != "A calm green page" {
		t.Fatalf("draft = %+v", fe)
	}
	if rec := a.live(slug, rev2.Revision); rec.Code != 200 {
		t.Fatalf("live r2: %d", rec.Code)
	}
	if fe := decodeFrontend(t, a.do("GET", "/api/frontend", nil)); fe.Serve != "rev/"+rev2.Revision || fe.Number != 2 {
		t.Fatalf("draft r2 = %+v", fe)
	}
	// and back to r1
	a.live(slug, r1.ID)
	if fe := decodeFrontend(t, a.do("GET", "/api/frontend", nil)); fe.Serve != "rev/"+r1.ID {
		t.Fatalf("back to r1 = %+v", fe)
	}
	// "" = the newest
	a.live(slug, "")
	if fe := decodeFrontend(t, a.do("GET", "/api/frontend", nil)); fe.Serve != "rev/"+rev2.Revision {
		t.Fatalf("newest = %+v", fe)
	}
	if fb := decodeFrontend(t, a.do("GET", "/api/frontend?fallback=1", nil)); fb.Draft || fb.Ref != frontend.DefaultRef {
		t.Fatalf("fallback = %+v", fb)
	}

	// the list: both revisions newest first, with prompts; what's live
	st := a.state(t)
	if len(st.Frontends) != 1 || st.Live == nil || st.Live.Revision != rev2.Revision || st.Live.Number != 2 || st.Live.Slug != slug {
		t.Fatalf("state = %+v", st)
	}
	f := st.Frontends[0]
	if f.ID != "fe/"+slug || f.Slug != slug || f.Running || len(f.Revisions) != 2 ||
		f.Revisions[0].ID != rev2.Revision || f.Revisions[0].Number != 2 || f.Revisions[0].ParentNumber != 1 || f.Revisions[0].Prompt != "greener" ||
		f.Revisions[1].ID != r1.ID || f.Revisions[1].Prompt != "A calm green page" || f.Revisions[1].Summary != "A page." {
		t.Fatalf("frontend row = %+v", f)
	}

	// exit
	rec = a.do("POST", "/build/api/exit", nil)
	if rec.Code != 204 || a.cookies[liveCookie] != "" {
		t.Fatalf("exit: %d %v", rec.Code, a.cookies)
	}
	if fe := decodeFrontend(t, a.do("GET", "/api/frontend", nil)); fe.Draft {
		t.Fatalf("still a draft after exit: %+v", fe)
	}
	if st := a.state(t); st.Live != nil {
		t.Fatalf("live after exit = %+v", st.Live)
	}

	// submit r1 → pending, shown to the visitor and on the admin badge
	rec = a.post("/build/api/fe/"+slug+"/submit", map[string]string{"rev": r1.ID})
	var sub struct {
		Status  visitorStatus
		Message string
	}
	json.Unmarshal(rec.Body.Bytes(), &sub)
	if rec.Code != 200 || sub.Status.Key != "pending" || sub.Status.Rev != r1.ID || sub.Status.RevN != 1 || sub.Message != buildMessage(msgSubmitted, e.s.limits) {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body)
	}
	if st := a.state(t); st.Frontends[0].Status.Label != "Waiting for review" {
		t.Fatalf("status = %+v", st.Frontends[0].Status)
	}
	rec = adminDo(e, "GET", "/admin/builder/pending.json")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"pending":1}` {
		t.Fatalf("pending.json: %d %s", rec.Code, rec.Body)
	}
	adminPage := adminDo(e, "GET", "/admin/builder/").Body.String()
	if !strings.Contains(adminPage, "A calm green page") || !strings.Contains(adminPage, "/approve") || !strings.Contains(adminPage, "Visitor front ends") {
		t.Fatalf("admin page lacks the submission:\n%s", adminPage)
	}
	if rec := adminDo(e, "GET", "/admin/builder/fe/"+slug+"?rev="+r1.ID); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Made by a visitor") ||
		!strings.Contains(rec.Body.String(), "/static/builder-stream.js") {
		t.Fatalf("admin preview page: %d", rec.Code)
	}

	// approve: active + in rotation; anyone whose pick it is gets r1
	subs, _ := e.st.Submissions(ctx, 10)
	if rec := adminDo(e, "POST", "/admin/builder/submissions/"+itoa64(subs[0].ID)+"/approve"); rec.Code != 303 {
		t.Fatalf("approve: %d", rec.Code)
	}
	if rec := adminDo(e, "POST", "/admin/builder/submissions/"+itoa64(subs[0].ID)+"/approve"); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Fatalf("approve twice: %d", rec.Code)
	}
	if rec := adminDo(e, "POST", "/admin/builder/submissions/999/reject"); rec.Code != 404 {
		t.Fatalf("reject unknown: %d", rec.Code)
	}
	stranger := e.visitor(9, "192.0.2.9")
	pick := decodeFrontend(t, stranger.do("GET", "/api/frontend", nil, "Cookie", pickCookie+"=fe/"+slug))
	if pick.Ref != "fe/"+slug || pick.Serve != "rev/"+r1.ID || pick.Draft {
		t.Fatalf("approved pick = %+v", pick)
	}
	if st := a.state(t); st.Frontends[0].Status.Key != "approved" || st.Frontends[0].Status.Label != "Approved" {
		t.Fatalf("visitor doesn't see the approval: %+v", st.Frontends[0].Status)
	}
	if rec := adminDo(e, "GET", "/admin/builder/pending.json"); strings.TrimSpace(rec.Body.String()) != `{"pending":0}` {
		t.Fatalf("pending after approve: %s", rec.Body)
	}
}

func itoa64(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestPublicBuilderRedirects(t *testing.T) {
	e := newBuilderServer(t, true)
	a := e.visitor(1, "198.51.100.1")
	slug := a.create(t, "Mine")
	// the old pages open the modal on the site, for anyone: no ownership leaks
	for _, who := range []*visitor{a, e.visitor(2, "198.51.100.2")} {
		for _, p := range []string{"/build", "/build/", "/build/" + slug, "/build/nope", "/build?msg=ip-day"} {
			rec := who.do("GET", p, nil)
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?build=1" {
				t.Errorf("%s: %d %q", p, rec.Code, rec.Header().Get("Location"))
			}
		}
	}
	// the old POST routes are gone
	for _, p := range []string{"/build/new", "/build/" + slug + "/chat", "/build/" + slug + "/live", "/build/" + slug + "/submit"} {
		if rec := a.post(p, map[string]string{}); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
			t.Errorf("POST %s: %d", p, rec.Code)
		}
	}
	// so is the visitor preview
	if rec := a.do("GET", "/build/preview?ref=builtin/site", nil); rec.Code != http.StatusFound {
		t.Errorf("/build/preview: %d", rec.Code)
	}
	// the home page with ?build=1 is just the home page (the modal opens client-side)
	if rec := get(e.s, "/?build=1"); rec.Code != 200 {
		t.Fatalf("/?build=1: %d", rec.Code)
	}
}

func TestPublicBuilderOwnershipIsolation(t *testing.T) {
	e := newBuilderServer(t, true)
	ctx := context.Background()
	a, b := e.visitor(1, "198.51.100.1"), e.visitor(2, "198.51.100.2")
	slug := a.create(t, "Secret garden")
	rev := a.chat(t, slug, "Secret garden", "")
	e.st.CreatePromptedFrontend(ctx, "fe/bens", "Ben's")
	bensRev, _ := e.st.AddRevision(ctx, store.Revision{ID: "bbbbbbbb1", FrontendID: "fe/bens", Author: "ben"})

	nobody := &visitor{e: e, sid: "", ip: "198.51.100.3", cookies: map[string]string{}}
	for _, who := range []*visitor{b, nobody} {
		for _, c := range []struct {
			target string
			body   map[string]string
		}{
			{"/build/api/fe/" + slug + "/live", map[string]string{"rev": rev.Revision}},
			{"/build/api/fe/" + slug + "/live", map[string]string{"rev": ""}},
			{"/build/api/fe/" + slug + "/submit", map[string]string{"rev": rev.Revision}},
			{"/build/api/fe/" + slug + "/chat", map[string]string{"prompt": "take it over"}},
			{"/build/api/fe/bens/live", map[string]string{"rev": bensRev.ID}},
			{"/build/api/fe/bens/submit", map[string]string{"rev": bensRev.ID}},
			{"/build/api/fe/bens/chat", map[string]string{"prompt": "take it over"}},
			{"/build/api/fe/nope/live", map[string]string{"rev": ""}},
			{"/build/api/fe/Bad_Slug/live", map[string]string{"rev": ""}},
		} {
			rec := who.post(c.target, c.body)
			if rec.Code != 404 {
				t.Errorf("%q: POST %s = %d, want 404", who.sid, c.target, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "Secret garden") {
				t.Errorf("POST %s leaks the title", c.target)
			}
		}
		if body := who.do("GET", "/build/api/frontends", nil).Body.String(); strings.Contains(body, "Secret garden") || strings.Contains(body, rev.Revision) {
			t.Error("another session lists the front end")
		}
		// a forged fe_live is ignored and cleared
		for _, forged := range []string{"fe/" + slug + ":" + rev.Revision, "fe/" + slug} {
			who.cookies[liveCookie] = forged
			if fe := decodeFrontend(t, who.do("GET", "/api/frontend", nil)); fe.Draft || fe.Ref == "fe/"+slug {
				t.Fatalf("non-owner fe_live %q served the draft: %+v", forged, fe)
			}
			if _, ok := who.cookies[liveCookie]; ok {
				t.Errorf("non-owner fe_live %q not cleared", forged)
			}
		}
		if st := who.state(t); st.Live != nil {
			t.Errorf("non-owner live = %+v", st.Live)
		}
	}
	// nothing was submitted or changed by the attempts
	if _, err := e.st.LatestSubmission(ctx, "fe/"+slug); err == nil {
		t.Fatal("a non-owner submitted")
	}
	if revs, _ := e.st.Revisions(ctx, "fe/"+slug); len(revs) != 1 {
		t.Fatalf("revisions = %d", len(revs))
	}
	if st := a.state(t); len(st.Frontends) != 1 || st.Frontends[0].Title != "Secret garden" {
		t.Fatalf("owner lost access: %+v", st)
	}
	// can't submit or show a revision of another front end through one's own
	other := a.create(t, "Second one")
	for _, r := range []string{rev.Revision, bensRev.ID, "zzzzzzzzzz"} {
		if rec := a.post("/build/api/fe/"+other+"/submit", map[string]string{"rev": r}); rec.Code != 404 {
			t.Errorf("submit foreign revision %s: %d", r, rec.Code)
		}
		if rec := a.live(other, r); rec.Code != 404 || a.cookies[liveCookie] != "" {
			t.Errorf("live foreign revision %s: %d %v", r, rec.Code, a.cookies)
		}
	}
}

func TestPublicBuilderFeLive(t *testing.T) {
	e := newBuilderServer(t, true)
	ctx := context.Background()
	a := e.visitor(1, "198.51.100.1")
	slug := a.create(t, "Empty for now")
	// no revision yet: View live refuses, kindly
	if rec := a.live(slug, ""); rec.Code != 409 || apiError(rec) != buildMessage(msgNoRevision, e.s.limits) || a.cookies[liveCookie] != "" {
		t.Fatalf("live without revision: %d %s", rec.Code, rec.Body)
	}
	r1 := a.chat(t, slug, "now something", "")
	r2 := a.chat(t, slug, "and more", r1.Revision)
	other := a.create(t, "Other one")
	r3 := a.chat(t, other, "other", "")
	e.st.CreatePromptedFrontend(ctx, "fe/bens", "Ben's")
	bensRev, _ := e.st.AddRevision(ctx, store.Revision{ID: "bbbbbbbb1", FrontendID: "fe/bens", Author: "ben"})

	// owner + a revision of that front end: served exactly
	for _, c := range []struct{ cookie, serve string }{
		{"fe/" + slug + ":" + r1.Revision, r1.Revision},
		{"fe/" + slug + ":" + r2.Revision, r2.Revision},
		{"fe/" + other + ":" + r3.Revision, r3.Revision},
		{"fe/" + slug, r2.Revision}, // legacy value: the newest
	} {
		a.cookies[liveCookie] = c.cookie
		fe := decodeFrontend(t, a.do("GET", "/api/frontend", nil))
		if !fe.Draft || fe.Serve != "rev/"+c.serve || fe.Revision != c.serve {
			t.Errorf("fe_live=%q: %+v", c.cookie, fe)
		}
		if a.cookies[liveCookie] != c.cookie {
			t.Errorf("fe_live=%q changed to %q", c.cookie, a.cookies[liveCookie])
		}
	}
	// anything else is ignored and cleared: a revision of another front end
	// (the visitor's own, or Ben's), a missing revision, other front ends,
	// garbage
	for _, v := range []string{
		"fe/" + slug + ":" + r3.Revision, "fe/" + other + ":" + r1.Revision, "fe/" + slug + ":" + bensRev.ID,
		"fe/" + slug + ":zzzzzzzzzz", "fe/" + slug + ":BAD!", "fe/" + slug + ":", "fe/bens:" + bensRev.ID, "fe/bens",
		"builtin/site", "builtin/site:" + r1.Revision, "garbage", "fe/nope", ":" + r1.Revision, "",
	} {
		a.cookies[liveCookie] = v
		fe := decodeFrontend(t, a.do("GET", "/api/frontend", nil))
		if fe.Draft || fe.Revision != "" {
			t.Errorf("fe_live=%q served a draft: %+v", v, fe)
		}
		if _, ok := a.cookies[liveCookie]; ok {
			t.Errorf("fe_live=%q not cleared", v)
		}
	}
	// without the sid, nobody gets it
	rec := get(e.s, "/api/frontend", &http.Cookie{Name: liveCookie, Value: "fe/" + slug + ":" + r1.Revision})
	if fe := decodeFrontend(t, rec); fe.Draft {
		t.Fatalf("no sid got the draft: %+v", fe)
	}
	// the live cookie is HttpOnly, Lax, a session cookie; exit clears it the same way
	rec = a.live(slug, r1.Revision)
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == liveCookie {
			found = true
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 0 || c.Path != "/" || c.Value != "fe/"+slug+":"+r1.Revision {
				t.Fatalf("fe_live cookie = %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("no fe_live cookie")
	}
	rec = a.do("POST", "/build/api/exit", nil)
	for _, c := range rec.Result().Cookies() {
		if c.Name == liveCookie && (c.MaxAge >= 0 || !c.HttpOnly || c.Path != "/") {
			t.Fatalf("exit cookie = %+v", c)
		}
	}
	// exit needs no session and is always allowed (it only clears a cookie)
	if rec := get(e.s, "/build/api/exit"); rec.Code == 204 {
		t.Fatal("GET exit")
	}
	// bad live bodies are a 404, not an error page
	if rec := a.do("POST", "/build/api/fe/"+slug+"/live", strings.NewReader("{"), "Content-Type", "application/json"); rec.Code != 404 {
		t.Fatalf("bad body: %d", rec.Code)
	}
}

func TestPublicBuilderLimits(t *testing.T) {
	e := newBuilderServer(t, true)
	ctx := context.Background()
	e.s.limits = BuildLimits{RunsPerSessionHour: 2, RunsPerIPDay: 3, RunsGlobalDay: 4, MaxPrompt: 50, ConcurrentRuns: 1, XFFHops: 1,
		NewPerIPWindow: 100, NewWindow: time.Minute}
	msg := func(key string) string { return buildMessage(key, e.s.limits) }
	refused := func(rec *httptest.ResponseRecorder, status int, key string) {
		t.Helper()
		if rec.Code != status || strings.TrimSpace(rec.Body.String()) != msg(key) {
			t.Fatalf("got %d %q, want %d %q", rec.Code, rec.Body, status, msg(key))
		}
	}
	refusedJSON := func(rec *httptest.ResponseRecorder, status int, key string) {
		t.Helper()
		if rec.Code != status || apiError(rec) != msg(key) || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("got %d %s, want %d %q", rec.Code, rec.Body, status, msg(key))
		}
	}

	a := e.visitor(1, "198.51.100.1")
	slug := a.create(t, "one")
	if st := a.state(t); st.MaxPrompt != 50 {
		t.Fatalf("maxPrompt = %d", st.MaxPrompt)
	}
	// prompt length (characters, not bytes)
	refused(a.chatRaw(slug, strings.Repeat("é", 51), ""), 400, msgTooLong)
	a.chat(t, slug, strings.Repeat("é", 50), "")
	refusedJSON(a.post("/build/api/new", map[string]string{"prompt": strings.Repeat("x", 51)}), 400, msgTooLong)
	refusedJSON(a.post("/build/api/new", map[string]string{"prompt": "  "}), 400, msgEmpty)
	refusedJSON(a.do("POST", "/build/api/new", strings.NewReader("prompt=x"), "Content-Type", "application/x-www-form-urlencoded"), 400, msgEmpty)

	// one at a time per session (a run in progress, e.g. on another instance)
	ipReq := httptest.NewRequest("GET", "/", nil)
	ipReq.Header.Set("X-Forwarded-For", a.ip)
	runID, err := e.st.StartVisitorRun(ctx, store.Run{FrontendID: "fe/" + slug, Prompt: "x"}, hashSID(a.sid), e.s.clientIPHash(ipReq), store.RunQuota{})
	if err != nil {
		t.Fatal(err)
	}
	refused(a.chatRaw(slug, "two", ""), 429, store.LimitConcurrent)
	e.st.FinishRun(ctx, runID, "", "x")

	// 2 per session per hour (the finished run above counts)
	refused(a.chatRaw(slug, "three", ""), 429, store.LimitSessionHour)

	// 3 per IP per day, across sessions
	a2 := e.visitor(2, "198.51.100.1")
	slug2 := a2.create(t, "two")
	a2.chat(t, slug2, "x", "")
	refused(a2.chatRaw(slug2, "y", ""), 429, store.LimitIPDay)
	// creating says so up front, and so does the list
	refusedJSON(a2.post("/build/api/new", map[string]string{"prompt": "more"}), 429, store.LimitIPDay)
	if st := a2.state(t); !strings.Contains(st.Notice, "plenty of building") || !st.Enabled {
		t.Fatalf("list doesn't show the IP limit: %+v", st)
	}

	// 4 per day globally, across sessions and IPs
	b := e.visitor(3, "198.51.100.2")
	slugB := b.create(t, "b")
	b.chat(t, slugB, "x", "")
	c := e.visitor(4, "198.51.100.3")
	refusedJSON(c.post("/build/api/new", map[string]string{"prompt": "c"}), 429, store.LimitGlobalDay)
	if st := c.state(t); !strings.Contains(st.Notice, "resting for today") {
		t.Fatalf("list doesn't say the builder is resting: %+v", st)
	}
	e.st.CreateVisitorFrontend(ctx, "fe/c", "C", hashSID(c.sid))
	refused(c.chatRaw("c", "x", ""), 429, store.LimitGlobalDay)

	// Ben is exempt
	e.form("/admin/builder/new", map[string][]string{"slug": {"bens"}})
	e.script()
	if rev := eventOf(e.chat(t, "bens", "anything", ""), "revision"); rev == nil {
		t.Fatal("admin chat limited")
	}
	if n, _ := e.st.VisitorRunCounts(ctx, nil, nil, e.s.runQuota()); n.GlobalDay != 4 {
		t.Fatalf("global count = %+v (admin runs must not count)", n)
	}

	// creation is rate limited per IP too
	e.s.limits = DefaultBuildLimits
	e.s.limits.NewPerIPWindow = 2
	d := e.visitor(5, "198.51.100.9")
	d.create(t, "first")
	d.create(t, "second")
	refusedJSON(d.post("/build/api/new", map[string]string{"prompt": "third"}), 429, msgSlowNew)
}

func TestPublicBuilderModelFailureIsFriendly(t *testing.T) {
	e := newBuilderServer(t, true)
	a := e.visitor(1, "198.51.100.1")
	slug := a.create(t, "x")
	e.model.Responses = nil // the scripted model errors, like an API or spend-limit failure
	rec := a.chatRaw(slug, "x", "")
	ev := eventOf(sseEvents(t, rec.Body.String()), "error")
	if ev == nil || ev.Text != buildMessage(msgUnavailable, e.s.limits) || !strings.HasPrefix(ev.Text, "The builder is unavailable right now.") {
		t.Fatalf("error event = %+v", ev)
	}
	e.model.Responses = []string{`{"id":"m","type":"message","role":"assistant","model":"x","stop_reason":"refusal","content":[]}`}
	ev = eventOf(sseEvents(t, a.chatRaw(slug, "x", "").Body.String()), "error")
	if ev == nil || ev.Text != buildMessage(msgRefused, e.s.limits) {
		t.Fatalf("refusal event = %+v", ev)
	}
}

func TestPublicBuilderDisabledWithoutKey(t *testing.T) {
	e := newBuilderServer(t, false)
	a := e.visitor(1, "198.51.100.1")
	if st := a.state(t); st.Enabled || !strings.Contains(st.Notice, "taking a break") {
		t.Fatalf("disabled state = %+v", st)
	}
	if rec := a.post("/build/api/new", map[string]string{"prompt": "x"}); rec.Code != 503 || !strings.Contains(apiError(rec), "taking a break") {
		t.Fatalf("new while disabled: %d %s", rec.Code, rec.Body)
	}
	e.st.CreateVisitorFrontend(context.Background(), "fe/x", "X", hashSID(a.sid))
	if rec := a.chatRaw("x", "x", ""); rec.Code != 503 || !strings.Contains(rec.Body.String(), "taking a break") {
		t.Fatalf("chat while disabled: %d %s", rec.Code, rec.Body)
	}
	// what's there is still listed
	if st := a.state(t); len(st.Frontends) != 1 {
		t.Fatalf("disabled list = %+v", st)
	}
}

func TestPublicBuilderGuards(t *testing.T) {
	e := newBuilderServer(t, true)
	ctx := context.Background()
	a := e.visitor(1, "198.51.100.1")
	slug := a.create(t, "x")
	// cross-site POSTs are refused
	for _, target := range []string{"/build/api/new", "/build/api/fe/" + slug + "/chat", "/build/api/fe/" + slug + "/live",
		"/build/api/fe/" + slug + "/submit", "/build/api/exit"} {
		if rec := a.do("POST", target, strings.NewReader(`{"prompt":"x"}`), "Sec-Fetch-Site", "cross-site",
			"Content-Type", "application/json"); rec.Code != http.StatusForbidden {
			t.Errorf("cross-site %s: %d", target, rec.Code)
		}
	}
	// big bodies are cut off
	if rec := a.post("/build/api/new", map[string]string{"prompt": strings.Repeat("x", 100<<10)}); rec.Code != 400 || apiError(rec) != buildMessage(msgTooLong, e.s.limits) {
		t.Fatalf("huge new: %d %s", rec.Code, rec.Body)
	}
	// slugs never collide with existing front ends, or with /build/<route>
	e.st.CreatePromptedFrontend(ctx, "fe/dark", "Ben's dark")
	if slug := a.create(t, "Dark"); slug != "dark-2" {
		t.Fatalf("slug = %q", slug)
	}
	if f, _ := e.st.BuilderFrontend(ctx, "fe/dark"); f.Title != "Ben's dark" {
		t.Fatal("existing front end changed")
	}
	for _, w := range []string{"New", "Preview", "Api"} {
		if slug := a.create(t, w); slug != strings.ToLower(w)+"-2" {
			t.Fatalf("reserved slug = %q", slug)
		}
	}
	if slug := a.create(t, "builtin site"); slug != "builtin-site" {
		t.Fatalf("slug = %q", slug)
	}
	// a name given by the visitor
	rec := a.post("/build/api/new", map[string]string{"prompt": "anything", "title": "  Night   sky "})
	if !strings.Contains(rec.Body.String(), `"slug":"night-sky"`) || !strings.Contains(rec.Body.String(), `"title":"Night sky"`) {
		t.Fatalf("named = %s", rec.Body)
	}
}

func TestSessionCookieSecureInProd(t *testing.T) {
	t.Setenv(frontend.RotationEnv, "")
	cfg := config.Config{Env: "prod", AdminUser: "admin", AdminPassword: "pw", SigningKey: testKey,
		MainOrigin: "https://example.com", UsercontentOrigin: "https://uc.example.com", FrontendsDir: t.TempDir()}
	s, err := New(cfg, store.NewMemory(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithBuilder(nil, revfiles.NewDir(cfg.FrontendsDir)))
	if err != nil {
		t.Fatal(err)
	}
	rec := get(s, "/build/api/frontends")
	for _, c := range rec.Result().Cookies() {
		if c.Name == sidCookie && c.Secure {
			return
		}
	}
	t.Fatalf("no Secure sid in prod: %v", rec.Result().Cookies())
}

func TestBuildLimitsFromEnv(t *testing.T) {
	env := map[string]string{"BUILD_RUNS_PER_SESSION_HOUR": "3", "BUILD_RUNS_GLOBAL_DAY": "0", "OBSERVE_XFF_HOPS": "2"}
	l, err := BuildLimitsFromEnv(func(k string) string { return env[k] })
	if err != nil || l.RunsPerSessionHour != 3 || l.RunsGlobalDay != 0 || l.RunsPerIPDay != 30 || l.MaxPrompt != 4000 || l.XFFHops != 2 {
		t.Fatalf("limits = %+v, %v", l, err)
	}
	env["BUILD_MAX_PROMPT"] = "lots"
	if _, err := BuildLimitsFromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("bad value accepted")
	}
}

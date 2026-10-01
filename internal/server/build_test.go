package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	req.AddCookie(&http.Cookie{Name: sidCookie, Value: v.sid})
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

func (v *visitor) form(target string, vals url.Values) *httptest.ResponseRecorder {
	return v.do("POST", target, strings.NewReader(vals.Encode()), "Content-Type", "application/x-www-form-urlencoded")
}

// create makes a front end from a prompt and returns its slug.
func (v *visitor) create(t *testing.T, prompt string) string {
	t.Helper()
	rec := v.form("/build/new", url.Values{"prompt": {prompt}})
	loc := rec.Header().Get("Location")
	if rec.Code != 303 || !strings.HasPrefix(loc, "/build/") || !strings.Contains(loc, "#start=") {
		t.Fatalf("new: %d %q", rec.Code, loc)
	}
	return strings.TrimPrefix(strings.SplitN(loc, "#", 2)[0], "/build/")
}

func (v *visitor) chatRaw(slug, prompt, parent string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"prompt": prompt, "parent": parent})
	return v.do("POST", "/build/"+slug+"/chat", strings.NewReader(string(body)), "Content-Type", "application/json")
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

	// /build: a session cookie, the public CSP, no front-end host
	req := httptest.NewRequest("GET", "/build", nil)
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
		t.Fatalf("/build: %d, sid %+v", rec.Code, sid)
	}
	body := rec.Body.String()
	if rec.Header().Get("Content-Security-Policy") != e.s.publicCSP || strings.Contains(body, "frontend-host.js") ||
		!strings.Contains(body, `action="/build/new"`) || strings.Contains(body, "<script>") || strings.Contains(body, "style=") {
		t.Fatalf("/build page: CSP %q\n%s", rec.Header().Get("Content-Security-Policy"), body)
	}
	// plain page views get no session cookie
	if rec := get(e.s, "/"); len(rec.Result().Cookies()) != 0 {
		t.Fatalf("/ set cookies: %v", rec.Result().Cookies())
	}

	// create → chat → revision
	slug := a.create(t, "A calm green page")
	if slug != "a-calm-green-page" {
		t.Fatalf("slug = %q", slug)
	}
	if rec := a.do("GET", "/build/"+slug, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `data-action="/build/`+slug+`/chat"`) {
		t.Fatalf("owner page: %d", rec.Code)
	}
	rev := a.chat(t, slug, "A calm green page", "")
	r1, err := e.st.Revision(ctx, rev.Revision)
	if err != nil || r1.Author != "visitor" || r1.FrontendID != "fe/"+slug {
		t.Fatalf("r1 = %+v, %v", r1, err)
	}
	if sys := e.model.Requests[0].System; len(sys) != 2 || sys[1].Text != builder.VisitorNote {
		t.Fatal("visitor run without the visitor note")
	}
	runs, _ := e.st.Runs(ctx, "fe/"+slug, 5)
	if len(runs) != 1 || runs[0].Status != store.RunDone {
		t.Fatalf("runs = %+v", runs)
	}
	page := a.do("GET", "/build/"+slug, nil).Body.String()
	if !strings.Contains(page, `data-ref="rev/`+r1.ID+`"`) || !strings.Contains(page, `data-preview-url="/build/preview"`) || !strings.Contains(page, "Draft") {
		t.Fatalf("page after r1:\n%s", page)
	}

	// preview URL for the owner
	rec = a.do("GET", "/build/preview?ref=rev/"+r1.ID, nil)
	var pv struct{ Ref, URL string }
	json.NewDecoder(rec.Body).Decode(&pv)
	if rec.Code != 200 || pv.Ref != "rev/"+r1.ID || !strings.HasPrefix(pv.URL, "http://127.0.0.1:8081/t/") {
		t.Fatalf("preview: %d %+v", rec.Code, pv)
	}

	// View live: fe_live, then /api/frontend serves the draft's latest revision
	rec = a.form("/build/"+slug+"/live", url.Values{"on": {"1"}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/" || a.cookies[liveCookie] != "fe/"+slug {
		t.Fatalf("live on: %d %q %v", rec.Code, rec.Header().Get("Location"), a.cookies)
	}
	rev2 := a.chat(t, slug, "greener", r1.ID)
	fe := decodeFrontend(t, a.do("GET", "/api/frontend", nil))
	if fe.Ref != "fe/"+slug || fe.Serve != "rev/"+rev2.Revision || !fe.Draft || fe.Exit != "/build/"+slug+"/live" {
		t.Fatalf("draft = %+v", fe)
	}
	if fb := decodeFrontend(t, a.do("GET", "/api/frontend?fallback=1", nil)); fb.Draft || fb.Ref != frontend.DefaultRef {
		t.Fatalf("fallback = %+v", fb)
	}
	if !strings.Contains(a.do("GET", "/build/"+slug, nil).Body.String(), "View it live on the site") {
		t.Fatal("no View live button")
	}
	// exit
	rec = a.form("/build/"+slug+"/live", url.Values{"on": {"0"}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/build/"+slug || a.cookies[liveCookie] != "" {
		t.Fatalf("live off: %d %v", rec.Code, a.cookies)
	}
	if fe := decodeFrontend(t, a.do("GET", "/api/frontend", nil)); fe.Draft {
		t.Fatalf("still a draft after exit: %+v", fe)
	}

	// submit r1 → pending, shown to the visitor and on the admin badge
	rec = a.form("/build/"+slug+"/submit", url.Values{"rev": {r1.ID}})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "msg=submitted") {
		t.Fatalf("submit: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if page := a.do("GET", "/build/"+slug+"?rev="+r1.ID, nil).Body.String(); !strings.Contains(page, "Waiting for review") || !strings.Contains(page, "is with Ben") {
		t.Fatalf("pending page:\n%s", page)
	}
	rec = adminDo(e, "GET", "/admin/builder/pending.json")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"pending":1}` {
		t.Fatalf("pending.json: %d %s", rec.Code, rec.Body)
	}
	adminPage := adminDo(e, "GET", "/admin/builder/").Body.String()
	if !strings.Contains(adminPage, "A calm green page") || !strings.Contains(adminPage, "/approve") || !strings.Contains(adminPage, "Visitor front ends") {
		t.Fatalf("admin page lacks the submission:\n%s", adminPage)
	}
	if rec := adminDo(e, "GET", "/admin/builder/fe/"+slug+"?rev="+r1.ID); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Made by a visitor") {
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
	if page := a.do("GET", "/build/"+slug+"?rev="+r1.ID, nil).Body.String(); !strings.Contains(page, "Approved") {
		t.Fatal("visitor doesn't see the approval")
	}
	if rec := adminDo(e, "GET", "/admin/builder/pending.json"); strings.TrimSpace(rec.Body.String()) != `{"pending":0}` {
		t.Fatalf("pending after approve: %s", rec.Body)
	}
	// the index lists it with its status
	if idx := a.do("GET", "/build", nil).Body.String(); !strings.Contains(idx, `href="/build/`+slug+`"`) || !strings.Contains(idx, "Approved") {
		t.Fatalf("index:\n%s", idx)
	}
}

func itoa64(n int64) string { b, _ := json.Marshal(n); return string(b) }

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
			method, target string
			form           url.Values
		}{
			{"GET", "/build/" + slug, nil},
			{"GET", "/build/preview?ref=rev/" + rev.Revision, nil},
			{"POST", "/build/" + slug + "/live", url.Values{"on": {"1"}}},
			{"POST", "/build/" + slug + "/submit", url.Values{"rev": {rev.Revision}}},
			{"GET", "/build/bens", nil},
			{"GET", "/build/preview?ref=rev/" + bensRev.ID, nil},
			{"GET", "/build/preview?ref=builtin/site", nil},
			{"POST", "/build/bens/live", url.Values{"on": {"1"}}},
			{"POST", "/build/bens/submit", url.Values{"rev": {bensRev.ID}}},
			{"GET", "/build/nope", nil},
		} {
			var rec *httptest.ResponseRecorder
			if c.method == "GET" {
				rec = who.do("GET", c.target, nil)
			} else {
				rec = who.form(c.target, c.form)
			}
			if rec.Code != 404 {
				t.Errorf("%q: %s %s = %d, want 404", who.sid, c.method, c.target, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "Secret garden") {
				t.Errorf("%s %s leaks the title", c.method, c.target)
			}
		}
		if rec := who.chatRaw(slug, "take it over", ""); rec.Code != 404 {
			t.Errorf("chat on another's front end: %d", rec.Code)
		}
		if rec := who.chatRaw("bens", "take it over", ""); rec.Code != 404 {
			t.Errorf("chat on Ben's front end: %d", rec.Code)
		}
		if idx := who.do("GET", "/build", nil).Body.String(); strings.Contains(idx, "Secret garden") {
			t.Error("another session lists the front end")
		}
		// a forged fe_live is ignored and cleared
		who.cookies[liveCookie] = "fe/" + slug
		if fe := decodeFrontend(t, who.do("GET", "/api/frontend", nil)); fe.Draft || fe.Ref == "fe/"+slug {
			t.Fatalf("non-owner fe_live served the draft: %+v", fe)
		}
		if _, ok := who.cookies[liveCookie]; ok {
			t.Error("non-owner fe_live not cleared")
		}
	}
	// nothing was submitted or changed by the attempts
	if _, err := e.st.LatestSubmission(ctx, "fe/"+slug); err == nil {
		t.Fatal("a non-owner submitted")
	}
	if revs, _ := e.st.Revisions(ctx, "fe/"+slug); len(revs) != 1 {
		t.Fatalf("revisions = %d", len(revs))
	}
	// the owner's other session-less requests: the owner still has it
	if rec := a.do("GET", "/build/"+slug, nil); rec.Code != 200 {
		t.Fatalf("owner lost access: %d", rec.Code)
	}
	// can't submit a revision of another front end through one's own
	other := a.create(t, "Second one")
	if rec := a.form("/build/"+other+"/submit", url.Values{"rev": {rev.Revision}}); rec.Code != 404 {
		t.Fatalf("submit foreign revision: %d", rec.Code)
	}
	if rec := a.form("/build/"+other+"/submit", url.Values{"rev": {bensRev.ID}}); rec.Code != 404 {
		t.Fatalf("submit Ben's revision: %d", rec.Code)
	}
}

func TestPublicBuilderFeLive(t *testing.T) {
	e := newBuilderServer(t, true)
	a := e.visitor(1, "198.51.100.1")
	slug := a.create(t, "Empty for now")
	// no revision yet: View live refuses, and a fe_live cookie is cleared
	if rec := a.form("/build/"+slug+"/live", url.Values{"on": {"1"}}); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "msg=no-revision") {
		t.Fatalf("live without revision: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, v := range []string{"fe/" + slug, "builtin/site", "garbage", "fe/nope"} {
		a.cookies[liveCookie] = v
		fe := decodeFrontend(t, a.do("GET", "/api/frontend", nil))
		if fe.Draft {
			t.Errorf("fe_live=%q served a draft: %+v", v, fe)
		}
		if _, ok := a.cookies[liveCookie]; ok {
			t.Errorf("fe_live=%q not cleared", v)
		}
	}
	// with a revision, the owner gets it; without the sid, nobody does
	rev := a.chat(t, slug, "now something", "")
	a.cookies[liveCookie] = "fe/" + slug
	if fe := decodeFrontend(t, a.do("GET", "/api/frontend", nil)); !fe.Draft || fe.Serve != "rev/"+rev.Revision {
		t.Fatalf("owner draft = %+v", fe)
	}
	rec := get(e.s, "/api/frontend", &http.Cookie{Name: liveCookie, Value: "fe/" + slug})
	if fe := decodeFrontend(t, rec); fe.Draft {
		t.Fatalf("no sid got the draft: %+v", fe)
	}
	// the live cookie is HttpOnly, Lax, a session cookie
	rec = a.form("/build/"+slug+"/live", url.Values{"on": {"1"}})
	for _, c := range rec.Result().Cookies() {
		if c.Name == liveCookie && (!c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 0 || c.Path != "/") {
			t.Fatalf("fe_live cookie = %+v", c)
		}
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

	a := e.visitor(1, "198.51.100.1")
	slug := a.create(t, "one")
	// prompt length (characters, not bytes)
	refused(a.chatRaw(slug, strings.Repeat("é", 51), ""), 400, msgTooLong)
	a.chat(t, slug, strings.Repeat("é", 50), "")
	if rec := a.form("/build/new", url.Values{"prompt": {strings.Repeat("x", 51)}}); rec.Header().Get("Location") != "/build?msg="+msgTooLong {
		t.Fatalf("new too long: %q", rec.Header().Get("Location"))
	}
	if rec := a.form("/build/new", url.Values{"prompt": {"  "}}); rec.Header().Get("Location") != "/build?msg="+msgEmpty {
		t.Fatalf("new empty: %q", rec.Header().Get("Location"))
	}

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
	// /build/new says so up front, and /build shows it
	if rec := a2.form("/build/new", url.Values{"prompt": {"more"}}); rec.Header().Get("Location") != "/build?msg="+store.LimitIPDay {
		t.Fatalf("new at IP limit: %q", rec.Header().Get("Location"))
	}
	if page := a2.do("GET", "/build?msg="+store.LimitIPDay, nil).Body.String(); !strings.Contains(page, "plenty of building") {
		t.Fatal("/build doesn't show the IP limit")
	}

	// 4 per day globally, across sessions and IPs
	b := e.visitor(3, "198.51.100.2")
	slugB := b.create(t, "b")
	b.chat(t, slugB, "x", "")
	c := e.visitor(4, "198.51.100.3")
	if rec := c.form("/build/new", url.Values{"prompt": {"c"}}); rec.Header().Get("Location") != "/build?msg="+store.LimitGlobalDay {
		t.Fatalf("new at global limit: %q", rec.Header().Get("Location"))
	}
	if page := c.do("GET", "/build", nil).Body.String(); !strings.Contains(page, "resting for today") {
		t.Fatal("/build doesn't say the builder is resting")
	}
	e.st.CreateVisitorFrontend(ctx, "fe/c", "C", hashSID(c.sid))
	refused(c.chatRaw("c", "x", ""), 429, store.LimitGlobalDay)

	// Ben is exempt
	e.form("/admin/builder/new", url.Values{"slug": {"bens"}})
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
	if rec := d.form("/build/new", url.Values{"prompt": {"third"}}); rec.Header().Get("Location") != "/build?msg="+msgSlowNew {
		t.Fatalf("third new: %q", rec.Header().Get("Location"))
	}
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
	page := a.do("GET", "/build", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "taking a break") || strings.Contains(page.Body.String(), `action="/build/new"`) {
		t.Fatalf("/build disabled: %d", page.Code)
	}
	if rec := a.form("/build/new", url.Values{"prompt": {"x"}}); rec.Header().Get("Location") != "/build?msg="+msgDisabled {
		t.Fatalf("new while disabled: %q", rec.Header().Get("Location"))
	}
	e.st.CreateVisitorFrontend(context.Background(), "fe/x", "X", hashSID(a.sid))
	if rec := a.chatRaw("x", "x", ""); rec.Code != 503 || !strings.Contains(rec.Body.String(), "taking a break") {
		t.Fatalf("chat while disabled: %d %s", rec.Code, rec.Body)
	}
	if rec := a.do("GET", "/build/x", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "taking a break") {
		t.Fatalf("page while disabled: %d", rec.Code)
	}
}

func TestPublicBuilderGuards(t *testing.T) {
	e := newBuilderServer(t, true)
	ctx := context.Background()
	a := e.visitor(1, "198.51.100.1")
	// cross-site POSTs are refused
	for _, target := range []string{"/build/new", "/build/x/chat", "/build/x/live", "/build/x/submit"} {
		if rec := a.do("POST", target, strings.NewReader("prompt=x"), "Sec-Fetch-Site", "cross-site",
			"Content-Type", "application/x-www-form-urlencoded"); rec.Code != http.StatusForbidden {
			t.Errorf("cross-site %s: %d", target, rec.Code)
		}
	}
	// big bodies are cut off
	if rec := a.form("/build/new", url.Values{"prompt": {strings.Repeat("x", 100<<10)}}); rec.Header().Get("Location") != "/build?msg="+msgTooLong {
		t.Fatalf("huge new: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// slugs never collide with existing front ends, or with /build/<route>
	e.st.CreatePromptedFrontend(ctx, "fe/dark", "Ben's dark")
	if slug := a.create(t, "Dark"); slug != "dark-2" {
		t.Fatalf("slug = %q", slug)
	}
	if f, _ := e.st.BuilderFrontend(ctx, "fe/dark"); f.Title != "Ben's dark" {
		t.Fatal("existing front end changed")
	}
	if slug := a.create(t, "New"); slug != "new-2" {
		t.Fatalf("reserved slug = %q", slug)
	}
	if slug := a.create(t, "Preview"); slug != "preview-2" {
		t.Fatalf("reserved slug = %q", slug)
	}
	if slug := a.create(t, "builtin site"); slug != "builtin-site" {
		t.Fatalf("slug = %q", slug)
	}
	// a name given by the visitor
	rec := a.form("/build/new", url.Values{"prompt": {"anything"}, "title": {"  Night   sky "}})
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/build/night-sky#start=anything") {
		t.Fatalf("named = %q", loc)
	}
	// /build/ redirects; unknown /build paths 404
	if rec := a.do("GET", "/build/", nil); rec.Code != 301 {
		t.Fatalf("/build/: %d", rec.Code)
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
	rec := get(s, "/build")
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

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

	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/fetoken"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

var testKey = []byte("test-key")

func newTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv(frontend.RotationEnv, "")
	cfg := config.Config{
		Env:               "dev",
		AdminUser:         "admin",
		AdminPassword:     "pw",
		SigningKey:        testKey,
		MainOrigin:        "http://localhost:8080",
		UsercontentOrigin: "http://127.0.0.1:8081",
	}
	s, err := New(cfg, store.NewMemory(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(s *Server, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func decodeFrontend(t *testing.T, rec *httptest.ResponseRecorder) frontendResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var fe frontendResponse
	if err := json.NewDecoder(rec.Body).Decode(&fe); err != nil {
		t.Fatal(err)
	}
	return fe
}

func pickCookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == pickCookie {
			return c
		}
	}
	return nil
}

func TestAPIFrontendURLAndToken(t *testing.T) {
	s := newTestServer(t)
	issued := time.Unix(1_900_000_000, 0)
	s.now = func() time.Time { return issued }
	s.intn = func(int) int { return 1 }

	fe := decodeFrontend(t, get(s, "/api/frontend"))
	if fe.Ref != "builtin/particle-stream" {
		t.Errorf("ref = %q", fe.Ref)
	}
	prefix := "http://127.0.0.1:8081/t/"
	if !strings.HasPrefix(fe.URL, prefix) || !strings.HasSuffix(fe.URL, "/") {
		t.Fatalf("url = %q", fe.URL)
	}
	tok := strings.TrimSuffix(strings.TrimPrefix(fe.URL, prefix), "/")
	if strings.Contains(tok, "/") {
		t.Fatalf("token has a slash: %q", tok)
	}
	c, err := fetoken.Verify(testKey, tok)
	if err != nil {
		t.Fatal(err)
	}
	// built-ins serve themselves: ref (front-end ID) == serve (token ref)
	if c.Ref != fe.Serve || fe.Serve != fe.Ref || !c.Issued.Equal(issued) {
		t.Errorf("claims = %+v", c)
	}
	if _, err := fetoken.Verify([]byte("other"), tok); err == nil {
		t.Error("token verified with the wrong key")
	}
}

func TestAPIFrontendCookie(t *testing.T) {
	s := newTestServer(t)
	s.intn = func(int) int { return 1 }

	rec := get(s, "/api/frontend")
	fe := decodeFrontend(t, rec)
	c := pickCookieOf(rec)
	if c == nil {
		t.Fatal("no fe_pick cookie set")
	}
	if c.Value != fe.Ref || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 0 || !c.Expires.IsZero() || c.Path != "/" {
		t.Errorf("cookie = %+v", c)
	}

	// sticky: the cookie wins over a different random pick, and isn't re-set
	s.intn = func(int) int { return 0 }
	for range 3 {
		rec = get(s, "/api/frontend", &http.Cookie{Name: pickCookie, Value: c.Value})
		if got := decodeFrontend(t, rec).Ref; got != c.Value {
			t.Errorf("sticky ref = %q, want %q", got, c.Value)
		}
		if pickCookieOf(rec) != nil {
			t.Error("cookie re-set for a valid pick")
		}
	}

	// re-pick when the cookie is malformed or not in the rotation
	for _, bad := range []string{"builtin/retired", "nonsense", "builtin/../x", ""} {
		rec = get(s, "/api/frontend", &http.Cookie{Name: pickCookie, Value: bad})
		fe = decodeFrontend(t, rec)
		if fe.Ref != frontend.DefaultRef {
			t.Errorf("cookie %q: ref = %q, want re-pick %q", bad, fe.Ref, frontend.DefaultRef)
		}
		if nc := pickCookieOf(rec); nc == nil || nc.Value != fe.Ref {
			t.Errorf("cookie %q: new cookie = %+v", bad, nc)
		}
	}
}

func TestAPIFrontendFallback(t *testing.T) {
	s := newTestServer(t)
	s.intn = func(int) int { return 1 }
	for _, cookies := range [][]*http.Cookie{nil, {{Name: pickCookie, Value: "builtin/particle-stream"}}} {
		rec := get(s, "/api/frontend?fallback=1", cookies...)
		fe := decodeFrontend(t, rec)
		if fe.Ref != frontend.DefaultRef {
			t.Errorf("fallback ref = %q", fe.Ref)
		}
		if pickCookieOf(rec) != nil {
			t.Error("fallback set the cookie")
		}
		tok := strings.TrimSuffix(strings.TrimPrefix(fe.URL, "http://127.0.0.1:8081/t/"), "/")
		if c, err := fetoken.Verify(testKey, tok); err != nil || c.Ref != frontend.DefaultRef {
			t.Errorf("fallback token: %+v, %v", c, err)
		}
	}
}

func TestFrontendRotationEnv(t *testing.T) {
	cfg := config.Config{Env: "dev", SigningKey: testKey, UsercontentOrigin: "http://127.0.0.1:8081"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Setenv(frontend.RotationEnv, "builtin/particle-stream")
	s, err := New(cfg, store.NewMemory(), log)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if got := decodeFrontend(t, get(s, "/api/frontend")).Ref; got != "builtin/particle-stream" {
			t.Errorf("ref = %q", got)
		}
	}
	// a pick outside the overridden rotation is re-picked
	rec := get(s, "/api/frontend", &http.Cookie{Name: pickCookie, Value: frontend.DefaultRef})
	if got := decodeFrontend(t, rec).Ref; got != "builtin/particle-stream" {
		t.Errorf("ref = %q", got)
	}

	t.Setenv(frontend.RotationEnv, "builtin/site,Bad Ref")
	if _, err := New(cfg, store.NewMemory(), log); err == nil {
		t.Error("invalid FRONTEND_ROTATION: want startup error")
	}
}

func TestPublicPages(t *testing.T) {
	s := newTestServer(t)
	for _, tc := range []struct {
		path   string
		status int
		want   string
	}{
		{"/", http.StatusOK, "Home page copy goes here."},
		{"/experiments/", http.StatusOK, "Particle Stream"},
		{"/experiments", http.StatusOK, "Particle Stream"},
		{"/no-such-page", http.StatusNotFound, "Not found"},
		{"/experiments/particle-stream/", http.StatusNotFound, "Not found"},
		{"/a/b/c", http.StatusNotFound, "Not found"},
	} {
		rec := get(s, tc.path)
		body := rec.Body.String()
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d", tc.path, rec.Code, tc.status)
		}
		if !strings.Contains(body, `<div id="transcript">`) || !strings.Contains(body, tc.want) {
			t.Errorf("%s: transcript missing %q", tc.path, tc.want)
		}
		if !strings.Contains(body, `<script src="/static/frontend-host.js" defer></script>`) {
			t.Errorf("%s: no frontend-host.js", tc.path)
		}
		for _, gone := range []string{"/site/", "<canvas", "navigator.gpu", `href="/experiments/particle-stream`} {
			if strings.Contains(body, gone) {
				t.Errorf("%s: still references %q", tc.path, gone)
			}
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-src http://127.0.0.1:8081") {
			t.Errorf("%s: CSP = %q", tc.path, csp)
		}
	}
}

func TestOtherRoutes(t *testing.T) {
	s := newTestServer(t)
	if rec := get(s, "/health"); rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("health: %d %q", rec.Code, rec.Body)
	}
	rec := get(s, "/api/site.json")
	var site struct {
		ContractVersion int
		Pages           []map[string]any
		Experiments     []map[string]any
	}
	if err := json.NewDecoder(rec.Body).Decode(&site); err != nil || site.ContractVersion != 1 || len(site.Pages) != 1 || len(site.Experiments) != 1 {
		t.Errorf("site.json: %v %+v", err, site)
	} else if p := site.Pages[0]; p["slug"] != "" || p["title"] != "Ben Priddy" || p["body"] == nil || p["_generated"] == nil {
		t.Errorf("site.json home page: %+v", p)
	}
	if rec := get(s, "/static/frontend-host.js"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "/api/frontend") {
		t.Errorf("frontend-host.js: %d", rec.Code)
	}
	if rec := get(s, "/admin/"); rec.Code != http.StatusUnauthorized {
		t.Errorf("admin without auth: %d", rec.Code)
	}
	req := httptest.NewRequest("GET", "/admin/", nil)
	req.SetBasicAuth("admin", "pw")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("admin: %d csp=%q", rec.Code, rec.Header().Get("Content-Security-Policy"))
	}
	if rec := get(s, "/site/site.js"); rec.Code != http.StatusNotFound {
		t.Errorf("/site/ still served: %d", rec.Code)
	}
}

func TestRotationFromStoreAndAdminToggle(t *testing.T) {
	s := newTestServer(t)
	pick := func(i int) string {
		s.intn = func(int) int { return i }
		var resp frontendResponse
		json.NewDecoder(get(s, "/api/frontend").Body).Decode(&resp)
		return resp.Ref
	}
	// store rotation: default first, then by ref
	if got := pick(0); got != "builtin/site" {
		t.Fatalf("pick(0) = %q", got)
	}
	if got := pick(1); got != "builtin/particle-stream" {
		t.Fatalf("pick(1) = %q", got)
	}

	toggle := func(ref, in string) int {
		req := httptest.NewRequest("POST", "/admin/frontends", strings.NewReader("ref="+ref+"&in_rotation="+in))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth("admin", "pw")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := toggle("builtin/particle-stream", "0"); code != http.StatusSeeOther {
		t.Fatalf("toggle off = %d", code)
	}
	if got := pick(0); got != "builtin/site" {
		t.Fatalf("after removal pick = %q", got)
	}
	// a visitor whose cookie names a removed front end is re-picked
	rec := get(s, "/api/frontend", &http.Cookie{Name: pickCookie, Value: "builtin/particle-stream"})
	var resp frontendResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Ref != "builtin/site" {
		t.Fatalf("stale cookie pick = %q", resp.Ref)
	}
	// with nothing in rotation, visitors still get the default
	toggle("builtin/site", "0")
	if got := pick(0); got != "builtin/site" {
		t.Fatalf("empty rotation pick = %q", got)
	}
	if code := toggle("builtin/nope", "1"); code != http.StatusNotFound {
		t.Fatalf("unknown ref toggle = %d", code)
	}
	// the dashboard lists front ends
	req := httptest.NewRequest("GET", "/admin/", nil)
	req.SetBasicAuth("admin", "pw")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "builtin/particle-stream") || !strings.Contains(rec.Body.String(), "Add to rotation") {
		t.Fatal("dashboard missing front-end rotation controls")
	}
}

func TestWWWRedirectsToMainOrigin(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/about?x=1", nil)
	req.Host = "www.benpriddy.com"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "http://localhost:8080/about?x=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// the canonical host is served normally
	req = httptest.NewRequest("GET", "/", nil)
	req.Host = "benpriddy.com"
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apex got %d", rec.Code)
	}
}

func TestBodyParagraphs(t *testing.T) {
	s := newTestServer(t)
	s.store.SavePage(context.Background(), content.Page{Slug: "", Title: "Ben Priddy", Body: "Coming soon.\n\nFirst <b>para</b>.\r\n\r\n\n\nSecond.", Published: true})
	body := get(s, "/").Body.String()
	for _, want := range []string{`<p class="lede">Coming soon.</p>`, `<p class="lede">First &lt;b&gt;para&lt;/b&gt;.</p>`, `<p class="lede">Second.</p>`} {
		if !strings.Contains(body, want) {
			t.Errorf("home missing %s", want)
		}
	}
}

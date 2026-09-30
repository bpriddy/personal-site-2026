package usercontent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/fetoken"
	webuc "github.com/bpriddy/personal-site-2026/web/usercontent"
)

const (
	testOrigin = "https://main.example"
	secret     = "TOP-SECRET-DO-NOT-SERVE"
)

var (
	testKey = []byte("test-key")
	t0      = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
)

const wantCSP = "sandbox allow-scripts; default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self' data:; media-src 'self' blob:; " +
	"connect-src 'self'; worker-src 'self' blob:; frame-src 'none'; form-action 'none'; " +
	"base-uri 'none'; frame-ancestors https://main.example"

// fixture lays out:
//
//	<root>/outside-secret.txt           (outside FRONTENDS_DIR)
//	<root>/fe/secret.txt                (inside FRONTENDS_DIR, outside any ref)
//	<root>/fe/builtin/other/index.html  (another ref)
//	<root>/fe/builtin/demo/...          (the ref under test)
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	fe := filepath.Join(root, "fe")
	demo := filepath.Join(fe, "builtin", "demo")
	files := map[string]string{
		filepath.Join(root, "outside-secret.txt"):           secret,
		filepath.Join(fe, "secret.txt"):                     secret,
		filepath.Join(fe, "builtin", "other", "index.html"): "OTHER " + secret,
		filepath.Join(demo, "index.html"):                   "<!doctype html><title>demo</title>",
		filepath.Join(demo, "app.js"):                       "console.log('hi')",
		filepath.Join(demo, "mod.mjs"):                      "export {}",
		filepath.Join(demo, "site.wasm"):                    "\x00asm\x01\x00\x00\x00",
		filepath.Join(demo, "shader.wgsl"):                  "@vertex fn main() {}",
		filepath.Join(demo, "blob.unknownext"):              "?",
		filepath.Join(demo, "sub", "page.txt"):              "sub page",
		filepath.Join(demo, ".env"):                         secret,
		filepath.Join(demo, ".git", "config"):               secret,
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// symlinks inside the ref that point out of it
	if err := os.Symlink(filepath.Join(fe, "secret.txt"), filepath.Join(demo, "abs-link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../secret.txt", filepath.Join(demo, "rel-link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../outside-secret.txt", filepath.Join(demo, "far-link.txt")); err != nil {
		t.Fatal(err)
	}
	return fe
}

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

func newHandler(t *testing.T) (*Handler, *testClock) {
	t.Helper()
	clk := &testClock{now: t0}
	h, err := New(Options{
		SigningKey: testKey,
		MainOrigin: testOrigin,
		Source:     NewDirSource(fixture(t)),
		Now:        clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, clk
}

func tok(ref string, issued time.Time) string {
	return fetoken.Sign(testKey, fetoken.Claims{Ref: ref, Issued: issued})
}

// do sends a request with a raw (uncleaned) path.
func do(h http.Handler, path string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var iframe = map[string]string{"Sec-Fetch-Dest": "iframe"}

func checkTokenHeaders(t *testing.T, w *httptest.ResponseRecorder, cache string) {
	t.Helper()
	want := map[string]string{
		"Content-Security-Policy":      wantCSP,
		"Access-Control-Allow-Origin":  "*",
		"Cross-Origin-Resource-Policy": "cross-origin",
		"Referrer-Policy":              "no-referrer",
		"X-Content-Type-Options":       "nosniff",
		"Cache-Control":                cache,
	}
	for k, v := range want {
		if got := w.Header().Values(k); len(got) != 1 || got[0] != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func noLeak(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(w.Body.String(), secret) {
		t.Fatalf("secret leaked: %d %q", w.Code, w.Body.String())
	}
}

func TestIndexInIframe(t *testing.T) {
	h, _ := newHandler(t)
	for _, p := range []string{"/", "/index.html"} {
		w := do(h, "/t/"+tok("builtin/demo", t0)+p, iframe)
		if w.Code != 200 {
			t.Fatalf("%s: code %d", p, w.Code)
		}
		if w.Body.String() != "<!doctype html><title>demo</title>" {
			t.Errorf("%s: body %q", p, w.Body.String())
		}
		checkTokenHeaders(t, w, "no-store")
		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("content-type %q", ct)
		}
	}
}

func TestIndexNeedsIframeDest(t *testing.T) {
	h, _ := newHandler(t)
	u := "/t/" + tok("builtin/demo", t0) + "/"
	for _, hdr := range []map[string]string{
		nil,
		{"Sec-Fetch-Dest": "document"},
		{"Sec-Fetch-Dest": "script"},
		{"Sec-Fetch-Dest": "Iframe"},
		{"Sec-Fetch-Dest": ""},
	} {
		w := do(h, u, hdr)
		if w.Code != 403 {
			t.Errorf("%v: code %d, want 403", hdr, w.Code)
		}
		checkTokenHeaders(t, w, "no-store")
	}
	// same for the explicit index.html spelling
	if w := do(h, "/t/"+tok("builtin/demo", t0)+"/index.html", nil); w.Code != 403 {
		t.Errorf("index.html without dest: %d", w.Code)
	}
}

func TestIndexAge(t *testing.T) {
	h, clk := newHandler(t)
	u := "/t/" + tok("builtin/demo", t0) + "/"
	for _, tc := range []struct {
		age  time.Duration
		code int
	}{
		{0, 200}, {59 * time.Second, 200}, {60 * time.Second, 200},
		{61 * time.Second, 403}, {10 * time.Minute, 403},
	} {
		clk.now = t0.Add(tc.age)
		if w := do(h, u, iframe); w.Code != tc.code {
			t.Errorf("age %v: code %d, want %d", tc.age, w.Code, tc.code)
		}
	}
}

func TestAssetAge(t *testing.T) {
	h, clk := newHandler(t)
	u := "/t/" + tok("builtin/demo", t0) + "/app.js"
	for _, tc := range []struct {
		age  time.Duration
		code int
	}{
		{0, 200}, {29 * time.Minute, 200}, {30 * time.Minute, 200},
		{31 * time.Minute, 403}, {48 * time.Hour, 403},
	} {
		clk.now = t0.Add(tc.age)
		w := do(h, u, nil) // assets don't need Sec-Fetch-Dest
		if w.Code != tc.code {
			t.Errorf("age %v: code %d, want %d", tc.age, w.Code, tc.code)
		}
		if tc.code == 200 {
			checkTokenHeaders(t, w, "private, max-age=1800")
			if w.Body.String() != "console.log('hi')" {
				t.Errorf("body %q", w.Body.String())
			}
		}
	}
}

func TestFutureToken(t *testing.T) {
	h, _ := newHandler(t)
	for _, tc := range []struct {
		skew time.Duration
		code int
	}{
		{10 * time.Second, 200}, {30 * time.Second, 200},
		{31 * time.Second, 403}, {time.Hour, 403},
	} {
		tk := tok("builtin/demo", t0.Add(tc.skew))
		if w := do(h, "/t/"+tk+"/app.js", nil); w.Code != tc.code {
			t.Errorf("asset issued +%v: code %d, want %d", tc.skew, w.Code, tc.code)
		}
		if w := do(h, "/t/"+tk+"/", iframe); w.Code != tc.code {
			t.Errorf("index issued +%v: code %d, want %d", tc.skew, w.Code, tc.code)
		}
	}
}

func TestForgedToken(t *testing.T) {
	h, _ := newHandler(t)
	good := tok("builtin/demo", t0)
	body, _, _ := strings.Cut(good, ".")
	other := fetoken.Sign([]byte("wrong-key"), fetoken.Claims{Ref: "builtin/demo", Issued: t0})
	// swap in a different ref's payload under the demo signature
	otherBody, _, _ := strings.Cut(tok("builtin/other", t0), ".")
	_, goodSig, _ := strings.Cut(good, ".")
	for _, bad := range []string{
		other,
		otherBody + "." + goodSig,
		body + ".",
		body,
		"garbage",
		"a.b",
		good + "x",
		strings.ToUpper(good),
	} {
		for _, p := range []string{"/", "/app.js"} {
			w := do(h, "/t/"+bad+p, iframe)
			if w.Code != 403 {
				t.Errorf("token %q%s: code %d, want 403", bad, p, w.Code)
			}
			checkTokenHeaders(t, w, "no-store")
			noLeak(t, w)
		}
	}
	if w := do(h, "/t//app.js", nil); w.Code != 403 {
		t.Errorf("empty token: code %d", w.Code)
	}
}

func TestInvalidRefInSignedToken(t *testing.T) {
	h, _ := newHandler(t)
	for _, ref := range []string{
		"builtin/../x", "builtin/..", "draft/1", "draft/1/2", "snap/1", "builtin",
		"builtin/", "builtin/demo/../other", "builtin/Demo", "../fe", "/etc",
		"builtin/demo/", "builtin/demo\n", ".", "..",
	} {
		for _, p := range []string{"/", "/secret.txt", "/index.html"} {
			w := do(h, "/t/"+tok(ref, t0)+p, iframe)
			if w.Code != 403 {
				t.Errorf("ref %q%s: code %d, want 403", ref, p, w.Code)
			}
			noLeak(t, w)
		}
	}
}

func TestTraversalAndDotfiles(t *testing.T) {
	h, _ := newHandler(t)
	tk := tok("builtin/demo", t0)
	for _, p := range []string{
		"/../secret.txt",
		"/../../secret.txt",
		"/../../../outside-secret.txt",
		"/../other/index.html",
		"/sub/../../../secret.txt",
		"/sub/../app.js", // not clean, even if harmless
		"/./app.js",
		"/%2e%2e/%2e%2e/secret.txt",
		"/..%2f..%2fsecret.txt",
		"/%2E%2E%2F%2E%2E%2Fsecret.txt",
		"/.env",
		"/.git/config",
		"/sub/.hidden",
		"/..",
		"/.",
		"/sub/", // directory
		"/sub",  // directory
		"//app.js",
		"/sub//page.txt",
		"/..\\..\\secret.txt",
		"/app.js%00.png",
		"/abs-link.txt", // symlinks out of the ref
		"/rel-link.txt",
		"/far-link.txt",
		"/app.js/x", // not a directory
	} {
		w := do(h, "/t/"+tk+p, iframe)
		if w.Code != 403 && w.Code != 404 {
			t.Errorf("%s: code %d, want 403 or 404", p, w.Code)
		}
		checkTokenHeaders(t, w, "no-store")
		noLeak(t, w)
	}
	// and nothing about the path can reach another ref's files
	if w := do(h, "/t/"+tk+"/../other/", iframe); strings.Contains(w.Body.String(), "OTHER") {
		t.Fatal("reached another ref")
	}
}

func TestMissingFile(t *testing.T) {
	h, _ := newHandler(t)
	tk := tok("builtin/demo", t0)
	for _, p := range []string{"/nope.js", "/sub/nope.txt", "/nodir/x.js"} {
		w := do(h, "/t/"+tk+p, nil)
		if w.Code != 404 {
			t.Errorf("%s: code %d, want 404", p, w.Code)
		}
		checkTokenHeaders(t, w, "no-store")
	}
	// a valid ref with no files at all
	if w := do(h, "/t/"+tok("builtin/ghost", t0)+"/", iframe); w.Code != 404 {
		t.Errorf("missing ref dir: code %d, want 404", w.Code)
	}
	if w := do(h, "/t/"+tk+"/sub/page.txt", nil); w.Code != 200 || w.Body.String() != "sub page" {
		t.Errorf("nested file: %d %q", w.Code, w.Body.String())
	}
}

func TestOtherRoutes(t *testing.T) {
	h, _ := newHandler(t)
	for _, p := range []string{"/", "/index.html", "/t", "/t/", "/t/" + tok("builtin/demo", t0),
		"/builtin/demo/index.html", "/secret.txt", "/healthz/", "/site-host.js/x", "/favicon.ico"} {
		if w := do(h, p, iframe); w.Code != 404 {
			t.Errorf("%s: code %d, want 404", p, w.Code)
		} else {
			noLeak(t, w)
		}
	}
	w := do(h, "/healthz", nil)
	if w.Code != 200 || w.Body.String() != "ok" {
		t.Errorf("healthz: %d %q", w.Code, w.Body.String())
	}
	r := httptest.NewRequest(http.MethodPost, "/t/"+tok("builtin/demo", t0)+"/app.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: code %d", rec.Code)
	}
}

func TestHead(t *testing.T) {
	h, _ := newHandler(t)
	r := httptest.NewRequest(http.MethodHead, "/t/"+tok("builtin/demo", t0)+"/app.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "17" {
		t.Errorf("HEAD: %d len=%d cl=%q", w.Code, w.Body.Len(), w.Header().Get("Content-Length"))
	}
}

func TestSiteHostJS(t *testing.T) {
	h, _ := newHandler(t)
	w := do(h, "/site-host.js", nil)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	want := strings.ReplaceAll(webuc.SiteHostJS, "__MAIN_ORIGIN__", testOrigin)
	if w.Body.String() != want {
		t.Errorf("body %q, want %q", w.Body.String(), want)
	}
	if strings.Contains(w.Body.String(), "__MAIN_ORIGIN__") {
		t.Error("placeholder not replaced")
	}
	for k, v := range map[string]string{
		"Content-Type":                 "text/javascript; charset=utf-8",
		"Access-Control-Allow-Origin":  "*",
		"Cross-Origin-Resource-Policy": "cross-origin",
		"X-Content-Type-Options":       "nosniff",
		"Cache-Control":                "public, max-age=300",
	} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestSiteHostSubstitution(t *testing.T) {
	// the real file is a placeholder for now, so check the replacement itself
	orig := webuc.SiteHostJS
	t.Cleanup(func() { webuc.SiteHostJS = orig })
	webuc.SiteHostJS = `const O = "__MAIN_ORIGIN__"; parent.postMessage(m, "__MAIN_ORIGIN__");`
	h, _ := newHandler(t)
	got := do(h, "/site-host.js", nil).Body.String()
	want := `const O = "https://main.example"; parent.postMessage(m, "https://main.example");`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestContentTypes(t *testing.T) {
	h, _ := newHandler(t)
	tk := tok("builtin/demo", t0)
	for p, want := range map[string]string{
		"/app.js":          "text/javascript; charset=utf-8",
		"/mod.mjs":         "text/javascript; charset=utf-8",
		"/site.wasm":       "application/wasm",
		"/shader.wgsl":     "text/plain; charset=utf-8",
		"/sub/page.txt":    "text/plain; charset=utf-8",
		"/blob.unknownext": "application/octet-stream",
	} {
		w := do(h, "/t/"+tk+p, nil)
		if w.Code != 200 {
			t.Errorf("%s: code %d", p, w.Code)
			continue
		}
		if got := w.Header().Get("Content-Type"); got != want {
			t.Errorf("%s: content-type %q, want %q", p, got, want)
		}
	}
	for name, want := range map[string]string{
		"a.JS": "text/javascript; charset=utf-8", "x.woff2": "font/woff2", "i.png": "image/png",
		"s.css": "text/css; charset=utf-8", "d.json": "application/json",
	} {
		if got := contentType(name); got != want {
			t.Errorf("contentType(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestNewValidates(t *testing.T) {
	src := NewDirSource(t.TempDir())
	for _, o := range []Options{
		{MainOrigin: testOrigin, Source: src},
		{SigningKey: testKey, MainOrigin: testOrigin},
		{SigningKey: testKey, Source: src},
		{SigningKey: testKey, Source: src, MainOrigin: "https://a.example/path"},
		{SigningKey: testKey, Source: src, MainOrigin: "https://a.example; script-src *"},
		{SigningKey: testKey, Source: src, MainOrigin: "javascript:alert(1)"},
		{SigningKey: testKey, Source: src, MainOrigin: "a.example"},
	} {
		if _, err := New(o); err == nil {
			t.Errorf("New(%+v) succeeded", o)
		}
	}
	if _, err := New(Options{SigningKey: testKey, Source: src, MainOrigin: "http://localhost:8080"}); err != nil {
		t.Errorf("dev origin rejected: %v", err)
	}
}

func TestDirSourceRejectsInvalidRef(t *testing.T) {
	s := NewDirSource(fixture(t))
	for _, ref := range []string{"builtin/../", "..", "draft/1"} {
		if o, err := s.Open(t.Context(), ref, "secret.txt"); err == nil {
			b, _ := io.ReadAll(o.Body)
			o.Body.Close()
			t.Errorf("ref %q opened: %q", ref, b)
		}
	}
}

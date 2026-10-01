package usercontent

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/fstest"

	"github.com/bpriddy/personal-site-2026/web"
)

// the house fonts every front end may load (docs/design-pov.md, section 8)
var houseFonts = []string{
	"newsreader-roman.woff2", "newsreader-italic.woff2",
	"instrument-sans-roman.woff2", "instrument-sans-italic.woff2",
	"fragment-mono-regular.woff2", "fragment-mono-italic.woff2",
}

func TestFonts(t *testing.T) {
	h, _ := newHandler(t)
	for _, name := range houseFonts {
		want, err := fs.ReadFile(web.Fonts, name)
		if err != nil {
			t.Fatalf("%s: not embedded: %v", name, err)
		}
		if !bytes.HasPrefix(want, []byte("wOF2")) {
			t.Fatalf("%s: not a WOFF2 file", name)
		}
		// a sandboxed front end's request: opaque origin, CORS mode
		w := do(h, "/fonts/"+name, map[string]string{"Origin": "null", "Sec-Fetch-Dest": "font", "Sec-Fetch-Mode": "cors"})
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), want) {
			t.Errorf("%s: code %d, %d bytes (want %d)", name, w.Code, w.Body.Len(), len(want))
		}
		for k, v := range map[string]string{
			"Content-Type":                 "font/woff2",
			"Access-Control-Allow-Origin":  "*",
			"Cross-Origin-Resource-Policy": "cross-origin",
			"X-Content-Type-Options":       "nosniff",
			"Cache-Control":                "public, max-age=31536000, immutable",
			"Content-Length":               strconv.Itoa(len(want)),
		} {
			if got := w.Header().Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", name, k, got, v)
			}
		}
		// no CSP: a font isn't a document
		if csp := w.Header().Get("Content-Security-Policy"); csp != "" {
			t.Errorf("%s: CSP %q", name, csp)
		}
	}
	// each family's license ships with it
	for _, lic := range []string{"OFL-newsreader.txt", "OFL-instrumentsans.txt", "OFL-fragmentmono.txt", "SOURCES.txt"} {
		w := do(h, "/fonts/"+lic, nil)
		if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("Open Font License")) ||
			w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("%s: %d %q", lic, w.Code, w.Header().Get("Content-Type"))
		}
	}
}

func TestFontsHead(t *testing.T) {
	h, _ := newHandler(t)
	r := httptest.NewRequest(http.MethodHead, "/fonts/"+houseFonts[0], nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") == "" {
		t.Errorf("HEAD: %d len=%d cl=%q", w.Code, w.Body.Len(), w.Header().Get("Content-Length"))
	}
	r = httptest.NewRequest(http.MethodPost, "/fonts/"+houseFonts[0], nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: code %d", w.Code)
	}
}

func TestFontsOnlyServesFontFiles(t *testing.T) {
	clk := &testClock{now: t0}
	h, err := New(Options{
		SigningKey: testKey, MainOrigin: testOrigin, Source: NewDirSource(fixture(t)), Now: clk.Now,
		Fonts: fstest.MapFS{
			"a.woff2":       {Data: []byte("wOF2a")},
			"LICENSE.txt":   {Data: []byte("license")},
			"secret.json":   {Data: []byte(secret)},
			".hidden.woff2": {Data: []byte(secret)},
			"sub/b.woff2":   {Data: []byte(secret)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if w := do(h, "/fonts/a.woff2", nil); w.Code != 200 || w.Body.String() != "wOF2a" {
		t.Errorf("a.woff2: %d %q", w.Code, w.Body.String())
	}
	if w := do(h, "/fonts/LICENSE.txt", nil); w.Code != 200 || w.Body.String() != "license" {
		t.Errorf("LICENSE.txt: %d %q", w.Code, w.Body.String())
	}
	for _, p := range []string{
		"/fonts/", "/fonts", "/fonts/secret.json", "/fonts/.hidden.woff2", "/fonts/sub/b.woff2",
		"/fonts/../fonts/a.woff2", "/fonts/missing.woff2", "/fonts/a.woff2/", "/fonts/%2e%2e/secret.txt",
	} {
		w := do(h, p, nil)
		if w.Code != 404 {
			t.Errorf("%s: code %d, want 404", p, w.Code)
		}
		if bytes.Contains(w.Body.Bytes(), []byte(secret)) {
			t.Errorf("%s: leaked", p)
		}
	}
}

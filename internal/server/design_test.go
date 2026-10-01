package server

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

// The house fonts are served self-hosted under the public CSP, with a long cache.
func TestStaticFonts(t *testing.T) {
	s := newTestServer(t)
	for _, name := range []string{"newsreader-roman.woff2", "instrument-sans-roman.woff2", "fragment-mono-regular.woff2"} {
		rec := get(s, "/static/fonts/"+name)
		if rec.Code != 200 || !bytes.HasPrefix(rec.Body.Bytes(), []byte("wOF2")) {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "font/woff2" {
			t.Errorf("%s: Content-Type %q", name, ct)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
			t.Errorf("%s: Cache-Control %q", name, cc)
		}
	}
	if rec := get(s, "/static/fonts/OFL-newsreader.txt"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Open Font License") {
		t.Errorf("license: %d", rec.Code)
	}
	if rec := get(s, "/static/fonts/nope.woff2"); rec.Code != 404 {
		t.Errorf("missing font: %d", rec.Code)
	}
	// the shell preloads the two critical files and links the stylesheets
	body := get(s, "/").Body.String()
	for _, want := range []string{
		`<link rel="preload" href="/static/fonts/newsreader-roman.woff2" as="font" type="font/woff2" crossorigin>`,
		`<link rel="preload" href="/static/fonts/instrument-sans-roman.woff2" as="font" type="font/woff2" crossorigin>`,
		`<link rel="stylesheet" href="/static/base.css">`, `<link rel="stylesheet" href="/static/site.css">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shell lacks %s", want)
		}
	}
	for _, f := range []string{"/static/base.css", "/static/site.css", "/static/admin.css", "/static/grain.png"} {
		if rec := get(s, f); rec.Code != 200 {
			t.Errorf("%s: %d", f, rec.Code)
		}
	}
}

// The transcript's meta row lists the published pages, numbered, marks the
// current one, and sets the name on two lines.
func TestTranscriptNav(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	s.store.SavePage(ctx, content.Page{Slug: "about", Title: "About", Body: "About me.", Published: true})
	s.store.SavePage(ctx, content.Page{Slug: "draft", Title: "Draft", Body: "Not yet.", Published: false})

	home := get(s, "/").Body.String()
	for _, want := range []string{
		`<a href="/" aria-current="page"><span class="idx">01</span> Index</a>`,
		`<a href="/about"><span class="idx">02</span> About</a>`,
		`<a href="/experiments/"><span class="idx">03</span> Experiments</a>`,
		`<h1 class="name"><span class="line"><span>Ben</span></span> <span class="line"><span>Priddy</span></span> </h1>`,
		`<span class="idx" aria-hidden="true">01</span>`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("home lacks %s", want)
		}
	}
	if strings.Contains(home, `href="/draft"`) {
		t.Error("an unpublished page is in the nav")
	}
	about := get(s, "/about").Body.String()
	if !strings.Contains(about, `<a href="/about" aria-current="page">`) || strings.Contains(about, `<a href="/" aria-current="page">`) {
		t.Error("/about: wrong current page")
	}
	if !strings.Contains(about, `<h1 class="display">About</h1>`) {
		t.Error("/about: no display title")
	}
	exps := get(s, "/experiments/").Body.String()
	if !strings.Contains(exps, `<a href="/experiments/" aria-current="page">`) {
		t.Error("/experiments/: not marked current")
	}
}

func TestTitleFromPrompt(t *testing.T) {
	for prompt, want := range map[string]string{
		"Make it feel like a quiet gallery":                           "Make it feel like a quiet gallery",
		"make it feel like a quiet gallery, with white walls and art": "Make it feel like a quiet gallery",
		"A calm green page":   "A calm green page",
		"brutalist, with the": "Brutalist",
		"the":                 "The",
		"!!!":                 "Untitled",
		"one two three four five six seven eight": "One two three four five six seven",
	} {
		if got := titleFromPrompt(prompt); got != want {
			t.Errorf("titleFromPrompt(%q) = %q, want %q", prompt, got, want)
		}
	}
}

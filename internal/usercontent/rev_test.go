package usercontent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Revision refs ("rev/<id>") are served from <FRONTENDS_DIR>/rev/<id>/ locally.
func TestRevisionRefs(t *testing.T) {
	fe := fixture(t)
	for p, body := range map[string]string{
		"rev/abcdefgh1/index.html": "<!doctype html><title>rev one</title>",
		"rev/abcdefgh1/js/app.js":  "console.log(1)",
		"rev/zzzzzzzz2/index.html": "OTHER " + secret,
	} {
		full := filepath.Join(fe, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(Options{SigningKey: testKey, MainOrigin: testOrigin, Source: NewDirSource(fe), Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}

	tk := tok("rev/abcdefgh1", t0)
	if w := do(h, "/t/"+tk+"/", iframe); w.Code != 200 || w.Body.String() != "<!doctype html><title>rev one</title>" {
		t.Fatalf("rev index: %d %q", w.Code, w.Body)
	} else {
		checkTokenHeaders(t, w, "no-store")
	}
	if w := do(h, "/t/"+tk+"/js/app.js", nil); w.Code != 200 || w.Body.String() != "console.log(1)" {
		t.Fatalf("rev asset: %d %q", w.Code, w.Body)
	}
	// the index still needs an iframe fetch
	if w := do(h, "/t/"+tk+"/", nil); w.Code != 403 {
		t.Errorf("rev index outside iframe: %d", w.Code)
	}
	// a revision can't reach another revision's files or the builtins
	for _, p := range []string{"/../zzzzzzzz2/index.html", "/../../builtin/demo/app.js", "/../../secret.txt"} {
		w := do(h, "/t/"+tk+p, nil)
		if w.Code == 200 {
			t.Errorf("%s: served", p)
		}
		noLeak(t, w)
	}
	if w := do(h, "/t/"+tk+"/missing.js", nil); w.Code != 404 {
		t.Errorf("missing rev file: %d", w.Code)
	}
	// front-end IDs and malformed revision refs aren't servable
	for _, ref := range []string{"fe/abcdefgh1", "rev/abc", "rev/ABCDEFGH1", "rev/abcdefgh1/js", "rev/../builtin/demo", "rev/"} {
		w := do(h, "/t/"+tok(ref, t0)+"/index.html", iframe)
		if w.Code != 403 {
			t.Errorf("ref %q: code %d, want 403", ref, w.Code)
		}
		noLeak(t, w)
	}
	// an unknown (well-formed) revision is simply not there
	if w := do(h, "/t/"+tok("rev/nosuchrev1", t0)+"/", iframe); w.Code != 404 {
		t.Errorf("unknown revision: %d", w.Code)
	}
}

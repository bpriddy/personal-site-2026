package builder

import (
	"strings"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/revfiles"
)

// The "smoke" build (2026-10-10): app.js, a plain script, did
// import("./smoke.js"), which can't resolve in the sandbox. Validate refuses
// it; a module script, or an absolute URL, is fine.
func TestClassicRelativeImports(t *testing.T) {
	idx := func(tag string) []byte {
		return []byte(`<!doctype html><script src="/site-host.js"></script>` + tag)
	}
	cases := []struct {
		name  string
		index []byte
		app   string
		bad   bool
	}{
		{"classic + relative import", idx(`<script src="app.js"></script>`), `if (x) import("./smoke.js").then(m => m.start());`, true},
		{"classic + ./ path + single quotes", idx(`<script defer src="./app.js"></script>`), `import('../lib/x.js')`, true},
		{"module script", idx(`<script type="module" src="app.js"></script>`), `import("./smoke.js")`, false},
		{"classic + absolute URL", idx(`<script src="app.js"></script>`), `import(new URL("./smoke.js", document.baseURI).href)`, false},
		{"classic, no import", idx(`<script src="app.js"></script>`), `site.ready()`, false},
	}
	for _, c := range cases {
		files := revfiles.Files{"index.html": c.index, "app.js": []byte(c.app)}
		got := ClassicRelativeImports(files)
		if (len(got) > 0) != c.bad {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
	files := revfiles.Files{"index.html": idx(`<script src="app.js"></script>`), "app.js": []byte(`import("./smoke.js"); site.ready(); site.loaded;`)}
	if err := Validate(files); err == nil || !strings.Contains(err.Error(), `type="module"`) {
		t.Errorf("Validate: %v", err)
	}
}

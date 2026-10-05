package builder

import (
	"strings"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/revfiles"
)

func TestSyntaxErrors(t *testing.T) {
	index := func(body string) []byte {
		return []byte(`<!doctype html><html><head>` + HostScriptTag + "\n</head><body>\n" + body + "\n</body></html>")
	}
	for _, tc := range []struct {
		name  string
		files revfiles.Files
		want  string // a substring of the one error, or "" for none
	}{
		{"modern code parses", revfiles.Files{
			"index.html": index(`<script type="module">import { a } from "./a.js"; await site.loaded; site.ready?.();</script>`),
			"a.js":       []byte("export class A { #x = 1; static { this.y ??= 2; } }\nexport const a = async () => [...new A()];\n"),
		}, ""},
		{"a duplicate declaration", revfiles.Files{
			"index.html": index(`<script>let kick = 1;
function f() {}
const kick = 2;</script>`),
		}, `index.html:5:7`},
		{"a missing comma before a function", revfiles.Files{
			"app.js": []byte("const o = {\n  a: 1\n  b() {}\n};\n"),
		}, `app.js:3:3`},
		{"data and shader scripts aren't JavaScript", revfiles.Files{
			"index.html": index(`<script type="application/json">{"a": [1,}</script><script type="x-shader/x-fragment">void main() {}</script>`),
		}, ""},
		{"external scripts have no inline body", revfiles.Files{
			"index.html": index(`<script src="app.js"></script>`),
			"app.js":     []byte("site.ready();\n"),
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := SyntaxErrors(tc.files)
			if tc.want == "" {
				if len(errs) > 0 {
					t.Fatalf("want no errors, got %v", errs)
				}
				return
			}
			if len(errs) == 0 || !strings.Contains(errs[0], tc.want) {
				t.Fatalf("want an error at %q, got %v", tc.want, errs)
			}
		})
	}
}

func TestValidateRefusesUnparsableScripts(t *testing.T) {
	files := revfiles.Files{
		"index.html": []byte("<!doctype html>" + HostScriptTag + `<script src="app.js"></script>`),
		"app.js":     []byte("site.ready(\nfunction x() {}\n"),
	}
	err := Validate(files)
	if err == nil || !strings.Contains(err.Error(), "won't parse") || !strings.Contains(err.Error(), "app.js:") {
		t.Fatalf("Validate = %v, want a parse error in app.js", err)
	}
}

package builder

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/bpriddy/personal-site-2026/internal/revfiles"
)

// maxSyntaxErrors is how many parse errors are reported per script: the first
// is usually the cause and the rest follow from it.
const maxSyntaxErrors = 3

// scriptTag matches an inline or external <script> element in HTML.
var scriptTag = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)

// scriptType reads a script tag's type attribute ("" when absent).
var scriptType = regexp.MustCompile(`(?i)\btype\s*=\s*["']?([^"'\s>]+)`)

// SyntaxErrors parses every script a front end ships (.js and .mjs files,
// and inline scripts in .html files) and reports what a browser would refuse
// to run: a script that doesn't parse runs none of its code, so the front end
// never signals ready and visitors get the fallback instead.
func SyntaxErrors(files revfiles.Files) []string {
	var out []string
	for _, name := range sortedNames(files) {
		body := string(files[name])
		switch strings.ToLower(path.Ext(name)) {
		case ".js", ".mjs":
			out = append(out, parseJS(name, body, 0)...)
		case ".html", ".htm":
			for _, m := range scriptTag.FindAllStringSubmatchIndex(body, -1) {
				attrs, code := body[m[2]:m[3]], body[m[4]:m[5]]
				if strings.TrimSpace(code) == "" || !isJSType(attrs) {
					continue
				}
				// report lines as lines of the HTML file
				out = append(out, parseJS(name, code, strings.Count(body[:m[4]], "\n"))...)
			}
		}
	}
	return out
}

// isJSType reports whether a script tag's type means JavaScript (no type,
// "module", or a JavaScript MIME type), not data such as JSON or a shader.
func isJSType(attrs string) bool {
	m := scriptType.FindStringSubmatch(attrs)
	if m == nil {
		return true
	}
	switch t := strings.ToLower(m[1]); t {
	case "module", "text/javascript", "application/javascript", "text/ecmascript", "application/ecmascript":
		return true
	}
	return false
}

// parseJS returns name:line:col: message for each parse error in code,
// whose first line is line lineOffset+1 of the file.
func parseJS(name, code string, lineOffset int) []string {
	res := api.Transform(code, api.TransformOptions{
		Loader:   api.LoaderJS,
		Target:   api.ESNext,
		LogLevel: api.LogLevelSilent,
	})
	var out []string
	for i, e := range res.Errors {
		if i == maxSyntaxErrors {
			break
		}
		loc := name
		if e.Location != nil {
			loc = fmt.Sprintf("%s:%d:%d", name, e.Location.Line+lineOffset, e.Location.Column+1)
		}
		out = append(out, fmt.Sprintf("%s: %s", loc, e.Text))
	}
	return out
}

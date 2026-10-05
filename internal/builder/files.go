package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bpriddy/personal-site-2026/internal/revfiles"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// Limits on a revision's files.
const (
	MaxFiles     = 40
	MaxFileBytes = 512 << 10 // per file
	MaxTotal     = 2 << 20   // all files
	MaxPathLen   = 120
)

// HostScriptTag must appear in index.html (docs/frontend-protocol.md).
const HostScriptTag = `<script src="/site-host.js"></script>`

// allowed file extensions: text formats a front end is made of
var allowedExt = map[string]bool{
	".html": true, ".htm": true, ".js": true, ".mjs": true, ".css": true, ".json": true,
	".wgsl": true, ".glsl": true, ".txt": true, ".svg": true, ".md": true,
}

// CheckPath reports why name can't be a front-end file path, or nil: it must be
// relative and slash-separated, with segments of [A-Za-z0-9._-] that don't
// start with ".", and an allowed text extension.
func CheckPath(name string) error {
	switch {
	case name == "":
		return errors.New("empty path")
	case len(name) > MaxPathLen:
		return fmt.Errorf("path longer than %d characters", MaxPathLen)
	case strings.HasPrefix(name, "/"):
		return errors.New("path must be relative (no leading /)")
	case path.Clean(name) != name:
		return errors.New("path must be clean (no .., ./, // or trailing /)")
	}
	for seg := range strings.SplitSeq(name, "/") {
		if seg == "" || seg[0] == '.' {
			return errors.New("path segments may not be empty or start with '.'")
		}
		for _, r := range seg {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
				return fmt.Errorf("path may only contain letters, digits, '.', '_', '-' and '/' (got %q)", r)
			}
		}
	}
	if !allowedExt[strings.ToLower(path.Ext(name))] {
		return fmt.Errorf("file type %q not allowed (use .html, .js, .mjs, .css, .json, .wgsl, .glsl, .svg or .txt)", path.Ext(name))
	}
	return nil
}

// Validate checks a complete set of files before it becomes a revision.
func Validate(files revfiles.Files) error {
	var problems []string
	if len(files) == 0 {
		return errors.New("no files")
	}
	if len(files) > MaxFiles {
		problems = append(problems, fmt.Sprintf("%d files; at most %d allowed", len(files), MaxFiles))
	}
	total := 0
	for _, name := range sortedNames(files) {
		body := files[name]
		if err := CheckPath(name); err != nil {
			problems = append(problems, name+": "+err.Error())
		}
		if len(body) > MaxFileBytes {
			problems = append(problems, fmt.Sprintf("%s: %d bytes; at most %d per file", name, len(body), MaxFileBytes))
		}
		if !utf8.Valid(body) {
			problems = append(problems, name+": not valid UTF-8 text")
		}
		total += len(body)
	}
	if total > MaxTotal {
		problems = append(problems, fmt.Sprintf("%d bytes in total; at most %d", total, MaxTotal))
	}
	index, ok := files["index.html"]
	switch {
	case !ok:
		problems = append(problems, "index.html is missing")
	case !strings.Contains(string(index), HostScriptTag):
		problems = append(problems, "index.html must include "+HostScriptTag+" (before any other script)")
	}
	if ok && !anyContains(files, "site.ready(") {
		problems = append(problems, "no call to site.ready(): the parent replaces front ends that never signal ready")
	}
	if errs := SyntaxErrors(files); len(errs) > 0 {
		problems = append(problems, "scripts that won't parse (a browser runs none of their code, so the site falls back): "+strings.Join(errs, "; "))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// Warnings are soft checks: the files are accepted, but the model is told.
func Warnings(files revfiles.Files) []string {
	var out []string
	if !anyContains(files, "site.loaded") {
		out = append(out, "nothing awaits site.loaded, so the content may be read before it arrives")
	}
	if !anyContains(files, "site.field(") {
		out = append(out, "no site.field(...) calls: read displayed item fields through site.field so missing content is reported and healed")
	}
	if !anyContains(files, "site.navigate(") {
		out = append(out, "no site.navigate(...) calls: visitors need a way to reach the other pages")
	}
	if !anyContains(files, "site.onRoute(") {
		out = append(out, "no site.onRoute(...) listener: the front end won't follow navigation or back/forward")
	}
	for _, bad := range []string{"localStorage", "sessionStorage", "indexedDB", "document.cookie", "history.pushState", "location.href ="} {
		if anyContains(files, bad) {
			out = append(out, "uses "+bad+", which is unavailable or forbidden in the sandbox")
		}
	}
	return out
}

func anyContains(files revfiles.Files, s string) bool {
	for _, b := range files {
		if strings.Contains(string(b), s) {
			return true
		}
	}
	return false
}

func sortedNames(files revfiles.Files) []string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Manifest describes files for the revision's database row.
func Manifest(files revfiles.Files) []store.FileInfo {
	out := make([]store.FileInfo, 0, len(files))
	for _, n := range sortedNames(files) {
		sum := sha256.Sum256(files[n])
		out = append(out, store.FileInfo{Path: n, Size: int64(len(files[n])), SHA256: hex.EncodeToString(sum[:])})
	}
	return out
}

// clone copies files so the working copy never aliases the parent's.
func clone(files revfiles.Files) revfiles.Files {
	out := make(revfiles.Files, len(files))
	for n, b := range files {
		out[n] = slices.Clone(b)
	}
	return out
}

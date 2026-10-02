package media

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testDir lays out a MEDIA_DIR with a few files, a dotfile, a directory, a
// symlink out of the root and a file next to (outside) the root.
func testDir(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "media")
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("projects/a/loop-1.mp4", "0123456789abcdef")
	write("projects/a/hero.jpg", "jpeg")
	write("projects/a/x.png", "png")
	write("projects/a/x.gif", "gif")
	write("projects/a/x.webp", "webp")
	write("projects/a/.secret.jpg", "hidden")
	write("projects/a/notes.txt", "text")
	write("projects/a/dir.jpg/inner.jpg", "nested")
	if err := os.WriteFile(filepath.Join(parent, "outside.jpg"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(parent, "outside.jpg"), filepath.Join(root, "projects", "a", "link.jpg")); err != nil {
		t.Fatal(err)
	}
	return root
}

func serve(h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerTypesAndHeaders(t *testing.T) {
	h := New(Dir{Root: testDir(t)}, nil)
	for path, ctype := range map[string]string{
		"/media/projects/a/loop-1.mp4": "video/mp4",
		"/media/projects/a/hero.jpg":   "image/jpeg",
		"/media/projects/a/x.png":      "image/png",
		"/media/projects/a/x.gif":      "image/gif",
		"/media/projects/a/x.webp":     "image/webp",
	} {
		rec := serve(h, "GET", path, nil)
		if rec.Code != 200 {
			t.Errorf("%s: %d", path, rec.Code)
			continue
		}
		hd := rec.Header()
		if hd.Get("Content-Type") != ctype {
			t.Errorf("%s: Content-Type %q, want %q", path, hd.Get("Content-Type"), ctype)
		}
		for k, v := range map[string]string{
			"Cache-Control":                "public, max-age=31536000, immutable",
			"Access-Control-Allow-Origin":  "*",
			"Cross-Origin-Resource-Policy": "cross-origin",
			"X-Content-Type-Options":       "nosniff",
			"Accept-Ranges":                "bytes",
		} {
			if hd.Get(k) != v {
				t.Errorf("%s: %s = %q, want %q", path, k, hd.Get(k), v)
			}
		}
	}
	// HEAD: headers, no body
	rec := serve(h, "HEAD", "/media/projects/a/loop-1.mp4", nil)
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "16" {
		t.Errorf("HEAD: %d len=%d cl=%q", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Length"))
	}
	// methods
	if rec := serve(h, "POST", "/media/projects/a/hero.jpg", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}

func TestHandlerRange(t *testing.T) {
	h := New(Dir{Root: testDir(t)}, nil)
	rec := serve(h, "GET", "/media/projects/a/loop-1.mp4", map[string]string{"Range": "bytes=4-7"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "4567" || rec.Header().Get("Content-Range") != "bytes 4-7/16" {
		t.Errorf("range: %d %q %q", rec.Code, rec.Body, rec.Header().Get("Content-Range"))
	}
	if rec.Header().Get("Content-Type") != "video/mp4" {
		t.Errorf("range Content-Type %q", rec.Header().Get("Content-Type"))
	}
	rec = serve(h, "GET", "/media/projects/a/loop-1.mp4", map[string]string{"Range": "bytes=100-"})
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("unsatisfiable range: %d", rec.Code)
	}
}

func TestHandlerRefuses(t *testing.T) {
	h := New(Dir{Root: testDir(t)}, nil)
	for _, path := range []string{
		"/media/projects/a/missing.jpg",
		"/media/projects/a/.secret.jpg",        // dotfile
		"/media/projects/a/notes.txt",          // not a media type
		"/media/projects/a/dir.jpg",            // a directory
		"/media/projects/a/link.jpg",           // symlink out of the root
		"/media/projects/../../outside.jpg",    // traversal
		"/media/projects/a/..%2f..%2fouts.jpg", // encoded traversal
		"/media/projects//a/hero.jpg",          // empty segment
		"/media/projects/a/hero.jpg/",          // trailing slash
		"/media/",
		"/media/projects/a/hero%00.jpg",
		"/media/projects/a/he%20ro.jpg",
		"/other/projects/a/hero.jpg",
	} {
		rec := serve(h, "GET", path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") {
			t.Errorf("%s: a 404 cached as immutable (%q)", path, cc)
		}
		if rec.Header().Get("Content-Type") == "video/mp4" || rec.Header().Get("Content-Type") == "image/jpeg" {
			t.Errorf("%s: 404 with a media type", path)
		}
	}
}

// a source that fails
type brokenSource struct{ err error }

func (b brokenSource) Open(context.Context, string) (*File, error) { return nil, b.err }

func TestHandlerSourceErrors(t *testing.T) {
	for _, err := range []error{fs.ErrNotExist, errors.New("bucket down"), ErrTooLarge} {
		rec := serve(New(brokenSource{err}, nil), "GET", "/media/projects/a/hero.jpg", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%v: %d", err, rec.Code)
		}
	}
}

func TestDirMissingRoot(t *testing.T) {
	_, err := Dir{Root: filepath.Join(t.TempDir(), "nope")}.Open(context.Background(), "projects/a/hero.jpg")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
	if _, err := (Dir{Root: t.TempDir()}).Open(context.Background(), "../x.jpg"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("traversal err = %v", err)
	}
}

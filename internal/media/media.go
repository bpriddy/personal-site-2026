// Package media serves project and experiment media (stills and loops) at
// GET/HEAD /media/<name>, from both the main site (for the HTML transcript)
// and the user-content service (for sandboxed front ends, whose CSP allows
// only same-origin images and video). See docs/frontend-protocol.md, "v1.4".
//
// Files come from Cloud Storage when FRONTENDS_BUCKET is set (the objects
// media/<name>), else from a local directory (MEDIA_DIR, dev default
// build/media: the file <dir>/<name>). They are immutable: a file name is
// never reused for new content, so responses are cached for a year.
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/storage"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

// MaxBytes caps one media file: they are small loops and stills, read whole
// into memory to serve ranges from the bucket.
const MaxBytes = 32 << 20

// ObjectPrefix is where media lives in the bucket.
const ObjectPrefix = "media/"

const cacheControl = "public, max-age=31536000, immutable"

// ErrTooLarge is returned for a file over MaxBytes.
var ErrTooLarge = errors.New("media: file too large")

// Source opens media files by name (the URL path after /media/, already
// validated by content.MediaName). A missing file is an error wrapping
// fs.ErrNotExist.
type Source interface {
	Open(ctx context.Context, name string) (*File, error)
}

// File is an open media file. The caller calls Close.
type File struct {
	Body    io.ReadSeeker
	ModTime time.Time
	close   func() error
}

// Close releases the file.
func (f *File) Close() error {
	if f.close == nil {
		return nil
	}
	return f.close()
}

// Dir serves files from a local directory.
type Dir struct{ Root string }

func (d Dir) Open(_ context.Context, name string) (*File, error) {
	if !ServableName(name) {
		return nil, fs.ErrNotExist
	}
	// os.OpenInRoot confines the open (symlinks included) to Root
	f, err := os.OpenInRoot(d.Root, filepath.FromSlash(name))
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return nil, fmt.Errorf("%w: %v", fs.ErrNotExist, err)
		}
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	if fi.Size() > MaxBytes {
		f.Close()
		return nil, ErrTooLarge
	}
	return &File{Body: f, ModTime: fi.ModTime(), close: f.Close}, nil
}

// GCS serves the objects media/<name> of a bucket.
type GCS struct{ Bucket *storage.BucketHandle }

// NewGCS opens bucket with Application Default Credentials (on Cloud Run,
// the service's own account, which needs object read).
func NewGCS(ctx context.Context, bucket string) (*GCS, error) {
	c, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("media: storage client: %w", err)
	}
	return &GCS{Bucket: c.Bucket(bucket)}, nil
}

func (g *GCS) Open(ctx context.Context, name string) (*File, error) {
	if !ServableName(name) {
		return nil, fs.ErrNotExist
	}
	r, err := g.Bucket.Object(ObjectPrefix + name).NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, fmt.Errorf("%w: %v", fs.ErrNotExist, err)
		}
		return nil, err
	}
	defer r.Close()
	if r.Attrs.Size > MaxBytes {
		return nil, ErrTooLarge
	}
	b, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxBytes {
		return nil, ErrTooLarge
	}
	return &File{Body: bytes.NewReader(b), ModTime: r.Attrs.LastModified}, nil
}

// AssetPrefix is where the builder's imported assets live (3D models, fonts,
// textures from its connections: internal/connect), next to the content's
// media. Asset names are media names under assets/, plus these extensions.
const AssetPrefix = "assets/"

var assetExts = map[string]bool{".glb": true, ".woff2": true, ".jpg": true, ".jpeg": true, ".png": true, ".webp": true}

// ServableName reports whether name can be served at /media/<name>: a
// content media name (content.MediaName), or an imported asset.
func ServableName(name string) bool {
	if content.MediaName(name) {
		return true
	}
	rest, ok := strings.CutPrefix(name, AssetPrefix)
	if !ok || rest == "" || len(name) > 200 || path.Clean(name) != name {
		return false
	}
	for _, s := range strings.Split(rest, "/") {
		if !assetSeg(s) {
			return false
		}
	}
	return assetExts[strings.ToLower(path.Ext(name))]
}

func assetSeg(s string) bool {
	if s == "" || !(s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z' || s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// Writer stores media (the builder's imported assets). Names are immutable:
// a name is never reused for different content.
type Writer interface {
	Put(ctx context.Context, name string, body []byte, contentType string) error
	Exists(ctx context.Context, name string) (bool, error)
}

func (d Dir) Put(_ context.Context, name string, body []byte, _ string) error {
	if !ServableName(name) {
		return fmt.Errorf("media: bad name %q", name)
	}
	p := filepath.Join(d.Root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, body, 0o644)
}

func (d Dir) Exists(_ context.Context, name string) (bool, error) {
	if !ServableName(name) {
		return false, nil
	}
	fi, err := os.Stat(filepath.Join(d.Root, filepath.FromSlash(name)))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil && fi.Mode().IsRegular(), err
}

func (g *GCS) Put(ctx context.Context, name string, body []byte, contentType string) error {
	if !ServableName(name) {
		return fmt.Errorf("media: bad name %q", name)
	}
	w := g.Bucket.Object(ObjectPrefix + name).NewWriter(ctx)
	w.ContentType = contentType
	w.CacheControl = cacheControl
	if _, err := w.Write(body); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func (g *GCS) Exists(ctx context.Context, name string) (bool, error) {
	if !ServableName(name) {
		return false, nil
	}
	_, err := g.Bucket.Object(ObjectPrefix + name).Attrs(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return false, nil
	}
	return err == nil, err
}

// ContentType is the type /media/ serves name as ("" if not servable).
func ContentType(name string) string { return contentTypes[strings.ToLower(path.Ext(name))] }

// contentTypes are the only types served; anything else is a 404.
var contentTypes = map[string]string{
	".glb":   "model/gltf-binary",
	".woff2": "font/woff2",
	".mp4":   "video/mp4",
	".webm":  "video/webm",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".png":   "image/png",
	".gif":   "image/gif",
	".webp":  "image/webp",
}

// Handler serves GET and HEAD /media/<name> from a Source.
type Handler struct {
	Source Source
	Log    *slog.Logger // nil discards
}

// New returns a Handler for src.
func New(src Source, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Handler{Source: src, Log: log}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	// readable from any origin (the sandboxed front ends' origin is opaque)
	hd.Set("Access-Control-Allow-Origin", "*")
	hd.Set("Cross-Origin-Resource-Policy", "cross-origin")
	hd.Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		hd.Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name, ok := strings.CutPrefix(r.URL.Path, content.MediaPrefix)
	ctype := contentTypes[strings.ToLower(path.Ext(name))]
	if !ok || ctype == "" || !ServableName(name) {
		h.notFound(w)
		return
	}
	f, err := h.Source.Open(r.Context(), name)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			h.Log.Error("media: open", "name", name, "err", err)
		}
		h.notFound(w)
		return
	}
	defer f.Close()
	hd.Set("Content-Type", ctype)
	hd.Set("Cache-Control", cacheControl)
	// ServeContent: Range, If-Modified-Since, HEAD, Content-Length
	http.ServeContent(w, r, name, f.ModTime, f.Body)
}

// notFound is a plain, briefly cached 404 (a file uploaded later appears
// within a minute).
func (h *Handler) notFound(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, max-age=60")
	http.Error(w, "not found", http.StatusNotFound)
}

// FromEnv picks the source: the bucket when set, else dir.
func FromEnv(ctx context.Context, bucket, dir string) (Source, error) {
	if bucket != "" {
		return NewGCS(ctx, bucket)
	}
	return Dir{Root: dir}, nil
}

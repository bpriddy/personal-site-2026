// Package revfiles stores the files of prompted front-end revisions: under
// <FRONTENDS_DIR>/rev/<id>/ locally, or as the Cloud Storage objects
// rev/<id>/<path> in FRONTENDS_BUCKET. The user-content service serves them
// (internal/usercontent); the builder writes and reads them. Revisions are
// immutable: writing an existing revision is an error.
package revfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/bpriddy/personal-site-2026/internal/frontend"
)

// Files maps a clean relative path ("index.html", "js/app.js") to contents.
type Files map[string][]byte

// Store reads and writes revision files.
type Store interface {
	// Write stores a new revision's files. It fails if the revision already
	// has files (revisions are immutable).
	Write(ctx context.Context, revID string, files Files) error
	// Read returns all of a revision's files; fs.ErrNotExist if it has none.
	Read(ctx context.Context, revID string) (Files, error)
}

// ErrExists is returned by Write for a revision that already has files.
var ErrExists = errors.New("revfiles: revision already exists")

// MaxReadBytes bounds Read, so a corrupt revision can't exhaust memory.
const MaxReadBytes = 32 << 20

func checkID(revID string) error {
	if !frontend.ValidRevisionID(revID) {
		return fmt.Errorf("revfiles: invalid revision id %q", revID)
	}
	return nil
}

// Dir stores revisions under <Root>/rev/<id>/.
type Dir struct{ Root string }

func NewDir(root string) *Dir { return &Dir{Root: root} }

func (d *Dir) Write(_ context.Context, revID string, files Files) error {
	if err := checkID(revID); err != nil {
		return err
	}
	revs := filepath.Join(d.Root, "rev")
	if err := os.MkdirAll(revs, 0o755); err != nil {
		return err
	}
	final := filepath.Join(revs, revID)
	if _, err := os.Stat(final); err == nil {
		return ErrExists
	}
	// write into a temp dir and rename it into place, so a revision is
	// never visible half-written
	tmp, err := os.MkdirTemp(revs, ".tmp-"+revID+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	root, err := os.OpenRoot(tmp)
	if err != nil {
		return err
	}
	defer root.Close()
	for name, body := range files {
		if dir := filepath.Dir(filepath.FromSlash(name)); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		f, err := root.OpenFile(filepath.FromSlash(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, werr := f.Write(body)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		if _, serr := os.Stat(final); serr == nil {
			return ErrExists
		}
		return err
	}
	return nil
}

func (d *Dir) Read(_ context.Context, revID string) (Files, error) {
	if err := checkID(revID); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Join(d.Root, "rev", revID))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", fs.ErrNotExist, err)
	}
	defer root.Close()
	out := Files{}
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		if !e.Type().IsRegular() {
			return nil
		}
		b, err := fs.ReadFile(root.FS(), p)
		if err != nil {
			return err
		}
		if total += int64(len(b)); total > MaxReadBytes {
			return fmt.Errorf("revfiles: revision %s is larger than %d bytes", revID, MaxReadBytes)
		}
		out[p] = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GCS stores revisions as the objects rev/<id>/<path> in a bucket.
type GCS struct {
	Bucket *storage.BucketHandle
}

// NewGCS opens a bucket with Application Default Credentials (on Cloud Run,
// the service's own service account).
func NewGCS(ctx context.Context, bucket string) (*GCS, error) {
	c, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("revfiles: storage client: %w", err)
	}
	return &GCS{Bucket: c.Bucket(bucket)}, nil
}

// ObjectName is where a revision file lives in the bucket.
func ObjectName(revID, name string) string { return "rev/" + revID + "/" + name }

func (g *GCS) Write(ctx context.Context, revID string, files Files) error {
	if err := checkID(revID); err != nil {
		return err
	}
	// Objects are written one by one; the revision only becomes reachable
	// once the builder stores its database row, after every file is written.
	for name, body := range files {
		w := g.Bucket.Object(ObjectName(revID, name)).If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
		w.ContentType = ContentType(name)
		if _, err := w.Write(body); err != nil {
			w.Close()
			return err
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("revfiles: write %s: %w", name, err)
		}
	}
	return nil
}

func (g *GCS) Read(ctx context.Context, revID string) (Files, error) {
	if err := checkID(revID); err != nil {
		return nil, err
	}
	prefix := "rev/" + revID + "/"
	it := g.Bucket.Objects(ctx, &storage.Query{Prefix: prefix})
	out := Files{}
	var total int64
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		if total += attrs.Size; total > MaxReadBytes {
			return nil, fmt.Errorf("revfiles: revision %s is larger than %d bytes", revID, MaxReadBytes)
		}
		r, err := g.Bucket.Object(attrs.Name).NewReader(ctx)
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(r, MaxReadBytes))
		r.Close()
		if err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(attrs.Name, prefix)] = b
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: revision %s", fs.ErrNotExist, revID)
	}
	return out, nil
}

// ContentType is the media type stored with an object (the user-content
// service sets its own on serving; this is for the console and gsutil).
func ContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".wgsl", ".txt", ".glsl":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

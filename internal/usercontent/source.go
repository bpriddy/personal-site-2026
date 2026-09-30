package usercontent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/frontend"
)

// FileSource stores front-end files, laid out as <ref>/<name>. The local
// directory is the only implementation today; Cloud Storage replaces it later.
//
// Callers pass a ref that has passed frontend.Valid and a name that is a clean,
// relative, slash-separated path with no "..", dot-segments or empty segments.
// Implementations must still refuse to escape <ref>/. A missing file, or a
// name that is a directory, is reported as an error wrapping fs.ErrNotExist.
type FileSource interface {
	Open(ctx context.Context, ref frontend.Ref, name string) (*Object, error)
}

// Object is an open file. The caller closes Body.
type Object struct {
	Body    io.ReadCloser
	Size    int64 // -1 if unknown
	ModTime time.Time
}

// DirSource serves files from a local directory: ref "builtin/x" maps to
// <Dir>/builtin/x/.
type DirSource struct {
	Dir string
}

func NewDirSource(dir string) *DirSource { return &DirSource{Dir: dir} }

func (s *DirSource) Open(_ context.Context, ref frontend.Ref, name string) (*Object, error) {
	if !frontend.Valid(ref) {
		return nil, fs.ErrNotExist
	}
	// os.OpenInRoot confines the open (including symlinks) to the ref's own
	// directory, so even a path-checking bug upstream can't reach other files.
	f, err := os.OpenInRoot(filepath.Join(s.Dir, filepath.FromSlash(ref)), filepath.FromSlash(name))
	if err != nil {
		// Missing files, missing ref dirs, "app.js/x" (ENOTDIR), and symlinks
		// that escape the root all come back as *PathError; to a client they
		// are all simply not there.
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
	return &Object{Body: f, Size: fi.Size(), ModTime: fi.ModTime()}, nil
}

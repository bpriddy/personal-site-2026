package usercontent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"cloud.google.com/go/storage"

	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
)

// GCSSource serves revision refs ("rev/<id>") from the Cloud Storage objects
// rev/<id>/<name>, and hands every other ref (the built-ins, which ship in
// the image) to Builtins.
type GCSSource struct {
	Bucket   *storage.BucketHandle
	Builtins FileSource
}

// NewGCSSource opens bucket with Application Default Credentials (on Cloud
// Run, the service's own service account, which needs only object read).
func NewGCSSource(ctx context.Context, bucket string, builtins FileSource) (*GCSSource, error) {
	c, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("usercontent: storage client: %w", err)
	}
	return &GCSSource{Bucket: c.Bucket(bucket), Builtins: builtins}, nil
}

func (s *GCSSource) Open(ctx context.Context, ref frontend.Ref, name string) (*Object, error) {
	rev := frontend.RevisionOf(ref)
	if rev == "" {
		return s.Builtins.Open(ctx, ref, name)
	}
	if !cleanName(name) {
		return nil, fs.ErrNotExist
	}
	r, err := s.Bucket.Object(revfiles.ObjectName(rev, name)).NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, fmt.Errorf("%w: %v", fs.ErrNotExist, err)
		}
		return nil, err
	}
	return &Object{Body: r, Size: r.Attrs.Size, ModTime: r.Attrs.LastModified}, nil
}

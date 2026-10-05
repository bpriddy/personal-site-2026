package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // DecodeConfig for attached images
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/media"
)

// Limits on images attached to a builder prompt. The model takes images up
// to 5 MB and 8000 px a side; the builder UIs downscale before sending.
const (
	maxAttachBytes   = 5 << 20
	maxAttachSide    = 8000
	maxAttachBen     = 4
	maxAttachVisitor = 1
	uploadsDir       = "assets/uploads/"
)

// attachTypes are the image formats the model reads, by decoded format name.
var attachTypes = map[string]struct{ mime, ext string }{
	"png":  {"image/png", ".png"},
	"jpeg": {"image/jpeg", ".jpg"},
	"webp": {"image/webp", ".webp"},
	"gif":  {"image/gif", ".gif"},
}

// errAttach is a problem with an attached image the sender can fix.
type errAttach struct{ msg string }

func (e errAttach) Error() string { return e.msg }

// storeAttachments checks the base64 images sent with a prompt and writes
// them to the media store (content-addressed under assets/uploads/), so the
// front end can use them by /media/ path. An errAttach says what's wrong.
func (s *Server) storeAttachments(ctx context.Context, encoded []string, visitor bool) ([]builder.Attachment, error) {
	if len(encoded) == 0 {
		return nil, nil
	}
	max := maxAttachBen
	if visitor {
		max = maxAttachVisitor
	}
	if len(encoded) > max {
		return nil, errAttach{fmt.Sprintf("Attach at most %d image(s).", max)}
	}
	w, ok := s.media.(media.Writer)
	if !ok {
		return nil, errors.New("the media store isn't writable")
	}
	var out []builder.Attachment
	for _, e := range encoded {
		data, err := base64.StdEncoding.DecodeString(e)
		if err != nil {
			return nil, errAttach{"An attached image couldn't be read."}
		}
		if len(data) > maxAttachBytes {
			return nil, errAttach{"An attached image is too large (5 MB at most)."}
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		t, known := attachTypes[format]
		if err != nil || !known {
			return nil, errAttach{"Attach PNG, JPEG, WebP or GIF images."}
		}
		if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxAttachSide || cfg.Height > maxAttachSide {
			return nil, errAttach{fmt.Sprintf("An attached image is too big (%d px a side at most).", maxAttachSide)}
		}
		sum := sha256.Sum256(data)
		name := uploadsDir + hex.EncodeToString(sum[:12]) + t.ext
		if exists, err := w.Exists(ctx, name); err != nil {
			return nil, fmt.Errorf("check upload: %w", err)
		} else if !exists {
			if err := w.Put(ctx, name, data, t.mime); err != nil {
				return nil, fmt.Errorf("store upload: %w", err)
			}
		}
		out = append(out, builder.Attachment{Path: "/media/" + name, MediaType: t.mime, Data: data, Width: cfg.Width, Height: cfg.Height})
	}
	return out, nil
}

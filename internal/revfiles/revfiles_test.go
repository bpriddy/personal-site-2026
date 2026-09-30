package revfiles

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestDirRoundTripAndImmutable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	d := NewDir(root)
	files := Files{"index.html": []byte("<p>hi"), "js/app.js": []byte("x=1"), "a/b/c.wgsl": []byte("fn")}
	if err := d.Write(ctx, "abcdefgh1", files); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "rev", "abcdefgh1", "js", "app.js")); err != nil || string(b) != "x=1" {
		t.Fatalf("on disk: %q %v", b, err)
	}
	got, err := d.Read(ctx, "abcdefgh1")
	if err != nil || len(got) != 3 || string(got["a/b/c.wgsl"]) != "fn" || string(got["index.html"]) != "<p>hi" {
		t.Fatalf("read = %v, %v", got, err)
	}
	if err := d.Write(ctx, "abcdefgh1", Files{"index.html": []byte("new")}); !errors.Is(err, ErrExists) {
		t.Fatalf("rewrite err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "rev", "abcdefgh1", "index.html")); string(b) != "<p>hi" {
		t.Fatalf("revision changed: %q", b)
	}
	if _, err := d.Read(ctx, "zzzzzzzz9"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing read err = %v", err)
	}
	// no temp dirs left behind
	ents, _ := os.ReadDir(filepath.Join(root, "rev"))
	if len(ents) != 1 {
		t.Fatalf("rev dir entries = %v", ents)
	}
}

func TestDirRejectsBadIDsAndEscapes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	d := NewDir(root)
	for _, id := range []string{"", "../x", "ABCDEFGH", "short"} {
		if err := d.Write(ctx, id, Files{"index.html": nil}); err == nil {
			t.Errorf("Write(%q) accepted", id)
		}
	}
	// the builder validates paths first; the root still confines a bad one
	if err := d.Write(ctx, "abcdefgh2", Files{"../../escape.txt": []byte("x")}); err == nil {
		t.Error("escaping path accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); err == nil {
		t.Error("file written outside the revision")
	}
}

func TestObjectName(t *testing.T) {
	if got := ObjectName("abcdefgh1", "js/app.js"); got != "rev/abcdefgh1/js/app.js" {
		t.Fatal(got)
	}
}

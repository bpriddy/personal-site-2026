//go:build live

// Live checks against the real services (go test -tags live ./internal/connect/).
// Not part of the normal suite: they need the network, and Sketchfab import
// needs SKETCHFAB_API_TOKEN.
package connect

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/media"
)

func TestLive(t *testing.T) {
	dir := t.TempDir()
	st := media.Dir{Root: dir}
	ctx := WithBudget(context.Background())

	sf := &Sketchfab{Token: os.Getenv("SKETCHFAB_API_TOKEN"), Store: st}
	out, err := sf.Call(ctx, "sketchfab_search", json.RawMessage(`{"query":"x-wing"}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("sketchfab search:\n%s\nimages: %v", out.Text, out.Images)
	if sf.Token != "" {
		uid := regexp.MustCompile(`uid ([0-9a-f]{32})`).FindStringSubmatch(out.Text)
		if uid == nil {
			t.Fatal("no uid in results")
		}
		imp, err := sf.Call(ctx, "sketchfab_import", json.RawMessage(`{"uid":"`+uid[1]+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("sketchfab import:\n%s", imp.Text)
	}

	ph := &PolyHaven{Store: st}
	out, err = ph.Call(ctx, "polyhaven_search_textures", json.RawMessage(`{"query":"weathered wood"}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("polyhaven search:\n%s", out.Text)
	id := regexp.MustCompile(`id ([a-z0-9_]+)`).FindStringSubmatch(out.Text)
	if id == nil {
		t.Fatal("no texture id")
	}
	out, err = ph.Call(ctx, "polyhaven_import_texture", json.RawMessage(`{"id":"`+id[1]+`","resolution":"1k"}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("polyhaven import:\n%s", out.Text)

	gf := &GoogleFonts{Store: st}
	out, err = gf.Call(ctx, "google_fonts_import", json.RawMessage(`{"family":"Space Grotesk","axes":"wght@300..700"}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("google fonts:\n%.900s", out.Text)
}

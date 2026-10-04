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
	"strings"
	"testing"
	"time"

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

func TestLiveKrea(t *testing.T) {
	tok := os.Getenv("KREA_API_KEY")
	if tok == "" {
		t.Skip("KREA_API_KEY unset")
	}
	dir := t.TempDir()
	k := &Krea{Token: tok, Store: media.Dir{Root: dir}}
	out, err := k.Call(WithBudget(context.Background()), "krea_generate_image",
		json.RawMessage(`{"prompt":"a wedge-shaped grey capital starship drifting past a ringed planet, 1977 matte painting, gouache on board, hard key light from the left, deep shadows, ship in the right third, empty dark sky on the left","aspect_ratio":"16:9"}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s\nstored in %s", out.Text, dir)
	if os.Getenv("KEEP_TO") != "" {
		m := regexp.MustCompile(`/media/(\S+)`).FindStringSubmatch(out.Text)
		b, _ := os.ReadFile(dir + "/" + strings.TrimRight(m[1], " (,"))
		os.WriteFile(os.Getenv("KEEP_TO"), b, 0o644)
	}
}

func TestLiveKreaVideo(t *testing.T) {
	tok := os.Getenv("KREA_API_KEY")
	if tok == "" || os.Getenv("LIVE_VIDEO") == "" {
		t.Skip("KREA_API_KEY and LIVE_VIDEO=1 needed (costs money)")
	}
	dir := t.TempDir()
	k := &KreaVideo{Token: tok, Store: media.Dir{Root: dir}}
	start := time.Now()
	out, err := k.Call(WithBudget(context.Background()), "krea_generate_video",
		json.RawMessage(`{"prompt":"a wedge-shaped grey capital starship glides slowly from right to left past a ringed planet, matte painting style, slow camera drift, calm","model":"minimax-h3","aspect_ratio":"16:9","seconds":5,"start_image":""}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s (%s)", out.Text, time.Since(start).Round(time.Second))
	if p := os.Getenv("KEEP_TO"); p != "" {
		m := regexp.MustCompile(`/media/(\S+\.mp4)`).FindStringSubmatch(out.Text)
		b, _ := os.ReadFile(dir + "/" + m[1])
		os.WriteFile(p, b, 0o644)
	}
}

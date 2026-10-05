package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/media"
)

func tinyPNG(t *testing.T, w, h int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// An image sent with a prompt is stored in the media store, shown to the
// model with its /media/ path, and recorded on the conversation.
func TestBuilderChatAttachments(t *testing.T) {
	e := newBuilderServer(t, true)
	mediaDir := t.TempDir()
	e.s.media = media.Dir{Root: mediaDir}
	if rec := e.form("/admin/builder/new", url.Values{"slug": {"pic"}, "title": {"Pic"}}); rec.Code != 303 {
		t.Fatalf("new: %d", rec.Code)
	}
	e.model.Responses = []string{
		builder.ToolUse("1", "write_file", map[string]any{"path": "index.html", "content": fixtureIndex}),
		builder.ToolUse("2", "finish", map[string]any{"summary": "Used it."}),
		builder.ToolUse("3", "finish", map[string]any{"summary": "Used it."}),
	}
	img := tinyPNG(t, 3, 2)
	post := func(images ...string) (int, string) {
		body, _ := json.Marshal(map[string]any{"prompt": "animate this", "parent": "", "images": images})
		rec := e.admin("POST", "/admin/builder/fe/pic/chat", strings.NewReader(string(body)),
			"Content-Type", "application/json", "Sec-Fetch-Site", "same-origin")
		return rec.Code, rec.Body.String()
	}

	code, body := post(img)
	rev := eventOf(sseEvents(t, body), "revision")
	if code != 200 || rev == nil {
		t.Fatalf("chat: %d %s", code, body)
	}
	matches, _ := filepath.Glob(filepath.Join(mediaDir, "assets", "uploads", "*.png"))
	if len(matches) != 1 {
		t.Fatalf("stored uploads = %v", matches)
	}
	path := "/media/assets/uploads/" + filepath.Base(matches[0])
	if b, _ := os.ReadFile(matches[0]); base64.StdEncoding.EncodeToString(b) != img {
		t.Error("the stored upload isn't the attached image")
	}

	// the model got the image and its path in the first message
	first := e.model.Requests[0].Messages[len(e.model.Requests[0].Messages)-1]
	var text string
	var sawImage bool
	for _, c := range first.Content {
		if c.OfText != nil {
			text += c.OfText.Text
		}
		if c.OfImage != nil && c.OfImage.Source.OfBase64 != nil && c.OfImage.Source.OfBase64.Data == img {
			sawImage = true
		}
	}
	if !sawImage || !strings.Contains(text, path+" (3x2)") || !strings.Contains(text, "start_image") {
		t.Errorf("first message: image %v, text %q", sawImage, text)
	}

	// the conversation records it, and the next run's history mentions it
	r, _ := e.st.Revision(context.Background(), rev.Revision)
	turns := builder.ParseConversation(r.Conversation)
	if len(turns) == 0 || len(turns[0].Images) != 1 || turns[0].Images[0] != path {
		t.Errorf("conversation = %+v", turns)
	}

	// refused: not an image, too many, a visitor's second image
	if code, body := post(base64.StdEncoding.EncodeToString([]byte("not an image"))); code != 400 || !strings.Contains(body, "PNG, JPEG, WebP or GIF") {
		t.Errorf("not an image: %d %s", code, body)
	}
	if code, body := post(img, img, img, img, img); code != 400 || !strings.Contains(body, "at most 4") {
		t.Errorf("five images: %d %s", code, body)
	}
	if _, err := e.s.storeAttachments(context.Background(), []string{img, img}, true); err == nil || !strings.Contains(err.Error(), "at most 1") {
		t.Errorf("visitor with two images: %v", err)
	}
}

// The create form uploads its images first, the redirect carries their
// paths to the new chat, and the chat sends them by path.
func TestBuilderCreateWithImages(t *testing.T) {
	e := newBuilderServer(t, true)
	e.s.media = media.Dir{Root: t.TempDir()}
	img := tinyPNG(t, 4, 4)

	body, _ := json.Marshal(map[string]any{"images": []string{img}})
	rec := e.admin("POST", "/admin/builder/uploads", strings.NewReader(string(body)), "Content-Type", "application/json", "Sec-Fetch-Site", "same-origin")
	var up struct{ Paths []string }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &up) != nil || len(up.Paths) != 1 || !uploadPath(up.Paths[0]) {
		t.Fatalf("uploads: %d %s", rec.Code, rec.Body)
	}

	rec = e.form("/admin/builder/new", url.Values{"prompt": {"animate my picture"}, "images": {up.Paths[0] + ",/media/../secret.png"}})
	loc := rec.Header().Get("Location")
	if want := "#start=animate+my+picture&images=" + url.QueryEscape(up.Paths[0]); rec.Code != 303 || !strings.HasSuffix(loc, want) {
		t.Fatalf("new: %d %q, want suffix %q", rec.Code, loc, want)
	}
	slug := strings.TrimPrefix(strings.SplitN(loc, "#", 2)[0], "/admin/builder/fe/")

	e.model.Responses = []string{
		builder.ToolUse("1", "write_file", map[string]any{"path": "index.html", "content": fixtureIndex}),
		builder.ToolUse("2", "finish", map[string]any{"summary": "ok"}),
		builder.ToolUse("3", "finish", map[string]any{"summary": "ok"}),
	}
	body, _ = json.Marshal(map[string]any{"prompt": "animate my picture", "attached": up.Paths})
	rec = e.admin("POST", "/admin/builder/fe/"+slug+"/chat", strings.NewReader(string(body)), "Content-Type", "application/json", "Sec-Fetch-Site", "same-origin")
	if eventOf(sseEvents(t, rec.Body.String()), "revision") == nil {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body)
	}
	var sawImage bool
	for _, c := range e.model.Requests[0].Messages[len(e.model.Requests[0].Messages)-1].Content {
		sawImage = sawImage || (c.OfImage != nil && c.OfImage.Source.OfBase64.Data == img)
	}
	if !sawImage {
		t.Error("the uploaded image didn't reach the model")
	}

	// a path outside the uploads isn't read
	body, _ = json.Marshal(map[string]any{"prompt": "x", "attached": []string{"/media/projects/q/hero.jpg"}})
	rec = e.admin("POST", "/admin/builder/fe/"+slug+"/chat", strings.NewReader(string(body)), "Content-Type", "application/json", "Sec-Fetch-Site", "same-origin")
	if rec.Code != 400 {
		t.Errorf("foreign path: %d %s", rec.Code, rec.Body)
	}
}

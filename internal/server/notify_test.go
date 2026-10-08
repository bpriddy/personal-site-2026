package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/notify"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// gatedModel holds its first turn until released, then plays its script.
type gatedModel struct {
	*builder.ScriptedModel
	started, release chan struct{}
	once             bool
}

func (m *gatedModel) Turn(ctx context.Context, p anthropic.BetaMessageNewParams, emit func(builder.Event)) (*anthropic.BetaMessage, error) {
	if !m.once {
		m.once = true
		m.started <- struct{}{}
		select {
		case <-m.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return m.ScriptedModel.Turn(ctx, p, emit)
}

func newNotifyEnv(t *testing.T) (*builderEnv, *gatedModel, *notify.Outbox) {
	t.Helper()
	t.Setenv(frontend.RotationEnv, "")
	dir := t.TempDir()
	cfg := config.Config{Env: "dev", AdminUser: "admin", AdminPassword: "pw", SigningKey: testKey,
		MainOrigin: "https://example.com", UsercontentOrigin: "http://127.0.0.1:8081", FrontendsDir: dir}
	env := &builderEnv{st: store.NewMemory(), dir: dir, model: &builder.ScriptedModel{}}
	m := &gatedModel{ScriptedModel: env.model, started: make(chan struct{}, 1), release: make(chan struct{})}
	out := &notify.Outbox{}
	s, err := New(cfg, env.st, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithBuilder(builder.New(m, builder.Config{Model: "claude-opus-5-5"}), revfiles.NewDir(dir)), WithMailer(out))
	if err != nil {
		t.Fatal(err)
	}
	env.s = s
	return env, m, out
}

// waitSent waits for the outbox to hold n messages (sending is async).
func waitSent(t *testing.T, out *notify.Outbox, n int) []notify.Message {
	t.Helper()
	for i := 0; i < 200; i++ {
		if got := out.Sent(); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("outbox has %d messages, want %d", len(out.Sent()), n)
	return nil
}

func TestBuildNotifyReady(t *testing.T) {
	e, m, out := newNotifyEnv(t)
	a := e.visitor(1, "192.0.2.1")
	slug := a.create(t, "a calm page")
	notifyURL := "/build/api/fe/" + slug + "/notify"

	if rec := a.post(notifyURL, map[string]string{"email": "ben@example.com"}); rec.Code != http.StatusConflict {
		t.Fatalf("nothing running: %d", rec.Code)
	}

	e.script()
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- a.chatRaw(slug, "SECRET PROMPT WORDS", "") }()
	<-m.started

	st := a.state(t)
	if !st.Notify || st.Frontends[0].Run == nil || st.Frontends[0].Run.Notify != "" {
		t.Fatalf("state before = notify %v run %+v", st.Notify, st.Frontends[0].Run)
	}
	if rec := a.post(notifyURL, map[string]string{"email": "Ben <ben@example.com>"}); rec.Code != 400 {
		t.Errorf("bad address: %d", rec.Code)
	}
	if rec := e.visitor(2, "192.0.2.2").post(notifyURL, map[string]string{"email": "x@example.com"}); rec.Code != 404 {
		t.Errorf("stranger: %d", rec.Code)
	}
	rec := a.post(notifyURL, map[string]string{"email": "ben@Example.COM"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "b•••@example.com") {
		t.Fatalf("notify: %d %s", rec.Code, rec.Body)
	}
	if got := a.state(t).Frontends[0].Run.Notify; got != "b•••@example.com" {
		t.Fatalf("masked in state = %q", got)
	}

	close(m.release)
	if evs := sseEvents(t, (<-done).Body.String()); eventOf(evs, "revision") == nil {
		t.Fatalf("no revision: %+v", evs)
	}
	msgs := waitSent(t, out, 1)
	msg := msgs[0]
	if msg.To != "ben@example.com" || !strings.Contains(msg.Subject, "ready") {
		t.Fatalf("message = %+v", msg)
	}
	if strings.Contains(msg.Text, "SECRET") || strings.Contains(msg.HTML, "SECRET") {
		t.Error("the email carries what the visitor typed")
	}
	i := strings.Index(msg.Text, "https://example.com/build/open/")
	if i < 0 {
		t.Fatalf("no link: %s", msg.Text)
	}
	link := strings.Fields(msg.Text[i:])[0]

	// taken: the address is gone from the store, and it's sent once
	runs, _ := e.st.Runs(context.Background(), "fe/"+slug, 1)
	if _, err := e.st.RunNotify(context.Background(), runs[0].ID); err == nil {
		t.Error("address kept after sending")
	}

	// the link, on another device: signs it in as a, shows the new version
	phone := &visitor{e: e, ip: "192.0.2.9", cookies: map[string]string{}}
	open := phone.do("GET", strings.TrimPrefix(link, "https://example.com"), nil)
	if open.Code != http.StatusFound || open.Header().Get("Location") != "/?build=1" {
		t.Fatalf("open: %d %s", open.Code, open.Header().Get("Location"))
	}
	var sid, live string
	for _, c := range open.Result().Cookies() {
		switch c.Name {
		case sidCookie:
			sid = c.Value
		case liveCookie:
			live = c.Value
		}
	}
	if sid != a.sid || !strings.HasPrefix(live, "fe/"+slug+":") {
		t.Fatalf("open cookies: sid %q live %q", sid, live)
	}
	if bad := phone.do("GET", "/build/open/garbage", nil); bad.Header().Get("Location") != "/?build=expired" {
		t.Errorf("bad link: %d %s", bad.Code, bad.Header().Get("Location"))
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(out.Sent()); n != 1 {
		t.Errorf("%d emails, want 1", n)
	}
}

func TestBuildNotifyFailedAndCanceled(t *testing.T) {
	e, m, out := newNotifyEnv(t)
	a := e.visitor(1, "192.0.2.1")
	slug := a.create(t, "a calm page")
	notifyURL := "/build/api/fe/" + slug + "/notify"

	// a failed build sends the short note
	e.model.Responses = []string{"error:boom"}
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- a.chatRaw(slug, "p", "") }()
	<-m.started
	if rec := a.post(notifyURL, map[string]string{"email": "v@example.com"}); rec.Code != 200 {
		t.Fatalf("notify: %d %s", rec.Code, rec.Body)
	}
	close(m.release)
	<-done
	if msg := waitSent(t, out, 1)[0]; !strings.Contains(msg.Subject, "didn't finish") || !strings.Contains(msg.Text, "/build/open/") {
		t.Fatalf("failed message = %+v", msg)
	}

	// asking, then withdrawing: nothing is sent; asking again works
	m.once, m.release = false, make(chan struct{})
	e.script()
	go func() { done <- a.chatRaw(slug, "p", "") }()
	<-m.started
	a.post(notifyURL, map[string]string{"email": "v@example.com"})
	if rec := a.do("DELETE", notifyURL, nil, "Sec-Fetch-Site", "same-origin"); rec.Code != http.StatusNoContent {
		t.Fatalf("clear: %d", rec.Code)
	}
	if got := a.state(t).Frontends[0].Run.Notify; got != "" {
		t.Fatalf("after clear: %q", got)
	}
	close(m.release)
	<-done
	time.Sleep(50 * time.Millisecond)
	if n := len(out.Sent()); n != 1 {
		t.Fatalf("withdrawn request sent: %d emails", n)
	}
}

func TestBuildNotifyCap(t *testing.T) {
	e, m, out := newNotifyEnv(t)
	ctx := context.Background()
	// the address has had its share today
	e.st.CreatePromptedFrontend(ctx, "fe/other", "Other")
	for i := 0; i < NotifyPerAddressDaily; i++ {
		id, _ := e.st.StartRun(ctx, store.Run{FrontendID: "fe/other", Prompt: "p"})
		e.st.SetRunNotify(ctx, store.RunNotify{RunID: id, Email: "v@example.com", Link: "t", AddressHash: notify.AddressHash("v@example.com")})
	}
	a := e.visitor(1, "192.0.2.1")
	slug := a.create(t, "a calm page")
	e.script()
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- a.chatRaw(slug, "p", "") }()
	<-m.started
	if rec := a.post("/build/api/fe/"+slug+"/notify", map[string]string{"email": "V@example.com"}); rec.Code != http.StatusTooManyRequests {
		t.Errorf("over the cap: %d %s", rec.Code, rec.Body)
	}
	if rec := a.post("/build/api/fe/"+slug+"/notify", map[string]string{"email": "w@example.com"}); rec.Code != 200 {
		t.Errorf("another address: %d", rec.Code)
	}
	close(m.release)
	<-done
	waitSent(t, out, 1)
}

func TestBuildNotifyOff(t *testing.T) {
	e := newBuilderServer(t, true) // no mailer
	a := e.visitor(1, "192.0.2.1")
	if a.state(t).Notify {
		t.Error("offer shown without a mailer")
	}
	slug := a.create(t, "a calm page")
	if rec := a.post("/build/api/fe/"+slug+"/notify", map[string]string{"email": "v@example.com"}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("notify without a mailer: %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(a.do("GET", "/dev/outbox", nil).Body.Bytes(), &body)
	if body != nil {
		t.Error("/dev/outbox served without an outbox")
	}
}

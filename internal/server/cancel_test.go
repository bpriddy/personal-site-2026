package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// blockingModel never answers: a turn waits until its context ends.
type blockingModel struct{ started chan struct{} }

func (m blockingModel) Turn(ctx context.Context, _ anthropic.BetaMessageNewParams, _ func(builder.Event)) (*anthropic.BetaMessage, error) {
	m.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

// A visitor's build in progress shows its prompt in the list (from the
// store, so a reload on another instance sees it) and can be canceled: it
// ends with a "canceled" event, no new version, and the run is closed.
func TestBuildCancel(t *testing.T) {
	cancelPoll = 20 * time.Millisecond
	t.Setenv(frontend.RotationEnv, "")
	st := store.NewMemory()
	m := blockingModel{started: make(chan struct{}, 1)}
	cfg := config.Config{Env: "dev", AdminUser: "admin", AdminPassword: "pw", SigningKey: testKey,
		MainOrigin: "http://localhost:8080", UsercontentOrigin: "http://127.0.0.1:8081", FrontendsDir: t.TempDir()}
	s, err := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithBuilder(builder.New(m, builder.Config{Model: "claude-opus-5-5"}), revfiles.NewDir(cfg.FrontendsDir)))
	if err != nil {
		t.Fatal(err)
	}
	e := &builderEnv{s: s, st: st}
	a := e.visitor(1, "192.0.2.1")
	slug := a.create(t, "a calm page")

	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- a.chatRaw(slug, "make it calmer", "") }()
	<-m.started

	var list buildState
	json.Unmarshal(a.do("GET", "/build/api/frontends", nil).Body.Bytes(), &list)
	if len(list.Frontends) != 1 || !list.Frontends[0].Running || list.Frontends[0].Run == nil || list.Frontends[0].Run.Prompt != "make it calmer" {
		t.Fatalf("list while running = %+v", list.Frontends)
	}

	// another visitor can't cancel it
	if rec := e.visitor(2, "192.0.2.2").post("/build/api/fe/"+slug+"/cancel", nil); rec.Code != 404 {
		t.Errorf("stranger cancel: %d", rec.Code)
	}
	if rec := a.post("/build/api/fe/"+slug+"/cancel", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"canceled":1`) {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the run didn't stop")
	}
	evs := sseEvents(t, rec.Body.String())
	if eventOf(evs, "canceled") == nil || eventOf(evs, "revision") != nil || eventOf(evs, "done") == nil {
		t.Fatalf("events = %+v", evs)
	}
	runs, _ := st.Runs(context.Background(), "fe/"+slug, 1)
	if len(runs) != 1 || runs[0].Status != store.RunFailed || runs[0].Error != store.RunCanceled {
		t.Fatalf("run = %+v", runs)
	}
	list = buildState{}
	json.Unmarshal(a.do("GET", "/build/api/frontends", nil).Body.Bytes(), &list)
	if list.Frontends[0].Running || list.Frontends[0].Run != nil {
		t.Errorf("still running after cancel: %+v", list.Frontends[0])
	}

	// the admin can cancel a run too (here: nothing is running)
	if rec := e.form("/admin/builder/fe/"+slug+"/cancel", url.Values{}); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"canceled":0`) {
		t.Errorf("admin cancel: %d %s", rec.Code, rec.Body)
	}
}

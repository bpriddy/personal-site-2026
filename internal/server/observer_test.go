package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/observer"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

type stubModel struct {
	mu sync.Mutex
	n  int
}

func (m *stubModel) Generate(_ context.Context, req observer.GenerateRequest) (observer.Generation, error) {
	m.mu.Lock()
	m.n++
	m.mu.Unlock()
	return observer.Generation{Value: "A generated " + req.Field, Confidence: 0.9, EnoughContent: true, Model: "stub"}, nil
}

type obsFixture struct {
	s       *Server
	o       *observer.Observer
	obs     *store.ObserverMemory
	content *store.Memory
	model   *stubModel
}

const origin = "http://localhost:8080"

func newObserverServer(t *testing.T, withModel bool, limits observer.Limits) *obsFixture {
	t.Helper()
	t.Setenv(frontend.RotationEnv, "")
	f := &obsFixture{obs: store.NewObserverMemory(), content: store.NewMemory(), model: &stubModel{}}
	f.content.SavePage(context.Background(), content.Page{Slug: "about", Title: "About",
		Body: "Ben builds interactive WebGPU experiments and writes about creative coding.", Published: true})
	oc := observer.Config{Content: f.content, Obs: f.obs, Limits: limits, CheckEvery: time.Hour, XFFHops: 1}
	if withModel {
		oc.Model = f.model
	}
	f.o = observer.New(oc)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f.o.Start(ctx)
	cfg := config.Config{Env: "dev", AdminUser: "admin", AdminPassword: "pw", SigningKey: testKey,
		MainOrigin: origin, UsercontentOrigin: "http://127.0.0.1:8081"}
	s, err := New(cfg, f.content, slog.New(slog.NewTextHandler(io.Discard, nil)), WithObserver(f.o))
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	return f
}

const gapBody = `{"kind":"content-gap","frontend":"builtin/site","serve":"builtin/site","route":"about",
	"collection":"pages","item":"about","field":"subtitle","expect":"text","got":"missing","message":"","stack":""}`

func observe(s *Server, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/observe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.RemoteAddr = "192.0.2.1:5555"
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestObserveIngestion(t *testing.T) {
	f := newObserverServer(t, true, observer.Limits{})
	for _, hdr := range []map[string]string{
		nil,
		{"Content-Type": "text/plain;charset=UTF-8"}, // sendBeacon with a string body
		{"Origin": "", "Sec-Fetch-Site": ""},         // non-browser client
	} {
		if rec := observe(f.s, gapBody, hdr); rec.Code != http.StatusNoContent {
			t.Fatalf("%v: status %d: %s", hdr, rec.Code, rec.Body)
		}
	}
	// deduplicated: one detection, count 3, one generation
	waitFor(t, func() bool {
		all, _ := f.obs.Detections(context.Background(), 0)
		return len(all) == 1 && all[0].Count == 3 && all[0].Status == store.StatusFixed
	})
	g, err := f.s.contentGenerated().GeneratedFields(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 1 || f.model.n != 1 {
		t.Fatalf("generated = %v, model calls %d", g, f.model.n)
	}
	// and the contract serves it
	rec := get(f.s, "/api/site.json")
	if !strings.Contains(rec.Body.String(), `"subtitle":"A generated subtitle"`) {
		t.Fatalf("site.json lacks the generated subtitle: %s", rec.Body)
	}
}

func TestObserveRejects(t *testing.T) {
	f := newObserverServer(t, true, observer.Limits{})
	cases := []struct {
		name string
		body string
		hdr  map[string]string
		want int
	}{
		{"cross-origin", gapBody, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": ""}, http.StatusForbidden},
		{"cross-site fetch", gapBody, map[string]string{"Origin": "", "Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"null origin (sandboxed iframe)", gapBody, map[string]string{"Origin": "null", "Sec-Fetch-Site": ""}, http.StatusForbidden},
		{"too large", `{"kind":"frontend-error","frontend":"builtin/site","stack":"` + strings.Repeat("x", 9000) + `"}`, nil, http.StatusRequestEntityTooLarge},
		{"content type", gapBody, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
		{"bad json", `{`, nil, http.StatusBadRequest},
		{"bad field", strings.Replace(gapBody, `"subtitle"`, `"<script>"`, 1), nil, http.StatusBadRequest},
		{"bad kind", strings.Replace(gapBody, `"content-gap"`, `"content-invalid"`, 1), nil, http.StatusBadRequest},
	}
	for _, c := range cases {
		if rec := observe(f.s, c.body, c.hdr); rec.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, rec.Code, c.want)
		}
	}
	if rec := get(f.s, "/api/observe"); rec.Code == http.StatusNoContent {
		t.Error("GET accepted")
	}
	time.Sleep(50 * time.Millisecond)
	if all, _ := f.obs.Detections(context.Background(), 0); len(all) != 0 {
		t.Fatalf("rejected reports recorded: %+v", all)
	}
}

func TestObserveRateLimit(t *testing.T) {
	f := newObserverServer(t, false, observer.Limits{PerIPRate: 0.001, PerIPBurst: 3, GlobalRate: 0.001, GlobalBurst: 5, MaxClients: 100})
	for i := range 6 {
		// every well-formed report gets 204, limited or not
		if rec := observe(f.s, gapBody, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("report %d: %d", i, rec.Code)
		}
	}
	for i := range 4 {
		if rec := observe(f.s, gapBody, map[string]string{"X-Forwarded-For": "198.51.100." + string(rune('1'+i))}); rec.Code != http.StatusNoContent {
			t.Fatalf("status %d", rec.Code)
		}
	}
	// 3 from the first client, then 2 more until the global cap of 5
	waitFor(t, func() bool {
		d, _ := f.obs.Detections(context.Background(), 0)
		return len(d) == 1 && d[0].Count == 5
	})
	time.Sleep(50 * time.Millisecond)
	if d, _ := f.obs.Detections(context.Background(), 0); d[0].Count != 5 {
		t.Fatalf("count = %d, want 5", d[0].Count)
	}
}

func adminReq(s *Server, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.SetBasicAuth("admin", "pw")
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func unseen(t *testing.T, s *Server) int {
	t.Helper()
	rec := adminReq(s, "GET", "/admin/observer/unseen.json", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unseen.json: %d %s", rec.Code, rec.Body)
	}
	var v struct{ Unseen int }
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v.Unseen
}

func TestAdminObserver(t *testing.T) {
	f := newObserverServer(t, true, observer.Limits{})
	ctx := context.Background()
	if rec := get(f.s, "/admin/observer/unseen.json"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unseen.json without auth: %d", rec.Code)
	}
	if n := unseen(t, f.s); n != 0 {
		t.Fatalf("unseen = %d", n)
	}
	observe(f.s, gapBody, nil)
	observe(f.s, strings.Replace(gapBody, "subtitle", "tagline", 1), nil)
	waitFor(t, func() bool {
		d, _ := f.obs.Detections(ctx, 0, store.StatusFixed)
		return len(d) == 2
	})
	if n := unseen(t, f.s); n != 2 {
		t.Fatalf("unseen = %d, want 2", n)
	}
	// the review tab is empty; viewing it marks nothing
	rec := adminReq(f.s, "GET", "/admin/observer/", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Nothing here.") {
		t.Fatalf("review tab: %d %s", rec.Code, rec.Body)
	}
	if n := unseen(t, f.s); n != 2 {
		t.Fatalf("unseen after viewing another tab = %d", n)
	}
	rec = adminReq(f.s, "GET", "/admin/observer/?tab=fixed", "")
	body := rec.Body.String()
	for _, want := range []string{`class="dot dot-new"`, `generated subtitle for &#34;about&#34;: &#34;A generated subtitle&#34;`, "/accept", "/revert", `id="observer-dot"`} {
		if !strings.Contains(body, want) {
			t.Errorf("fixed tab lacks %q", want)
		}
	}
	if n := unseen(t, f.s); n != 0 {
		t.Fatalf("unseen after viewing = %d", n)
	}
	if strings.Contains(adminReq(f.s, "GET", "/admin/observer/?tab=fixed", "").Body.String(), "dot-new") {
		t.Error("rows still marked new on the second view")
	}

	d, _ := f.obs.Detections(ctx, 0)
	id := func(field string) string {
		for _, x := range d {
			if x.Field == field {
				return strconv.FormatInt(x.ID, 10)
			}
		}
		t.Fatalf("no detection for %s", field)
		return ""
	}
	rec = adminReq(f.s, "POST", "/admin/observer/"+id("subtitle")+"/accept", "tab=fixed")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/observer/?done=accept&tab=fixed" {
		t.Fatalf("accept: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if g, _ := f.obs.GeneratedField(ctx, "pages", "about", "subtitle"); g.Status != store.GenAccepted {
		t.Fatalf("status = %s", g.Status)
	}
	rec = adminReq(f.s, "POST", "/admin/observer/"+id("subtitle")+"/accept", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "no generated value to accept") {
		t.Fatalf("second accept: %d", rec.Code)
	}
	adminReq(f.s, "POST", "/admin/observer/"+id("tagline")+"/edit", "value=Mine")
	if g, _ := f.obs.GeneratedField(ctx, "pages", "about", "tagline"); g.Value != "Mine" || g.Model != "human" {
		t.Fatalf("edit = %+v", g)
	}
	adminReq(f.s, "POST", "/admin/observer/"+id("tagline")+"/revert", "")
	if g, _ := f.obs.GeneratedField(ctx, "pages", "about", "tagline"); g.Status != store.GenCleared {
		t.Fatalf("revert = %+v", g)
	}
	adminReq(f.s, "POST", "/admin/observer/"+id("tagline")+"/dismiss", "")
	tid, _ := strconv.ParseInt(id("tagline"), 10, 64)
	if x, _ := f.obs.Detection(ctx, tid); x.Status != store.StatusDismissed {
		t.Fatalf("dismiss = %+v", x)
	}
	if rec := adminReq(f.s, "POST", "/admin/observer/999/dismiss", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing id: %d", rec.Code)
	}
	if rec := adminReq(f.s, "POST", "/admin/observer/1/explode", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown action: %d", rec.Code)
	}
	// cross-site form posts are refused (CrossOriginProtection)
	req := httptest.NewRequest("POST", "/admin/observer/1/dismiss", nil)
	req.SetBasicAuth("admin", "pw")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	f.s.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site admin post: %d", rec.Code)
	}
}

func TestAdminObserverHealingOff(t *testing.T) {
	f := newObserverServer(t, false, observer.Limits{})
	observe(f.s, gapBody, nil)
	waitFor(t, func() bool { n, _ := f.obs.UnseenDetections(context.Background()); return n == 1 })
	body := adminReq(f.s, "GET", "/admin/observer/", "").Body.String()
	if !strings.Contains(body, "Healing is off") || !strings.Contains(body, "dot-new") || strings.Contains(body, "/regenerate") {
		t.Fatalf("page: %s", body)
	}
}

func TestObserverNotConfigured(t *testing.T) {
	s := newTestServer(t)
	if rec := observe(s, gapBody, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("observe: %d", rec.Code)
	}
	if n := unseen(t, s); n != 0 {
		t.Fatalf("unseen = %d", n)
	}
	body := adminReq(s, "GET", "/admin/observer/", "").Body.String()
	if !strings.Contains(body, "not configured") {
		t.Fatalf("page: %s", body)
	}
	if s.contentGenerated() != nil {
		t.Fatal("contentGenerated not nil without an observer")
	}
}

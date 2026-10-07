package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

func TestEstimateRunCost(t *testing.T) {
	for _, c := range []struct {
		costs []float64
		want  float64
		runs  int
	}{
		{nil, 4, 0},
		{[]float64{9, 9}, 4, 0}, // too few runs: the default
		{[]float64{1, 2, 3}, 3, 3},
		// p90 by nearest rank of 10: the 9th smallest
		{[]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 9, 10},
		{[]float64{0.01, 0.02, 0.01}, estimateFloorUSD, 3}, // never below the floor
	} {
		got, n := estimateRunCost(c.costs, 4)
		if math.Abs(got-c.want) > 1e-9 || n != c.runs {
			t.Errorf("estimate(%v) = %v from %d, want %v from %d", c.costs, got, n, c.want, c.runs)
		}
	}
	// only the newest estimateRuns count (costs come newest first)
	costs := make([]float64, 0, 30)
	for range 20 {
		costs = append(costs, 1)
	}
	for range 10 {
		costs = append(costs, 100) // older, expensive runs
	}
	if got, n := estimateRunCost(costs, 4); got != 1 || n != estimateRuns {
		t.Fatalf("estimate = %v from %d, want 1 from the newest %d", got, n, estimateRuns)
	}
}

func TestBudgetDecide(t *testing.T) {
	at := time.Date(2026, 10, 31, 23, 59, 30, 0, time.UTC) // the last minute of the month
	nextDay := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		st      budgetStatus
		limit   string
		reopens time.Time
	}{
		{"room for exactly one more", budgetStatus{DailyUSD: 10, Today: 6, Estimate: 4}, "", time.Time{}},
		{"one more would pass the day", budgetStatus{DailyUSD: 10, Today: 6.01, Estimate: 4}, pauseDay, nextDay},
		{"under the limit, but not with one more", budgetStatus{DailyUSD: 10, Today: 7, Estimate: 4}, pauseDay, nextDay},
		{"runs going count", budgetStatus{DailyUSD: 10, Today: 2, Reserved: 4.5, Estimate: 4}, pauseDay, nextDay},
		{"no daily limit", budgetStatus{Today: 1000, Estimate: 4}, "", time.Time{}},
		{"month", budgetStatus{MonthlyUSD: 100, Month: 97, Estimate: 4}, pauseMonth, nextDay},
		{"month wins over day", budgetStatus{DailyUSD: 10, MonthlyUSD: 100, Today: 9, Month: 99, Estimate: 4}, pauseMonth, nextDay},
		{"the API's own limit", budgetStatus{DailyUSD: 10, APIPausedUntil: at.Add(time.Minute)}, pauseLater, time.Time{}},
		{"the API's limit with a known reset", budgetStatus{APIPausedUntil: nextDay, APIReopens: nextDay}, pauseLater, nextDay},
		{"an API pause that's over", budgetStatus{APIPausedUntil: at.Add(-time.Second)}, "", time.Time{}},
		{"API over day", budgetStatus{DailyUSD: 1, Today: 5, Estimate: 4, APIPausedUntil: at.Add(time.Hour)}, pauseLater, time.Time{}},
	} {
		st := c.st
		st.At = at
		st.decide()
		if st.Closed != (c.limit != "") || st.Limit != c.limit || !st.Reopens.Equal(c.reopens) {
			t.Errorf("%s: closed=%v limit=%q reopens=%v, want %q %v", c.name, st.Closed, st.Limit, st.Reopens, c.limit, c.reopens)
		}
	}
	// periods are UTC, whatever the server's zone; months roll over years
	la := time.FixedZone("PDT", -7*3600)
	local := time.Date(2026, 12, 31, 20, 0, 0, 0, la) // 2027-01-01 03:00 UTC
	if d := dayStart(local); !d.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("dayStart = %v", d)
	}
	st := budgetStatus{At: time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC), MonthlyUSD: 1, Month: 1, Estimate: 1}
	st.decide()
	if !st.Reopens.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("December reopens %v", st.Reopens)
	}
}

func TestBudgetFromEnv(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	b, err := BudgetFromEnv(get)
	if err != nil || b != DefaultBudget {
		t.Fatalf("defaults = %+v, %v", b, err)
	}
	env = map[string]string{"BUILD_BUDGET_DAILY_USD": "12.5", "BUILD_BUDGET_MONTHLY_USD": "0", "BUILD_COST_ESTIMATE_USD": "3", "BUILD_BUDGET_DEV_COOKIE": "1"}
	if b, err = BudgetFromEnv(get); err != nil || b.DailyUSD != 12.5 || b.MonthlyUSD != 0 || b.EstimateUSD != 3 || !b.DevCookie {
		t.Fatalf("set = %+v, %v", b, err)
	}
	for _, bad := range []string{"-1", "ten", "NaN", "Inf"} {
		env = map[string]string{"BUILD_BUDGET_DAILY_USD": bad}
		if _, err := BudgetFromEnv(get); err == nil {
			t.Errorf("BUILD_BUDGET_DAILY_USD=%q accepted", bad)
		}
	}
}

// costly makes a scripted response report usage on Opus 5.5.
func costly(raw string, input, output int64) string {
	var m map[string]any
	json.Unmarshal([]byte(raw), &m)
	m["model"] = "claude-opus-5-5"
	m["usage"] = map[string]any{"input_tokens": input, "output_tokens": output}
	b, _ := json.Marshal(m)
	return string(b)
}

// scriptCostly scripts a valid revision costing $4 (1M input) + $2 (100k output).
func (e *builderEnv) scriptCostly() {
	e.model.Responses = []string{
		costly(builder.ToolUse("1", "write_file", map[string]any{"path": "index.html", "content": fixtureIndex}), 1_000_000, 0),
		costly(builder.ToolUse("2", "finish", map[string]any{"summary": "A page."}), 0, 100_000),
		costly(builder.ToolUse("3", "finish", map[string]any{"summary": "A page."}), 0, 0),
	}
}

func (e *builderEnv) setLimits(t *testing.T, daily, monthly string) {
	t.Helper()
	rec := e.form("/admin/builder/budget", url.Values{"daily": {daily}, "monthly": {monthly}})
	if rec.Code != 303 || strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Fatalf("budget: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func (e *builderEnv) adminChat(slug, prompt string, over bool) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"prompt": prompt, "overBudget": over})
	return e.admin("POST", "/admin/builder/fe/"+slug+"/chat", strings.NewReader(string(body)),
		"Content-Type", "application/json", "Sec-Fetch-Site", "same-origin")
}

func TestBudgetGate(t *testing.T) {
	e := newBuilderServer(t, true)
	e.s.budget.cfg = Budget{DailyUSD: 10, MonthlyUSD: 100, EstimateUSD: 2}
	ctx := context.Background()
	a := e.visitor(1, "198.51.100.1")

	// a run's real cost is recorded from the API's usage
	slug := a.create(t, "A dark page")
	e.scriptCostly()
	rec := a.chatRaw(slug, "A dark page", "")
	if rec.Code != 200 || eventOf(sseEvents(t, rec.Body.String()), "revision") == nil {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body)
	}
	runs, _ := e.st.Runs(ctx, "fe/"+slug, 1)
	if len(runs) != 1 || math.Abs(runs[0].Cost.USD-6) > 1e-9 || runs[0].Cost.InputTokens != 1_000_000 || runs[0].Cost.OutputTokens != 100_000 {
		t.Fatalf("run cost = %+v", runs)
	}
	st, err := e.s.budgetStatus(ctx, true)
	if err != nil || math.Abs(st.Today-6) > 1e-9 || math.Abs(st.Month-6) > 1e-9 || st.Estimate != 2 || st.Closed {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if s := a.state(t); !s.Enabled || s.Paused != nil {
		t.Fatalf("open state = %+v", s)
	}

	// $6 + $2 for one more passes a $7.99 day: paused until the next UTC midnight
	e.setLimits(t, "7.99", "")
	s := a.state(t)
	tomorrow := dayStart(time.Now()).AddDate(0, 0, 1)
	if s.Enabled || s.Paused == nil || s.Paused.Limit != pauseDay || s.Paused.Reopens == nil || !s.Paused.Reopens.Equal(tomorrow) || s.Notice != "" {
		t.Fatalf("paused state = %+v (%+v)", s, s.Paused)
	}
	// visitors never see amounts
	raw := a.do("GET", "/build/api/frontends", nil).Body.String()
	fe := a.do("GET", "/api/frontend", nil).Body.String()
	for _, body := range []string{raw, fe} {
		if strings.Contains(body, "$") || strings.Contains(strings.ToLower(body), "usd") || strings.Contains(body, "7.99") {
			t.Fatalf("a visitor sees amounts: %s", body)
		}
	}
	if !strings.Contains(fe, `"paused":{"limit":"day"`) {
		t.Fatalf("/api/frontend doesn't say building is paused: %s", fe)
	}

	// the server refuses builds while paused: a new front end, and a chat
	b := e.visitor(2, "198.51.100.2")
	rec = b.post("/build/api/new", map[string]string{"prompt": "Water"})
	var refusal struct {
		Error, Key string
		Paused     *buildPause
	}
	json.Unmarshal(rec.Body.Bytes(), &refusal)
	if rec.Code != 503 || refusal.Key != msgPaused || refusal.Paused == nil || refusal.Paused.Limit != pauseDay || !strings.Contains(refusal.Error, "closed for today") {
		t.Fatalf("new while paused: %d %s", rec.Code, rec.Body)
	}
	e.scriptCostly()
	rec = a.chatRaw(slug, "Darker", runs[0].RevisionID)
	if rec.Code != 503 || rec.Header().Get(pausedHeader) != pauseDay || !strings.Contains(rec.Body.String(), "studio is closed") {
		t.Fatalf("chat while paused: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if n := len(e.model.Requests); n != 3 {
		t.Fatalf("the model was called while paused (%d requests)", n)
	}
	// browsing still works: showing a version on the site
	if rec := a.live(slug, ""); rec.Code != 200 {
		t.Fatalf("live while paused: %d %s", rec.Code, rec.Body)
	}

	// Ben: refused with the numbers, unless he overrides on purpose
	rec = e.adminChat(slug, "Admin change", false)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "Budget gate closed") || !strings.Contains(rec.Body.String(), "$6.00") {
		t.Fatalf("admin chat while closed: %d %s", rec.Code, rec.Body)
	}
	e.script()
	rec = e.adminChat(slug, "Admin change", true)
	if rec.Code != 200 || eventOf(sseEvents(t, rec.Body.String()), "revision") == nil {
		t.Fatalf("admin override: %d %s", rec.Code, rec.Body)
	}
	page := adminDo(e, "GET", "/admin/builder/").Body.String()
	for _, want := range []string{`id="spend"`, "gate closed", "$6.00", "$7.99", "One front end ≈"} {
		if !strings.Contains(page, want) {
			t.Errorf("admin builder page lacks %q", want)
		}
	}

	// the month: $6 + $2 passes $7; it reopens on the 1st
	e.setLimits(t, "", "7")
	s = a.state(t)
	if s.Paused == nil || s.Paused.Limit != pauseMonth || !s.Paused.Reopens.Equal(monthStart(time.Now()).AddDate(0, 1, 0)) {
		t.Fatalf("month: %+v", s.Paused)
	}
	// 0 = no limit
	e.setLimits(t, "0", "0")
	if s = a.state(t); !s.Enabled || s.Paused != nil {
		t.Fatalf("no limits: %+v", s)
	}
	if rec := e.form("/admin/builder/budget", url.Values{"daily": {"lots"}}); !strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Fatal("a bad limit was saved")
	}

	// runs going count against the room: one running run holds an estimate
	e.setLimits(t, "10", "")
	if _, err := e.st.StartRun(ctx, store.Run{FrontendID: "fe/" + slug, Prompt: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	// $6 spent + $2 held for it + $2 for one more = $10: just room
	if st, _ = e.s.budgetStatus(ctx, true); st.Running != 1 || st.Reserved != 2 || st.Closed {
		t.Fatalf("running: %+v", st)
	}
	e.setLimits(t, "9.99", "")
	if st, _ = e.s.budgetStatus(ctx, true); !st.Closed || st.Limit != pauseDay {
		t.Fatalf("$6 spent + $2 held + $2 more passes $9.99: %+v", st)
	}
}

// The Claude API's own spend refusal looks exactly like the gate to a
// visitor: a paused state, never an error or "unavailable".
func TestBudgetAPISpendLimitLooksPaused(t *testing.T) {
	e := newBuilderServer(t, true)
	a := e.visitor(1, "198.51.100.1")
	slug := a.create(t, "A dark page")
	e.model.Responses = []string{`error:400 Bad Request {"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits."}}`}
	rec := a.chatRaw(slug, "A dark page", "")
	evs := sseEvents(t, rec.Body.String())
	if p := eventOf(evs, "paused"); rec.Code != 200 || p == nil || eventOf(evs, "error") != nil || !strings.Contains(p.Text, "studio is closed for now") {
		t.Fatalf("API spend limit: %d %+v", rec.Code, evs)
	}
	s := a.state(t)
	if s.Enabled || s.Paused == nil || s.Paused.Limit != pauseLater || s.Paused.Reopens != nil {
		t.Fatalf("state = %+v (%+v)", s, s.Paused)
	}
	// and the next try is refused up front, without calling the model
	rec = a.chatRaw(slug, "Again", "")
	if rec.Code != 503 || rec.Header().Get(pausedHeader) != pauseLater {
		t.Fatalf("after: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(adminDo(e, "GET", "/admin/builder/").Body.String(), "Clear the pause") {
		t.Fatal("the admin doesn't offer to clear the API pause")
	}
	if rec := e.form("/admin/builder/budget/clear-api", url.Values{}); rec.Code != 303 {
		t.Fatalf("clear: %d", rec.Code)
	}
	if s := a.state(t); !s.Enabled || s.Paused != nil {
		t.Fatalf("after clearing: %+v", s)
	}

	// a limit that says when it resets: that's when it reopens
	e.model.Responses = []string{`error:400 {"type":"error","error":{"type":"invalid_request_error","message":"You have reached your specified workspace API usage limits. You will regain access on 2031-11-01 at 00:00 UTC."}}`}
	a.chatRaw(slug, "Once more", "")
	s = a.state(t)
	if s.Paused == nil || s.Paused.Limit != pauseLater || s.Paused.Reopens == nil || !s.Paused.Reopens.Equal(time.Date(2031, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("with a reset: %+v", s.Paused)
	}
	// a run that gets through ends the pause (Ben's override here)
	e.script()
	if rec := e.adminChat(slug, "Admin", true); rec.Code != 200 {
		t.Fatalf("override: %d %s", rec.Code, rec.Body)
	}
	if s := a.state(t); s.Paused != nil {
		t.Fatalf("still paused after a run got through: %+v", s.Paused)
	}
}

func TestBudgetDevCookie(t *testing.T) {
	e := newBuilderServer(t, true)
	a := e.visitor(1, "198.51.100.1")
	a.cookies[devBudgetCookie] = pauseMonth
	if s := a.state(t); s.Paused != nil {
		t.Fatal("the dev cookie works without BUILD_BUDGET_DEV_COOKIE")
	}
	e.s.budget.cfg.DevCookie = true
	if s := a.state(t); s.Paused == nil || s.Paused.Limit != pauseMonth {
		t.Fatalf("dev cookie: %+v", s)
	}
	if rec := a.post("/build/api/new", map[string]string{"prompt": "x"}); rec.Code != 503 {
		t.Fatalf("new with the dev cookie: %d", rec.Code)
	}
	// only this browser
	if s := e.visitor(2, "198.51.100.2").state(t); s.Paused != nil {
		t.Fatal("the dev cookie paused everyone")
	}
	e.s.cfg.Env = "prod"
	if s := a.state(t); s.Paused != nil {
		t.Fatal("the dev cookie works in prod")
	}
}

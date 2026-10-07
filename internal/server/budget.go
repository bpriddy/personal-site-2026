package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/llm"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// The builder's budget gate (docs/frontend-protocol.md, v1.12). Every run's
// real cost is recorded from the API's usage (internal/llm prices it). New
// builds are refused when today's or this month's spend, plus what the runs
// still going are expected to spend, plus the expected cost of one more front
// end, would pass Ben's daily or monthly limit. The Claude API's own spend
// refusal ("usage limits", "credit balance is too low") closes the same gate.
// Visitors see "building is paused", never dollars.

// Budget is the spend limits' defaults (env); Ben can change the limits in
// the admin (stored in builder_budget, which wins).
type Budget struct {
	DailyUSD    float64 // BUILD_BUDGET_DAILY_USD; 0 = no daily limit
	MonthlyUSD  float64 // BUILD_BUDGET_MONTHLY_USD; 0 = no monthly limit
	EstimateUSD float64 // BUILD_COST_ESTIMATE_USD: one front end, until there's history
	// DevCookie (BUILD_BUDGET_DEV_COOKIE=1, dev only): a dev_budget cookie
	// (day, month or later) forces the gate closed for that browser, so tests
	// can see the paused state without closing it for everyone.
	DevCookie bool
}

// DefaultBudget: about ten front ends a day, a hundred a month, at the
// default estimate.
var DefaultBudget = Budget{DailyUSD: 40, MonthlyUSD: 400, EstimateUSD: 4}

const (
	estimateRuns       = 20   // the estimate looks at this many recent completed runs
	estimatePercentile = 0.9  // and takes this percentile of their cost
	estimateMinRuns    = 3    // below this many, it uses Budget.EstimateUSD
	estimateFloorUSD   = 0.25 // never estimate a front end cheaper than this
	apiPauseRetry      = 30 * time.Minute
	budgetCacheFor     = 15 * time.Second // the visitor-facing state; run starts check fresh
	devBudgetCookie    = "dev_budget"
)

// BudgetFromEnv reads BUILD_BUDGET_DAILY_USD, BUILD_BUDGET_MONTHLY_USD,
// BUILD_COST_ESTIMATE_USD and BUILD_BUDGET_DEV_COOKIE over DefaultBudget. A
// malformed or negative amount is an error.
func BudgetFromEnv(getenv func(string) string) (Budget, error) {
	b := DefaultBudget
	for _, v := range []struct {
		name string
		dst  *float64
	}{
		{"BUILD_BUDGET_DAILY_USD", &b.DailyUSD},
		{"BUILD_BUDGET_MONTHLY_USD", &b.MonthlyUSD},
		{"BUILD_COST_ESTIMATE_USD", &b.EstimateUSD},
	} {
		s := getenv(v.name)
		if s == "" {
			continue
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f < 0 || math.IsInf(f, 0) || math.IsNaN(f) {
			return b, fmt.Errorf("%s=%q: want a non-negative amount in USD", v.name, s)
		}
		*v.dst = f
	}
	if b.EstimateUSD <= 0 {
		b.EstimateUSD = DefaultBudget.EstimateUSD
	}
	b.DevCookie = getenv("BUILD_BUDGET_DEV_COOKIE") == "1"
	return b, nil
}

// WithBudget sets the budget's defaults (default DefaultBudget).
func WithBudget(b Budget) Option {
	return func(s *Server) error {
		if b.EstimateUSD <= 0 {
			b.EstimateUSD = DefaultBudget.EstimateUSD
		}
		s.budget.cfg = b
		return nil
	}
}

// budgetState is the server's budget side.
type budgetState struct {
	cfg Budget

	mu       sync.Mutex
	cached   budgetStatus
	cachedAt time.Time
}

// Pause limits, as the visitor-facing state names them.
const (
	pauseDay   = "day"   // today's limit: reopens at the next UTC midnight
	pauseMonth = "month" // this month's: reopens on the 1st (UTC)
	pauseLater = "later" // the Claude API's own limit: reopens when it says, or later
)

// budgetStatus is the gate's state at one moment.
type budgetStatus struct {
	At                   time.Time
	DailyUSD, MonthlyUSD float64 // the limits in force (0 = none)
	DailySet, MonthlySet bool    // set by Ben in the admin (else the env default)
	Today, Month         float64 // spent: runs started today / this month (UTC)
	Running              int     // runs going now
	Reserved             float64 // what those are still expected to spend
	Estimate             float64 // one more front end
	EstimateRuns         int     // how many runs the estimate came from (0 = the default)
	APIPausedUntil       time.Time
	APIReopens           time.Time

	Closed  bool
	Limit   string    // pauseDay, pauseMonth or pauseLater
	Reopens time.Time // zero: unknown ("later")
}

// estimateRunCost is the expected cost of one more front end: a high
// percentile of recent completed runs (newest first), or def without enough
// of them. It returns the estimate and how many runs it used.
func estimateRunCost(costs []float64, def float64) (float64, int) {
	if len(costs) > estimateRuns {
		costs = costs[:estimateRuns]
	}
	if len(costs) < estimateMinRuns {
		return def, 0
	}
	sorted := slices.Clone(costs)
	slices.Sort(sorted)
	// nearest rank
	i := int(math.Ceil(estimatePercentile*float64(len(sorted)))) - 1
	return max(sorted[max(i, 0)], estimateFloorUSD), len(sorted)
}

// dayStart and monthStart are the UTC periods spend is counted in.
func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// decide closes the gate if one more front end (st.Estimate), on top of the
// spend and what running runs are expected to spend, would pass a limit, or
// while the API's own limit holds. The month wins over the API, the API over
// the day: the reason that lasts longest is the one to tell.
func (st *budgetStatus) decide() {
	st.Closed, st.Limit, st.Reopens = false, "", time.Time{}
	const eps = 1e-9
	next := st.Reserved + st.Estimate
	switch {
	case st.MonthlyUSD > 0 && st.Month+next > st.MonthlyUSD+eps:
		st.Closed, st.Limit, st.Reopens = true, pauseMonth, monthStart(st.At).AddDate(0, 1, 0)
	case st.At.Before(st.APIPausedUntil):
		st.Closed, st.Limit, st.Reopens = true, pauseLater, st.APIReopens
	case st.DailyUSD > 0 && st.Today+next > st.DailyUSD+eps:
		st.Closed, st.Limit, st.Reopens = true, pauseDay, dayStart(st.At).AddDate(0, 0, 1)
	}
}

// spendStore is the store's spend side, or nil.
func (s *Server) spendStore() store.Spend {
	sp, _ := s.store.(store.Spend)
	return sp
}

// budgetStatus works out the gate now. fresh skips the short cache (run
// starts); the visitor-facing state may be a few seconds old. Without a
// spend store the gate is always open.
func (s *Server) budgetStatus(ctx context.Context, fresh bool) (budgetStatus, error) {
	now := s.now().UTC()
	if !fresh {
		s.budget.mu.Lock()
		c, at := s.budget.cached, s.budget.cachedAt
		s.budget.mu.Unlock()
		if !at.IsZero() && now.Sub(at) >= 0 && now.Sub(at) < budgetCacheFor {
			return c, nil
		}
	}
	cfg := s.budget.cfg
	if cfg.EstimateUSD <= 0 {
		cfg = DefaultBudget
	}
	st := budgetStatus{At: now, DailyUSD: cfg.DailyUSD, MonthlyUSD: cfg.MonthlyUSD, Estimate: cfg.EstimateUSD}
	sp := s.spendStore()
	if sp == nil {
		return st, nil
	}
	set, err := sp.BudgetSettings(ctx)
	if err != nil {
		return st, err
	}
	if set.DailyUSD != nil {
		st.DailyUSD, st.DailySet = *set.DailyUSD, true
	}
	if set.MonthlyUSD != nil {
		st.MonthlyUSD, st.MonthlySet = *set.MonthlyUSD, true
	}
	st.APIPausedUntil, st.APIReopens = set.APIPausedUntil, set.APIReopens
	alive := now.Add(-RunTimeout - time.Minute)
	month, err := sp.SpendSince(ctx, monthStart(now), alive)
	if err != nil {
		return st, err
	}
	today, err := sp.SpendSince(ctx, dayStart(now), alive)
	if err != nil {
		return st, err
	}
	costs, err := sp.RecentRunCosts(ctx, estimateRuns)
	if err != nil {
		return st, err
	}
	st.Estimate, st.EstimateRuns = estimateRunCost(costs, cfg.EstimateUSD)
	st.Today, st.Month, st.Running = today.USD, month.USD, month.Running
	// runs going now will spend about an estimate each; they've spent some already
	st.Reserved = max(0, float64(month.Running)*st.Estimate-month.RunningUSD)
	st.decide()
	s.budget.mu.Lock()
	s.budget.cached, s.budget.cachedAt = st, now
	s.budget.mu.Unlock()
	return st, nil
}

// forgetBudget drops the cached state (a limit or the API pause changed).
func (s *Server) forgetBudget() {
	s.budget.mu.Lock()
	s.budget.cachedAt = time.Time{}
	s.budget.mu.Unlock()
}

// buildPause is the visitor-facing paused state (no amounts).
type buildPause struct {
	Limit   string     `json:"limit"`             // "day", "month" or "later"
	Reopens *time.Time `json:"reopens,omitempty"` // when building opens again, if known (UTC)
}

// visitorPause says whether visitors can't build right now because of the
// budget, and until when; nil when building is open (or the builder is off
// for another reason). fresh: see budgetStatus. A store error keeps building
// open here: the run start checks again.
func (s *Server) visitorPause(r *http.Request, fresh bool) *buildPause {
	if p := s.devPause(r); p != nil {
		return p
	}
	st, err := s.budgetStatus(r.Context(), fresh)
	if err != nil {
		s.log.Error("budget: status", "err", err)
		return nil
	}
	return st.pause()
}

// devPause is the pause a dev_budget cookie forces (dev with
// BUILD_BUDGET_DEV_COOKIE=1 only), or nil.
func (s *Server) devPause(r *http.Request) *buildPause {
	if !s.budget.cfg.DevCookie || !s.cfg.Dev() {
		return nil
	}
	c, err := r.Cookie(devBudgetCookie)
	if err != nil {
		return nil
	}
	now := s.now().UTC()
	switch c.Value {
	case pauseDay:
		t := dayStart(now).AddDate(0, 0, 1)
		return &buildPause{Limit: pauseDay, Reopens: &t}
	case pauseMonth:
		t := monthStart(now).AddDate(0, 1, 0)
		return &buildPause{Limit: pauseMonth, Reopens: &t}
	case pauseLater:
		return &buildPause{Limit: pauseLater}
	}
	return nil
}

func (st budgetStatus) pause() *buildPause {
	if !st.Closed {
		return nil
	}
	p := &buildPause{Limit: st.Limit}
	if !st.Reopens.IsZero() {
		t := st.Reopens.UTC()
		p.Reopens = &t
	}
	return p
}

// pausedHeader marks a refused build as "paused for budget" (its value is
// the limit), so the builder's stream client can tell it from other errors.
const pausedHeader = "X-Build-Paused"

// gate checks the budget for a run starting now (fresh): the visitor-facing
// pause (nil = open; the dev cookie applies) and the full status.
func (s *Server) gate(r *http.Request) (*buildPause, budgetStatus, error) {
	st, err := s.budgetStatus(r.Context(), true)
	if err != nil {
		return nil, st, err
	}
	if p := s.devPause(r); p != nil {
		return p, st, nil
	}
	return st.pause(), st, nil
}

// pausedMessage is the plain text a visitor gets when a build is refused
// because building is paused (the modal draws its own, richer state).
func pausedMessage(p *buildPause) string {
	switch p.Limit {
	case pauseDay:
		return "The studio is closed for today, so nothing new can be built until tomorrow. Everything you've made is still here."
	case pauseMonth:
		return "The studio is closed for the rest of the month, so nothing new can be built until then. Everything you've made is still here."
	}
	return "The studio is closed for now, so nothing new can be built. Check back a little later. Everything you've made is still here."
}

// adminBudgetRefusal is Ben's explanation when the gate refuses his run.
func adminBudgetRefusal(st budgetStatus) string {
	switch st.Limit {
	case pauseMonth:
		return fmt.Sprintf("Budget gate closed: this month's spend $%.2f + ~$%.2f for one more front end (+ $%.2f for runs going) would pass the monthly limit $%.2f. Tick “Build anyway” to override.",
			st.Month, st.Estimate, st.Reserved, st.MonthlyUSD)
	case pauseDay:
		return fmt.Sprintf("Budget gate closed: today's spend $%.2f + ~$%.2f for one more front end (+ $%.2f for runs going) would pass the daily limit $%.2f. Tick “Build anyway” to override.",
			st.Today, st.Estimate, st.Reserved, st.DailyUSD)
	}
	return "Budget gate closed: the Claude API refused a recent run for lack of credit or a spend limit. Tick “Build anyway” to try anyway (it clears the pause if it works), or clear it on the Builder page."
}

// noteSpendLimit records the Claude API's spend refusal: building pauses
// until the API said access returns, else for apiPauseRetry (a refused
// request costs nothing, so trying again then is harmless).
func (s *Server) noteSpendLimit(ctx context.Context, se *llm.SpendLimitError) {
	sp := s.spendStore()
	if sp == nil {
		return
	}
	now := s.now().UTC()
	until := now.Add(apiPauseRetry)
	if se.Reset.After(until) {
		until = se.Reset
	}
	if err := sp.SetAPIPause(ctx, until, se.Reset); err != nil {
		s.log.Error("budget: api pause", "err", err)
	}
	s.log.Warn("builder: the Claude API refused for lack of money; building paused", "until", until, "err", se.Err)
	s.forgetBudget()
}

// clearAPIPause ends an API pause after a run got through.
func (s *Server) clearAPIPause(ctx context.Context) {
	sp := s.spendStore()
	if sp == nil {
		return
	}
	set, err := sp.BudgetSettings(ctx)
	if err != nil || set.APIPausedUntil.IsZero() {
		return
	}
	if err := sp.SetAPIPause(ctx, time.Time{}, time.Time{}); err != nil {
		s.log.Error("budget: clear api pause", "err", err)
	}
	s.forgetBudget()
}

// runCostRecorder accumulates a run's cost and writes the total to the store
// after every report (so a run that fails or never ends still counts).
type runCostRecorder struct {
	s     *Server
	ctx   context.Context
	runID int64
	mu    sync.Mutex
	total llm.Cost
}

func (s *Server) newRunCost(ctx context.Context, runID int64) *runCostRecorder {
	return &runCostRecorder{s: s, ctx: ctx, runID: runID}
}

func (rc *runCostRecorder) add(c llm.Cost) {
	rc.mu.Lock()
	rc.total = rc.total.Add(c)
	t := rc.total
	rc.mu.Unlock()
	sp := rc.s.spendStore()
	if sp == nil {
		return
	}
	if err := sp.SetRunCost(rc.ctx, rc.runID, store.RunCost{InputTokens: t.InputTokens, OutputTokens: t.OutputTokens,
		CacheReadTokens: t.CacheReadTokens, CacheWriteTokens: t.CacheWriteTokens, USD: t.USD}); err != nil {
		rc.s.log.Error("budget: run cost", "run", rc.runID, "err", err)
	}
}

func (rc *runCostRecorder) usd() float64 {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.total.USD
}

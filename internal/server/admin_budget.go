package server

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The builder's spend in the admin (v1.12): today's and this month's spend
// against the limits, the estimate of one front end, the gate's state, and a
// form for the limits (POST /admin/builder/budget). Shown on the Builder page.

// budgetView is the Spend section's data, formatted.
type budgetView struct {
	Closed         bool
	Reason         string // why it's closed, for Ben
	Reopens        string // when it reopens ("" = unknown)
	Today, Month   string // "$12.34"
	DailyLimit     string // "$40.00" or "no limit"
	MonthlyLimit   string
	DailyInput     string // the form's values: "" = the env default
	MonthlyInput   string
	DailyDefault   string // the env defaults, for the form's hints
	MonthlyDefault string
	DayRoom        string // "about 3 more front ends"
	MonthRoom      string
	DayPct         int // spent / limit, for the bars (0-100)
	MonthPct       int
	Estimate       string
	EstimateFrom   string // where the estimate comes from
	Running        int
	Reserved       string
	APIPaused      bool
	APIUntil       string
}

func usd(v float64) string { return fmt.Sprintf("$%.2f", v) }

func limitText(v float64) string {
	if v <= 0 {
		return "no limit"
	}
	return usd(v)
}

func roomText(limit, spent, reserved, estimate float64) string {
	if limit <= 0 {
		return "no limit"
	}
	n := int(math.Floor((limit - spent - reserved) / estimate))
	switch {
	case n <= 0:
		return "no room for another front end"
	case n == 1:
		return "room for about 1 more front end"
	}
	return fmt.Sprintf("room for about %d more front ends", n)
}

func pct(spent, limit float64) int {
	if limit <= 0 {
		return 0
	}
	return int(math.Min(100, math.Round(100*spent/limit)))
}

func (s *Server) budgetView(st budgetStatus) budgetView {
	cfg := s.budget.cfg
	v := budgetView{
		Closed: st.Closed, Today: usd(st.Today), Month: usd(st.Month),
		DailyLimit: limitText(st.DailyUSD), MonthlyLimit: limitText(st.MonthlyUSD),
		DailyDefault: limitText(cfg.DailyUSD), MonthlyDefault: limitText(cfg.MonthlyUSD),
		DayRoom:   roomText(st.DailyUSD, st.Today, st.Reserved, st.Estimate),
		MonthRoom: roomText(st.MonthlyUSD, st.Month, st.Reserved, st.Estimate),
		DayPct:    pct(st.Today, st.DailyUSD), MonthPct: pct(st.Month, st.MonthlyUSD),
		Estimate: usd(st.Estimate), Running: st.Running, Reserved: usd(st.Reserved),
		APIPaused: st.At.Before(st.APIPausedUntil),
	}
	if st.DailySet {
		v.DailyInput = strconv.FormatFloat(st.DailyUSD, 'f', 2, 64)
	}
	if st.MonthlySet {
		v.MonthlyInput = strconv.FormatFloat(st.MonthlyUSD, 'f', 2, 64)
	}
	if st.EstimateRuns > 0 {
		v.EstimateFrom = fmt.Sprintf("the 90th percentile of the last %d completed runs", st.EstimateRuns)
	} else {
		v.EstimateFrom = fmt.Sprintf("the default (BUILD_COST_ESTIMATE_USD), until %d runs have finished with a cost", estimateMinRuns)
	}
	if v.APIPaused {
		v.APIUntil = st.APIPausedUntil.UTC().Format("Jan 2 15:04 UTC")
	}
	switch st.Limit {
	case pauseDay:
		v.Reason = "one more front end would pass today's limit"
	case pauseMonth:
		v.Reason = "one more front end would pass this month's limit"
	case pauseLater:
		v.Reason = "the Claude API refused a run for lack of credit or a spend limit"
	}
	if !st.Reopens.IsZero() {
		v.Reopens = st.Reopens.UTC().Format("Mon Jan 2 15:04 UTC")
	}
	return v
}

// adminBudget is the Spend section's data, or nil without a spend store.
func (s *Server) adminBudget(r *http.Request) (*budgetView, error) {
	if s.spendStore() == nil {
		return nil, nil
	}
	st, err := s.budgetStatus(r.Context(), true)
	if err != nil {
		return nil, err
	}
	v := s.budgetView(st)
	return &v, nil
}

// parseLimit reads a limit field: "" = the env default (nil), else a
// non-negative amount ("$" and "," allowed; 0 = no limit).
func parseLimit(s string) (*float64, error) {
	s = strings.TrimSpace(strings.NewReplacer("$", "", ",", "").Replace(s))
	if s == "" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || math.IsInf(f, 0) || math.IsNaN(f) || f > 1e6 {
		return nil, fmt.Errorf("%q isn't an amount in USD", s)
	}
	f = math.Round(f*100) / 100
	return &f, nil
}

// builderBudget saves Ben's limits (form: daily, monthly).
func (s *Server) builderBudget(w http.ResponseWriter, r *http.Request) {
	sp := s.spendStore()
	if sp == nil {
		http.Error(w, "the store doesn't track spend", http.StatusServiceUnavailable)
		return
	}
	daily, err := parseLimit(r.FormValue("daily"))
	if err == nil {
		var monthly *float64
		if monthly, err = parseLimit(r.FormValue("monthly")); err == nil {
			err = sp.SetBudgetLimits(r.Context(), daily, monthly)
			if err != nil {
				s.fail(w, "builder: budget", err)
				return
			}
		}
	}
	s.forgetBudget()
	to := "/admin/builder/#spend"
	if err != nil {
		to = "/admin/builder/?error=" + url.QueryEscape("Limits not saved: "+err.Error()) + "#spend"
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// builderBudgetClearAPI ends a pause the Claude API's spend refusal started
// (e.g. after adding credit).
func (s *Server) builderBudgetClearAPI(w http.ResponseWriter, r *http.Request) {
	if sp := s.spendStore(); sp != nil {
		if err := sp.SetAPIPause(r.Context(), time.Time{}, time.Time{}); err != nil {
			s.fail(w, "builder: budget", err)
			return
		}
	}
	s.forgetBudget()
	http.Redirect(w, r, "/admin/builder/#spend", http.StatusSeeOther)
}

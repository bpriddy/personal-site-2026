package store

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

type spendStore interface {
	Builder
	Spend
}

// spendConformance runs the behavior every Spend must share.
func spendConformance(t *testing.T, newStore func(t *testing.T) spendStore) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

	t.Run("run costs and spend", func(t *testing.T) {
		st := newStore(t)
		must(t, st.CreatePromptedFrontend(ctx, "fe/s", "S"))
		start := func() int64 {
			id, err := st.StartRun(ctx, Run{FrontendID: "fe/s", Prompt: "p"})
			must(t, err)
			return id
		}
		done, failed, running := start(), start(), start()
		must(t, st.SetRunCost(ctx, done, RunCost{InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40, USD: 1.5}))
		must(t, st.SetRunCost(ctx, done, RunCost{InputTokens: 11, OutputTokens: 21, CacheReadTokens: 31, CacheWriteTokens: 41, USD: 1.75})) // totals, not increments
		must(t, st.FinishRun(ctx, done, "", ""))
		must(t, st.SetRunCost(ctx, failed, RunCost{USD: 0.5}))
		must(t, st.FinishRun(ctx, failed, "", "model: boom"))
		must(t, st.SetRunCost(ctx, running, RunCost{USD: 0.25}))
		if err := st.SetRunCost(ctx, 9999, RunCost{USD: 1}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown run: %v", err)
		}

		past := time.Now().Add(-time.Hour)
		got, err := st.SpendSince(ctx, past, past)
		must(t, err)
		if !near(got.USD, 2.5) || got.Running != 1 || !near(got.RunningUSD, 0.25) {
			t.Fatalf("spend = %+v", got)
		}
		// runs older than aliveSince are dead, not running (but still spent)
		got, _ = st.SpendSince(ctx, past, time.Now().Add(time.Hour))
		if !near(got.USD, 2.5) || got.Running != 0 || got.RunningUSD != 0 {
			t.Fatalf("dead runs: %+v", got)
		}
		// nothing started in the future
		got, _ = st.SpendSince(ctx, time.Now().Add(time.Hour), past)
		if got.USD != 0 || got.Running != 0 {
			t.Fatalf("future: %+v", got)
		}

		costs, err := st.RecentRunCosts(ctx, 10)
		must(t, err)
		if len(costs) != 1 || !near(costs[0], 1.75) {
			t.Fatalf("recent costs = %v (only done runs with a cost)", costs)
		}
		runs, _ := st.Runs(ctx, "fe/s", 3)
		if len(runs) != 3 || runs[2].ID != done || !near(runs[2].Cost.USD, 1.75) || runs[2].Cost.CacheWriteTokens != 41 || runs[2].Cost.InputTokens != 11 {
			t.Fatalf("runs = %+v", runs)
		}
	})

	t.Run("settings", func(t *testing.T) {
		st := newStore(t)
		b, err := st.BudgetSettings(ctx)
		must(t, err)
		if b.DailyUSD != nil || b.MonthlyUSD != nil || !b.APIPausedUntil.IsZero() {
			t.Fatalf("empty = %+v", b)
		}
		ten := 10.0
		must(t, st.SetBudgetLimits(ctx, &ten, nil))
		until := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		reopens := until
		must(t, st.SetAPIPause(ctx, until.Add(-time.Hour), reopens))
		b, _ = st.BudgetSettings(ctx)
		if b.DailyUSD == nil || *b.DailyUSD != 10 || b.MonthlyUSD != nil || !b.APIPausedUntil.Equal(until.Add(-time.Hour)) || !b.APIReopens.Equal(reopens) {
			t.Fatalf("set = %+v", b)
		}
		// the limits and the API pause are independent
		zero := 0.0
		must(t, st.SetBudgetLimits(ctx, nil, &zero))
		b, _ = st.BudgetSettings(ctx)
		if b.DailyUSD != nil || b.MonthlyUSD == nil || *b.MonthlyUSD != 0 || b.APIPausedUntil.IsZero() {
			t.Fatalf("limits again = %+v", b)
		}
		must(t, st.SetAPIPause(ctx, time.Time{}, reopens))
		b, _ = st.BudgetSettings(ctx)
		if !b.APIPausedUntil.IsZero() || !b.APIReopens.IsZero() || b.MonthlyUSD == nil {
			t.Fatalf("cleared = %+v", b)
		}
	})
}

func TestMemorySpendConformance(t *testing.T) {
	spendConformance(t, func(*testing.T) spendStore { return NewMemory() })
}

func TestPostgresSpendConformance(t *testing.T) {
	spendConformance(t, func(t *testing.T) spendStore { return openPG(t, freshDB(t)) })
}

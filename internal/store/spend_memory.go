package store

import (
	"context"
	"time"
)

var _ Spend = (*Memory)(nil)

func (m *Memory) SetRunCost(_ context.Context, runID int64, c RunCost) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if runID < 1 || int(runID) > len(m.builder.runs) {
		return ErrNotFound
	}
	m.builder.runs[runID-1].Cost = c
	return nil
}

func (m *Memory) SpendSince(_ context.Context, since, aliveSince time.Time) (SpendTotal, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var t SpendTotal
	for _, r := range m.builder.runs {
		if r.StartedAt.Before(since) {
			continue
		}
		t.USD += r.Cost.USD
		if r.Status == RunRunning && !r.StartedAt.Before(aliveSince) {
			t.Running++
			t.RunningUSD += r.Cost.USD
		}
	}
	return t, nil
}

func (m *Memory) RecentRunCosts(_ context.Context, n int) ([]float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []float64{}
	for i := len(m.builder.runs) - 1; i >= 0 && len(out) < n; i-- {
		if r := m.builder.runs[i]; r.Status == RunDone && r.Cost.USD > 0 {
			out = append(out, r.Cost.USD)
		}
	}
	return out, nil
}

func (m *Memory) BudgetSettings(context.Context) (BudgetSettings, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b := m.budget
	if b.DailyUSD != nil {
		v := *b.DailyUSD
		b.DailyUSD = &v
	}
	if b.MonthlyUSD != nil {
		v := *b.MonthlyUSD
		b.MonthlyUSD = &v
	}
	return b, nil
}

func (m *Memory) SetBudgetLimits(_ context.Context, dailyUSD, monthlyUSD *float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.budget.DailyUSD, m.budget.MonthlyUSD = copyFloat(dailyUSD), copyFloat(monthlyUSD)
	m.budget.UpdatedAt = time.Now()
	return nil
}

func (m *Memory) SetAPIPause(_ context.Context, until, reopens time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if until.IsZero() {
		reopens = time.Time{}
	}
	m.budget.APIPausedUntil, m.budget.APIReopens = until, reopens
	m.budget.UpdatedAt = time.Now()
	return nil
}

func copyFloat(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

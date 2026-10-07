package store

import (
	"context"
	"time"
)

// Spend persists what builder runs cost and the budget gate's settings
// (migration 0012; protocol v1.12). Memory and Postgres implement it
// alongside Builder; the server finds it with a type assertion.
type Spend interface {
	// SetRunCost records a run's cost so far (totals, not an increment);
	// ErrNotFound for an unknown run.
	SetRunCost(ctx context.Context, runID int64, c RunCost) error
	// SpendSince sums the cost of runs started at or after since. Runs still
	// running count as Running if they started at or after aliveSince (older
	// ones are dead: their instance went away).
	SpendSince(ctx context.Context, since, aliveSince time.Time) (SpendTotal, error)
	// RecentRunCosts returns the cost (USD) of the newest n runs that
	// finished done with a cost recorded, newest first.
	RecentRunCosts(ctx context.Context, n int) ([]float64, error)

	// BudgetSettings returns the stored settings (zero value if none).
	BudgetSettings(ctx context.Context) (BudgetSettings, error)
	// SetBudgetLimits stores Ben's limits; nil means "the env default".
	SetBudgetLimits(ctx context.Context, dailyUSD, monthlyUSD *float64) error
	// SetAPIPause records that the Claude API refused for lack of money:
	// building pauses until until. reopens is when the API said access
	// returns (zero if it didn't say). A zero until clears the pause.
	SetAPIPause(ctx context.Context, until, reopens time.Time) error
}

// RunCost is what one run cost.
type RunCost struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	USD              float64
}

// SpendTotal is the spend of a period.
type SpendTotal struct {
	USD        float64 // every run started in the period
	Running    int     // runs still going (alive)
	RunningUSD float64 // what those have spent so far (included in USD)
}

// BudgetSettings is the stored budget configuration.
type BudgetSettings struct {
	DailyUSD, MonthlyUSD *float64 // nil = env default
	APIPausedUntil       time.Time
	APIReopens           time.Time
	UpdatedAt            time.Time
}

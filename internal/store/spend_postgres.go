package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var _ Spend = (*Postgres)(nil)

func (p *Postgres) SetRunCost(ctx context.Context, runID int64, c RunCost) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE builder_runs SET input_tokens = $2, output_tokens = $3, cache_read_tokens = $4,
		       cache_write_tokens = $5, cost_usd = $6
		WHERE id = $1`, runID, c.InputTokens, c.OutputTokens, c.CacheReadTokens, c.CacheWriteTokens, c.USD)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) SpendSince(ctx context.Context, since, aliveSince time.Time) (SpendTotal, error) {
	var t SpendTotal
	err := p.pool.QueryRow(ctx, `
		SELECT coalesce(sum(cost_usd), 0)::float8,
		       count(*) FILTER (WHERE status = 'running' AND started_at >= $2),
		       coalesce(sum(cost_usd) FILTER (WHERE status = 'running' AND started_at >= $2), 0)::float8
		FROM builder_runs WHERE started_at >= $1`, since, aliveSince).Scan(&t.USD, &t.Running, &t.RunningUSD)
	return t, err
}

func (p *Postgres) RecentRunCosts(ctx context.Context, n int) ([]float64, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT cost_usd::float8 FROM builder_runs
		WHERE status = 'done' AND cost_usd > 0
		ORDER BY started_at DESC, id DESC LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[float64])
	if out == nil {
		out = []float64{}
	}
	return out, err
}

func (p *Postgres) BudgetSettings(ctx context.Context) (BudgetSettings, error) {
	var b BudgetSettings
	var until, reopens *time.Time
	err := p.pool.QueryRow(ctx, `
		SELECT daily_usd::float8, monthly_usd::float8, api_paused_until, api_reopens, updated_at
		FROM builder_budget WHERE id`).Scan(&b.DailyUSD, &b.MonthlyUSD, &until, &reopens, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return BudgetSettings{}, nil
	}
	if until != nil {
		b.APIPausedUntil = *until
	}
	if reopens != nil {
		b.APIReopens = *reopens
	}
	return b, err
}

func (p *Postgres) SetBudgetLimits(ctx context.Context, dailyUSD, monthlyUSD *float64) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO builder_budget (id, daily_usd, monthly_usd) VALUES (true, $1, $2)
		ON CONFLICT (id) DO UPDATE SET daily_usd = $1, monthly_usd = $2, updated_at = now()`, dailyUSD, monthlyUSD)
	return err
}

func (p *Postgres) SetAPIPause(ctx context.Context, until, reopens time.Time) error {
	var u, r *time.Time
	if !until.IsZero() {
		u = &until
		if !reopens.IsZero() {
			r = &reopens
		}
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO builder_budget (id, api_paused_until, api_reopens) VALUES (true, $1, $2)
		ON CONFLICT (id) DO UPDATE SET api_paused_until = $1, api_reopens = $2, updated_at = now()`, u, r)
	return err
}

-- Builder spend (protocol v1.12): what each run cost, from the API's usage
-- (internal/llm prices it), and the budget gate's settings. A run's cost is
-- written as it goes (after every model turn), so failed and running runs
-- count too. Spend is summed by the UTC day / month a run started in.

ALTER TABLE builder_runs
    ADD COLUMN input_tokens       bigint NOT NULL DEFAULT 0,  -- uncached input
    ADD COLUMN output_tokens      bigint NOT NULL DEFAULT 0,
    ADD COLUMN cache_read_tokens  bigint NOT NULL DEFAULT 0,
    ADD COLUMN cache_write_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN cost_usd           numeric(12, 6) NOT NULL DEFAULT 0;

CREATE INDEX builder_runs_started ON builder_runs (started_at);

-- One row: Ben's limits (NULL = the BUILD_BUDGET_* env default) and the
-- Claude API's own spend refusal, if one was seen (paused until api_paused_until;
-- api_reopens is when the API said access returns, if it said).
CREATE TABLE builder_budget (
    id                boolean PRIMARY KEY DEFAULT true CHECK (id),
    daily_usd         numeric(12, 2) CHECK (daily_usd >= 0),
    monthly_usd       numeric(12, 2) CHECK (monthly_usd >= 0),
    api_paused_until  timestamptz,
    api_reopens       timestamptz,
    updated_at        timestamptz NOT NULL DEFAULT now()
);

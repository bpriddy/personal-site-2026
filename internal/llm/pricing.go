package llm

import (
	"github.com/anthropics/anthropic-sdk-go"
)

// Prices: what Claude costs, in one place (the builder's spend accounting and
// budget gate, docs/frontend-protocol.md v1.12, price every model turn here).
//
// USD per million tokens, Anthropic first-party API standard rates, from
// https://docs.claude.com/en/docs/about-claude/pricing (checked 2026-10-07).
// Update these when Anthropic's prices change.
const (
	// Claude Opus 5.5 (Model), the builder's model.
	Opus55Input     = 4.00 // input tokens (uncached)
	Opus55Output    = 20.00
	Opus55CacheRead = 0.20 // cache hits
	// Cache writes cost a multiple of the input price: 1.25x for the
	// 5-minute TTL (the builder's), 2x for the 1-hour TTL.
	CacheWrite5mFactor = 1.25
	CacheWrite1hFactor = 2.0
	// Fast mode (speed "fast") costs twice the standard rates.
	FastFactor = 2.0
)

// Price is one model's per-million-token rates.
type Price struct {
	Input, Output, CacheRead float64
}

// prices are the models a builder run may be billed for: the builder's own,
// and those the server-side refusal fallback ("default") may re-serve a turn
// on (the response names the model that answered).
var prices = map[string]Price{
	Model:               {Opus55Input, Opus55Output, Opus55CacheRead},
	"claude-opus-5":     {5.00, 25.00, 0.50},
	"claude-opus-4-8":   {5.00, 25.00, 0.50},
	"claude-opus-4-7":   {5.00, 25.00, 0.50},
	"claude-fable-5-1":  {10.00, 50.00, 0.25},
	"claude-fable-5":    {10.00, 50.00, 1.00},
	"claude-sonnet-5-5": {2.00, 10.00, 0.20},
	"claude-sonnet-5":   {2.00, 10.00, 0.20},
	"claude-haiku-4-5":  {1.00, 5.00, 0.10},
}

// unknownPrice prices a model missing from the table: the dearest one, so an
// unknown model never makes the budget look roomier than it is.
var unknownPrice = Price{10.00, 50.00, 1.00}

// PriceOf returns model's rates, and whether the table knows it.
func PriceOf(model string) (Price, bool) {
	p, ok := prices[model]
	if !ok {
		return unknownPrice, false
	}
	return p, true
}

// Cost is what some work cost: tokens by kind, and the price in USD (which
// may include things that aren't tokens, e.g. generated media).
type Cost struct {
	InputTokens      int64 // uncached input
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	USD              float64
}

// Add returns c + d.
func (c Cost) Add(d Cost) Cost {
	return Cost{
		InputTokens:      c.InputTokens + d.InputTokens,
		OutputTokens:     c.OutputTokens + d.OutputTokens,
		CacheReadTokens:  c.CacheReadTokens + d.CacheReadTokens,
		CacheWriteTokens: c.CacheWriteTokens + d.CacheWriteTokens,
		USD:              c.USD + d.USD,
	}
}

// Usage is one response's token counts, as the API reports them.
type Usage struct {
	Model        string // the model that answered
	Input        int64  // uncached input tokens
	Output       int64
	CacheRead    int64
	CacheWrite5m int64
	CacheWrite1h int64
	Fast         bool // fast mode
}

// UsageOf reads a (beta) message's usage. Cache writes without a TTL
// breakdown count as 5-minute writes. model is used when the message doesn't
// name one.
func UsageOf(msg *anthropic.BetaMessage, model string) Usage {
	u := msg.Usage
	out := Usage{Model: string(msg.Model), Input: u.InputTokens, Output: u.OutputTokens,
		CacheRead: u.CacheReadInputTokens, Fast: u.Speed == "fast"}
	if out.Model == "" {
		out.Model = model
	}
	w5, w1 := u.CacheCreation.Ephemeral5mInputTokens, u.CacheCreation.Ephemeral1hInputTokens
	if w5+w1 == 0 {
		w5 = u.CacheCreationInputTokens
	}
	out.CacheWrite5m, out.CacheWrite1h = w5, w1
	return out
}

// Cost prices u.
func (u Usage) Cost() Cost {
	p, _ := PriceOf(u.Model)
	usd := (float64(u.Input)*p.Input +
		float64(u.Output)*p.Output +
		float64(u.CacheRead)*p.CacheRead +
		float64(u.CacheWrite5m)*p.Input*CacheWrite5mFactor +
		float64(u.CacheWrite1h)*p.Input*CacheWrite1hFactor) / 1e6
	if u.Fast {
		usd *= FastFactor
	}
	return Cost{InputTokens: u.Input, OutputTokens: u.Output, CacheReadTokens: u.CacheRead,
		CacheWriteTokens: u.CacheWrite5m + u.CacheWrite1h, USD: usd}
}

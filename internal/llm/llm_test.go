package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestUsageCostOpus55(t *testing.T) {
	// 1M of each kind on Opus 5.5: $4 in, $20 out, $0.20 cache reads,
	// $5 (1.25x) 5-minute writes, $8 (2x) 1-hour writes
	u := Usage{Model: "claude-opus-5-5", Input: 1e6, Output: 1e6, CacheRead: 1e6, CacheWrite5m: 1e6, CacheWrite1h: 1e6}
	c := u.Cost()
	if !near(c.USD, 4+20+0.20+5+8) {
		t.Fatalf("cost = %v", c.USD)
	}
	if c.InputTokens != 1e6 || c.OutputTokens != 1e6 || c.CacheReadTokens != 1e6 || c.CacheWriteTokens != 2e6 {
		t.Fatalf("tokens = %+v", c)
	}
	// a realistic turn: 3k fresh input, 40k cached, 2k cache write, 8k output
	u = Usage{Model: Model, Input: 3000, CacheRead: 40000, CacheWrite5m: 2000, Output: 8000}
	want := (3000*4.0 + 40000*0.20 + 2000*5.0 + 8000*20.0) / 1e6
	if got := u.Cost().USD; !near(got, want) {
		t.Fatalf("turn = %v, want %v", got, want)
	}
	// fast mode doubles it
	u.Fast = true
	if got := u.Cost().USD; !near(got, 2*want) {
		t.Fatalf("fast = %v", got)
	}
	// an unknown model is priced at the dearest rates, never as free
	if p, ok := PriceOf("claude-next"); ok || p != unknownPrice {
		t.Fatalf("unknown model price = %+v, %v", p, ok)
	}
	if got := (Usage{Model: "claude-next", Output: 1e6}).Cost().USD; got < 20 {
		t.Fatalf("unknown model cost = %v", got)
	}
	if got := (Usage{Model: "demo"}).Cost().USD; got != 0 {
		t.Fatalf("no tokens costs %v", got)
	}
	sum := Cost{InputTokens: 1, USD: 1}.Add(Cost{OutputTokens: 2, USD: 0.5})
	if sum.InputTokens != 1 || sum.OutputTokens != 2 || !near(sum.USD, 1.5) {
		t.Fatalf("add = %+v", sum)
	}
}

func TestUsageOf(t *testing.T) {
	var msg anthropic.BetaMessage
	raw := `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":40,
		"cache_creation":{"ephemeral_5m_input_tokens":15,"ephemeral_1h_input_tokens":25},"speed":"standard"}}`
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatal(err)
	}
	u := UsageOf(&msg, Model)
	if u.Model != "claude-opus-5" || u.Input != 10 || u.Output != 20 || u.CacheRead != 30 || u.CacheWrite5m != 15 || u.CacheWrite1h != 25 || u.Fast {
		t.Fatalf("usage = %+v (the answering model is priced, e.g. a fallback)", u)
	}
	// no TTL breakdown: all writes are 5-minute; no model: the configured one
	raw = `{"id":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":40,"speed":"fast"}}`
	msg = anthropic.BetaMessage{}
	json.Unmarshal([]byte(raw), &msg)
	u = UsageOf(&msg, Model)
	if u.Model != Model || u.CacheWrite5m != 40 || u.CacheWrite1h != 0 || !u.Fast {
		t.Fatalf("usage = %+v", u)
	}
}

func TestAsSpendLimit(t *testing.T) {
	for _, c := range []struct {
		msg   string
		spend bool
		reset time.Time
	}{
		{`400 Bad Request {"type":"error","error":{"type":"invalid_request_error","message":"You have reached your specified workspace API usage limits. You will regain access on 2026-11-01 at 00:00 UTC."}}`,
			true, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)},
		{`400 Bad Request {"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits."}}`,
			true, time.Time{}},
		{`You have reached your specified API usage limits. You will regain access on 2026-12-01.`, true, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
		{`429 Too Many Requests {"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed the rate limit for your organization of 4,000,000 input tokens per minute."}}`, false, time.Time{}},
		{`529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, false, time.Time{}},
		{`scripted model: no more responses`, false, time.Time{}},
	} {
		err := fmt.Errorf("model: %w", errors.New(c.msg))
		se := AsSpendLimit(err)
		if (se != nil) != c.spend {
			t.Errorf("%q: spend limit = %v, want %v", c.msg, se != nil, c.spend)
			continue
		}
		if se != nil && !se.Reset.Equal(c.reset) {
			t.Errorf("%q: reset = %v, want %v", c.msg, se.Reset, c.reset)
		}
		if se != nil && !errors.Is(se, errors.Unwrap(err)) {
			t.Errorf("%q: doesn't wrap the API error", c.msg)
		}
	}
	if AsSpendLimit(nil) != nil {
		t.Fatal("nil is a spend limit")
	}
	// already classified (wrapped further up): found again
	se := &SpendLimitError{Err: errors.New("x")}
	if AsSpendLimit(fmt.Errorf("run: %w", se)) != se {
		t.Fatal("a wrapped SpendLimitError isn't found")
	}
}

// The SDK's own errors: a 402 billing_error, and a 400 whose message is the
// workspace limit, both through a real client; a 429 rate limit is not one.
func TestAsSpendLimitSDKErrors(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		spend  bool
	}{
		{402, `{"type":"error","error":{"type":"billing_error","message":"Payment required."}}`, true},
		{400, `{"type":"error","error":{"type":"invalid_request_error","message":"You have reached your specified workspace API usage limits. You will regain access on 2026-11-01 at 00:00 UTC."}}`, true},
		{429, `{"type":"error","error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit."}}`, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			io.WriteString(w, c.body)
		}))
		client := anthropic.NewClient(option.WithAPIKey("test"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
		_, err := client.Beta.Messages.New(context.Background(), anthropic.BetaMessageNewParams{
			Model: Model, MaxTokens: 10, Messages: []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("hi"))}})
		srv.Close()
		if err == nil {
			t.Fatalf("%d: no error", c.status)
		}
		if got := AsSpendLimit(fmt.Errorf("model: %w", err)) != nil; got != c.spend {
			t.Errorf("%d: spend limit = %v, want %v (%v)", c.status, got, c.spend, err)
		}
	}
}

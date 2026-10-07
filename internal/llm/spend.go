package llm

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// SpendLimitError means the Claude API refused a request because the
// account's money ran out: a workspace or organization spend limit, or too
// little credit. Retrying won't help until the limit resets or credit is
// added. Reset is when the API said access returns, if it said (else zero).
type SpendLimitError struct {
	Reset time.Time
	Err   error
}

func (e *SpendLimitError) Error() string { return "API spend limit: " + e.Err.Error() }
func (e *SpendLimitError) Unwrap() error { return e.Err }

// spendPhrases are the API's own words for running out of money (matched
// case-insensitively in the error body), e.g.
//
//	"You have reached your specified workspace API usage limits. You will regain access on 2026-11-01 at 00:00 UTC."
//	"Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits."
var spendPhrases = []string{
	"api usage limits",
	"usage limit",
	"spend limit",
	"spending limit",
	"credit balance is too low",
	"purchase credits",
}

// regainRE finds "regain access on 2026-11-01 at 00:00 UTC".
var regainRE = regexp.MustCompile(`(?i)regain access on (\d{4}-\d{2}-\d{2})(?: at (\d{2}:\d{2}))?`)

// AsSpendLimit returns err as a *SpendLimitError if it's the API's spend or
// credit refusal (a 402 billing_error, or a 400/403/429 whose message says
// so), else nil. Rate limits ("rate limit") and overloads are not spend
// limits: they pass in seconds.
func AsSpendLimit(err error) *SpendLimitError {
	if err == nil {
		return nil
	}
	var se *SpendLimitError
	if errors.As(err, &se) {
		return se
	}
	msg := strings.ToLower(err.Error())
	var apiErr *anthropic.Error
	isAPI := errors.As(err, &apiErr)
	billing := isAPI && (apiErr.StatusCode == 402 || apiErr.Type() == "billing_error")
	if !billing {
		if isAPI && apiErr.StatusCode >= 500 {
			return nil
		}
		hit := false
		for _, p := range spendPhrases {
			if strings.Contains(msg, p) {
				hit = true
				break
			}
		}
		if !hit {
			return nil
		}
	}
	out := &SpendLimitError{Err: err}
	if m := regainRE.FindStringSubmatch(err.Error()); m != nil {
		at := m[1] + "T00:00"
		if m[2] != "" {
			at = m[1] + "T" + m[2]
		}
		if t, perr := time.Parse("2006-01-02T15:04", at); perr == nil {
			out.Reset = t.UTC()
		}
	}
	return out
}

package server

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/observer"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// BuildLimits bound the public builder (docs/frontend-protocol.md, v1.2
// "Limits"). Ben's admin builder is exempt.
type BuildLimits struct {
	RunsPerSessionHour int // BUILD_RUNS_PER_SESSION_HOUR
	RunsPerIPDay       int // BUILD_RUNS_PER_IP_DAY (UTC day)
	RunsGlobalDay      int // BUILD_RUNS_GLOBAL_DAY (UTC day; counted in the database)
	MaxPrompt          int // BUILD_MAX_PROMPT, in characters
	ConcurrentRuns     int // per session; always 1 today

	// XFFHops: how many trailing X-Forwarded-For entries to count back for
	// the client IP, like the observer (OBSERVE_XFF_HOPS; see observer.ClientIP).
	XFFHops int

	// NewPerIPWindow front ends may be created per client IP per NewWindow
	// (in memory, per instance): creating is free, but shouldn't be spammable.
	NewPerIPWindow int
	NewWindow      time.Duration
}

// DefaultBuildLimits are the spec's defaults.
var DefaultBuildLimits = BuildLimits{
	RunsPerSessionHour: 6, RunsPerIPDay: 30, RunsGlobalDay: 25, MaxPrompt: 4000, ConcurrentRuns: 1,
	XFFHops: 1, NewPerIPWindow: 10, NewWindow: 10 * time.Minute,
}

// BuildLimitsFromEnv reads the BUILD_* variables (and OBSERVE_XFF_HOPS) over
// the defaults. A malformed or negative value is an error.
func BuildLimitsFromEnv(getenv func(string) string) (BuildLimits, error) {
	l := DefaultBuildLimits
	for _, v := range []struct {
		name string
		dst  *int
	}{
		{"BUILD_RUNS_PER_SESSION_HOUR", &l.RunsPerSessionHour},
		{"BUILD_RUNS_PER_IP_DAY", &l.RunsPerIPDay},
		{"BUILD_RUNS_GLOBAL_DAY", &l.RunsGlobalDay},
		{"BUILD_MAX_PROMPT", &l.MaxPrompt},
		{"OBSERVE_XFF_HOPS", &l.XFFHops},
	} {
		s := getenv(v.name)
		if s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return l, fmt.Errorf("%s=%q: want a non-negative integer", v.name, s)
		}
		*v.dst = n
	}
	if l.MaxPrompt == 0 {
		l.MaxPrompt = DefaultBuildLimits.MaxPrompt
	}
	return l, nil
}

// WithBuildLimits sets the public builder's limits (default DefaultBuildLimits).
func WithBuildLimits(l BuildLimits) Option {
	return func(s *Server) error {
		if l.MaxPrompt <= 0 {
			l.MaxPrompt = DefaultBuildLimits.MaxPrompt
		}
		s.limits = l
		return nil
	}
}

// runQuota is the limits for a visitor run starting now.
func (s *Server) runQuota() store.RunQuota {
	now := s.now().UTC()
	l := s.limits
	return store.RunQuota{
		SessionHour: l.RunsPerSessionHour, IPDay: l.RunsPerIPDay, GlobalDay: l.RunsGlobalDay, Concurrent: max(l.ConcurrentRuns, 1),
		HourStart:    now.Add(-time.Hour),
		DayStart:     time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC),
		RunningSince: now.Add(-RunTimeout - time.Minute),
	}
}

// clientIPHash is the hash the limits count a client IP by (the IP itself is
// never stored).
func (s *Server) clientIPHash(r *http.Request) []byte {
	h := sha256.Sum256([]byte("build-ip:" + observer.ClientIP(r, s.limits.XFFHops)))
	return h[:]
}

// Message keys for the public builder (beside the store.Limit* keys).
const (
	msgEmpty       = "empty"
	msgTooLong     = "too-long"
	msgUnavailable = "unavailable"
	msgRefused     = "refused"
	msgDisabled    = "disabled"
	msgSlowNew     = "slow-new"
	msgNoRevision  = "no-revision"
	msgSubmitted   = "submitted"
)

// buildMessage is the friendly text for a message key; "" for unknown keys,
// so a ?msg= in the URL can only ever pick one of these.
func buildMessage(key string, l BuildLimits) string {
	switch key {
	case store.LimitConcurrent:
		return "One thing at a time: your last change is still being made. Let it finish, then try again."
	case store.LimitSessionHour:
		return "Slow down a little: you've made a lot of changes in the last hour. Take a short break and try again soon."
	case store.LimitIPDay:
		return "That's plenty of building for one day. Come back tomorrow to make more."
	case store.LimitGlobalDay:
		return "The builder is resting for today; try again tomorrow."
	case msgEmpty:
		return "Describe the front end you'd like first."
	case msgTooLong:
		return fmt.Sprintf("That's a long description. Please keep it under %d characters.", l.MaxPrompt)
	case msgUnavailable:
		return "The builder is unavailable right now. Please try again in a little while."
	case msgRefused:
		return "Claude couldn't make that one. Try describing it a different way."
	case msgDisabled:
		return "The builder is taking a break right now, so new front ends can't be made at the moment. Please check back soon."
	case msgSlowNew:
		return "Slow down a little: you're starting new front ends very quickly. Try again in a few minutes."
	case msgNoRevision:
		return "There's nothing to show yet: wait for the first version to finish."
	case msgSubmitted:
		return "Thanks! It's on its way to Ben. You'll see his answer here."
	}
	return ""
}

// windowLimiter allows n events per key per window (a sliding log, in memory).
type windowLimiter struct {
	mu     sync.Mutex
	events map[string][]time.Time
}

func (l *windowLimiter) allow(key string, n int, window time.Duration, now time.Time) bool {
	if n <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.events == nil {
		l.events = map[string][]time.Time{}
	}
	if len(l.events) > 10_000 { // forget idle clients
		for k, ts := range l.events {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > window {
				delete(l.events, k)
			}
		}
	}
	ts := l.events[key]
	keep := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= n {
		l.events[key] = keep
		return false
	}
	l.events[key] = append(keep, now)
	return true
}

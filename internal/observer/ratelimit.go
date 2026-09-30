package observer

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rate limits for POST /api/observe. They are per process: on Cloud Run each
// instance keeps its own buckets, so the effective limits scale with the
// instance count (max 4 today). That is acceptable for abuse control here;
// deduplication and the generation budget bound the real cost.
type Limits struct {
	PerIPRate   float64 // tokens per second per client IP
	PerIPBurst  float64
	GlobalRate  float64 // tokens per second across all clients
	GlobalBurst float64
	MaxClients  int // tracked IPs; beyond this, new IPs are refused until idle ones expire
}

// DefaultLimits: a visitor may send a burst of 20 reports (a page with several
// gaps reports each once) and then one every 6 s; the whole site accepts 200
// at once and 5/s sustained.
var DefaultLimits = Limits{PerIPRate: 1.0 / 6, PerIPBurst: 20, GlobalRate: 5, GlobalBurst: 200, MaxClients: 10_000}

type bucket struct {
	tokens float64
	last   time.Time
}

// take refills b at rate up to burst, then spends one token if it can.
func (b *bucket) take(now time.Time, rate, burst float64) bool {
	b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type limiter struct {
	mu      sync.Mutex
	lim     Limits
	global  bucket
	clients map[string]*bucket
	now     func() time.Time
}

func newLimiter(lim Limits, now func() time.Time) *limiter {
	return &limiter{lim: lim, global: bucket{tokens: lim.GlobalBurst, last: now()},
		clients: map[string]*bucket{}, now: now}
}

// allow spends a token from ip's bucket and the global one.
func (l *limiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.clients[ip]
	if !ok {
		if len(l.clients) >= l.lim.MaxClients {
			l.evict(now)
			if len(l.clients) >= l.lim.MaxClients {
				return false
			}
		}
		b = &bucket{tokens: l.lim.PerIPBurst, last: now}
		l.clients[ip] = b
	}
	// check the client's own bucket first, so one noisy client can't drain
	// the global bucket with requests that would be refused anyway
	if !b.take(now, l.lim.PerIPRate, l.lim.PerIPBurst) {
		return false
	}
	return l.global.take(now, l.lim.GlobalRate, l.lim.GlobalBurst)
}

// evict drops buckets that have refilled completely: forgetting them changes
// nothing, since a new client starts with a full bucket.
func (l *limiter) evict(now time.Time) {
	for ip, b := range l.clients {
		if b.tokens+now.Sub(b.last).Seconds()*l.lim.PerIPRate >= l.lim.PerIPBurst {
			delete(l.clients, ip)
		}
	}
}

// ClientIP returns the client address for rate limiting. Behind Google's
// front ends the peer address is a proxy, and X-Forwarded-For ends with the
// entries they appended: hops is how many trailing entries to count back
// (1 for Cloud Run alone, 2 behind the external load balancer, which appends
// "<client>, <lb-ip>"). Anything earlier in the header is client-supplied
// and ignored. hops <= 0 uses the peer address.
func ClientIP(r *http.Request, hops int) string {
	if hops > 0 {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			parts := strings.Split(strings.Join(xff, ","), ",")
			if len(parts) >= hops {
				if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-hops])); ip != nil {
					return ip.String()
				}
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

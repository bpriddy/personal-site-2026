// Package connect gives the front-end builder connections to outside
// services: tools it can call during a run (search Sketchfab for a 3D model
// and import it, take a texture from Poly Haven, self-host a Google Font).
// Each connection is optional: it's on when it's configured (an API key where
// one is needed), and the builder's tool list is the union of what's on.
//
// What a connection imports is copied into the site's media store under
// /media/assets/... (internal/media): front ends run in a sandbox whose CSP
// only allows the user-content origin, so nothing is hot-linked.
// docs/connections.md describes each connection and how to add one.
package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/media"
)

// Tool is one tool a connection gives the builder. Its input schema is an
// object with these properties, all required (strict tool use).
type Tool struct {
	Name        string
	Description string
	Props       map[string]any
}

// Output is a tool's result for the model: text, plus images (https URLs)
// the model sees, e.g. search thumbnails.
type Output struct {
	Text   string
	Images []string
	// Credit, when set, is a credit line an import's license requires the
	// front end to show (the builder checks for it before saving).
	Credit *Credit
}

// Credit is what a front end must show for an imported asset.
type Credit struct {
	Asset string // the /media/... path
	Line  string // the full credit line
	Must  string // a short string that must appear in the front end's files (e.g. the author)
}

// Connection is an outside service the builder can use.
type Connection interface {
	Name() string
	Tools() []Tool
	// Call runs one of its tools. An error is shown to the model as a failed
	// tool call (so its message should say what to do instead).
	Call(ctx context.Context, tool string, input json.RawMessage) (Output, error)
}

// Store is where imports go: the site's media store (read and write).
type Store interface {
	media.Source
	media.Writer
}

// openAll reads a stored asset whole.
func openAll(ctx context.Context, st Store, name string) ([]byte, error) {
	f, err := st.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f.Body)
}

// UserError is an error whose message is meant for the model as is.
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

// Userf makes a UserError.
func Userf(format string, a ...any) error { return &UserError{Msg: fmt.Sprintf(format, a...)} }

// ── per-run budgets ──

type budgetKey struct{}

// Budget counts uses of limited operations (imports) within one builder run.
type Budget struct {
	mu   sync.Mutex
	used map[string]int
}

// WithBudget gives ctx a fresh per-run budget.
func WithBudget(ctx context.Context) context.Context {
	return context.WithValue(ctx, budgetKey{}, &Budget{used: map[string]int{}})
}

// Spend takes one use of what from ctx's budget, or returns a UserError when
// max are used up. Without a budget in ctx, it allows everything.
func Spend(ctx context.Context, what string, max int) error {
	b, _ := ctx.Value(budgetKey{}).(*Budget)
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used[what] >= max {
		return Userf("At most %d %s per run; reuse what you've already imported.", max, what)
	}
	b.used[what]++
	return nil
}

// ── HTTP helpers ──

// Client is the HTTP client connections use (tests replace it).
var Client = &http.Client{Timeout: 90 * time.Second}

// getJSON GETs url (with optional headers) into out.
func getJSON(ctx context.Context, url string, hdr map[string]string, out any) error {
	b, err := get(ctx, url, hdr, 4<<20)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// ErrTooBig is returned by get for a body over its limit.
var ErrTooBig = errors.New("connect: response too large")

// StatusError is a non-2xx response.
type StatusError struct {
	Code int
	URL  string
}

func (e *StatusError) Error() string { return fmt.Sprintf("HTTP %d from %s", e.Code, e.URL) }

// get GETs url and returns at most max bytes of body.
func get(ctx context.Context, url string, hdr map[string]string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "benpriddy.com-builder/1 (+https://benpriddy.com)")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, &StatusError{Code: resp.StatusCode, URL: req.URL.Redacted()}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, ErrTooBig
	}
	return b, nil
}

// MB formats a byte count.
func MB(n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d KB", (n+1023)>>10)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

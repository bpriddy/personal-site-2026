package store

import (
	"context"
	"time"
)

// Notifies stores "email me when it's ready" requests for builder runs
// (migration 0013; protocol v1.13). The address and the open-link token are
// kept only until the notice is taken. Memory and Postgres implement it
// alongside Builder; the server finds it with a type assertion.
type Notifies interface {
	// SetRunNotify records (or replaces) the request for n.RunID.
	// ErrNotFound for an unknown run.
	SetRunNotify(ctx context.Context, n RunNotify) error
	// RunNotify returns the pending request for a run; ErrNotFound if there
	// is none (never asked, removed, or already taken).
	RunNotify(ctx context.Context, runID int64) (RunNotify, error)
	// ClearRunNotify removes a run's pending request (the visitor changed
	// their mind). No error if there is none.
	ClearRunNotify(ctx context.Context, runID int64) error
	// TakeRunNotify atomically returns a run's pending request and clears
	// its address and link, recording outcome. ok is false if there was none.
	TakeRunNotify(ctx context.Context, runID int64, outcome string) (n RunNotify, ok bool, err error)
	// NotifyCount counts requests for an address (by hash) made at or after
	// since, taken or not.
	NotifyCount(ctx context.Context, addressHash []byte, since time.Time) (int, error)
}

// RunNotify is one run's request to be emailed when it ends.
type RunNotify struct {
	RunID       int64
	Email       string
	Link        string // the open-link token for the email
	AddressHash []byte
	CreatedAt   time.Time
}

package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Builder persists prompted front ends, their immutable revisions and builder
// runs (migration 0002). Memory and Postgres implement it alongside Store; the
// server finds it with a type assertion, so Store itself doesn't grow.
type Builder interface {
	// BuilderFrontends lists every registered front end (built-in and
	// prompted), ordered by ID.
	BuilderFrontends(ctx context.Context) ([]FrontendInfo, error)
	// BuilderFrontend returns one front end; ErrNotFound if unregistered.
	BuilderFrontend(ctx context.Context, id string) (FrontendInfo, error)
	// CreatePromptedFrontend registers a new prompted front end ("fe/<slug>"),
	// not in the rotation and with no revisions; ErrExists if the ID is taken.
	CreatePromptedFrontend(ctx context.Context, id, title string) error

	// AddRevision stores a new revision of rev.FrontendID. It assigns
	// rev.Number (the next number for that front end) and CreatedAt, and
	// returns the stored revision. ErrNotFound if the front end isn't
	// registered or the parent isn't a revision of the same front end;
	// ErrExists if the ID is taken. Revisions are never updated.
	AddRevision(ctx context.Context, rev Revision) (Revision, error)
	// Revision returns one revision; ErrNotFound if unknown.
	Revision(ctx context.Context, id string) (Revision, error)
	// Revisions lists a front end's revisions, newest (highest number) first.
	Revisions(ctx context.Context, frontendID string) ([]Revision, error)
	// SetActiveRevision makes revID the front end's active (served) revision;
	// ErrNotFound unless revID is a revision of frontendID.
	SetActiveRevision(ctx context.Context, frontendID, revID string) error

	// StartRun records a builder run as running and returns its ID.
	StartRun(ctx context.Context, run Run) (int64, error)
	// FinishRun marks a run done (with its revision) or failed (with errMsg).
	FinishRun(ctx context.Context, id int64, revisionID, errMsg string) error
	// Runs lists a front end's most recent runs, newest first.
	Runs(ctx context.Context, frontendID string, limit int) ([]Run, error)
}

// ErrExists is returned when creating something whose ID is already taken.
var ErrExists = errors.New("already exists")

// Front-end kinds.
const (
	KindBuiltin  = "builtin"
	KindPrompted = "prompted"
)

// FrontendInfo is a front end as the builder sees it.
type FrontendInfo struct {
	ID             string // "builtin/<name>" or "fe/<slug>"
	Title          string
	Kind           string // KindBuiltin or KindPrompted
	InRotation     bool
	ActiveRevision string // prompted only; "" = none yet
	UpdatedAt      time.Time
}

// Revision is one immutable snapshot of a prompted front end. Its files live
// in front-end storage under rev/<ID>/; Files is their manifest.
type Revision struct {
	ID           string // [a-z0-9]{8,40}; see frontend.NewRevisionID
	FrontendID   string
	Number       int    // 1, 2, ... within the front end
	ParentID     string // "" for a first revision
	CreatedAt    time.Time
	Author       string // "ben", "observer", ...
	Summary      string
	Conversation json.RawMessage // the prompt history that produced it (JSON array)
	Files        []FileInfo
}

// FileInfo describes one file of a revision.
type FileInfo struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Run statuses.
const (
	RunRunning = "running"
	RunDone    = "done"
	RunFailed  = "failed"
)

// Run is one builder chat turn: a prompt applied to a parent revision.
type Run struct {
	ID         int64
	FrontendID string
	ParentID   string
	Prompt     string
	Status     string
	RevisionID string
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time // zero while running
}

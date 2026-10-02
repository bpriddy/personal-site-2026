package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// The site observer's persistence (docs/observer.md, "Layer 3"). It is a
// separate interface from Store: ObserverMemory backs it in dev and tests, and
// PostgresObserver shares the Postgres pool. Both pass the same conformance
// suite (observer_test.go).

// Detection kinds.
const (
	KindContentGap     = "content-gap"
	KindTypeBreak      = "type-break"
	KindFrontendError  = "frontend-error"
	KindContentInvalid = "content-invalid"
	// KindContentDrift: a prompted front end in the rotation doesn't read a
	// collection (or a widely used field) the content now has (migration 0006).
	KindContentDrift = "content-drift"
)

// Detection statuses.
const (
	StatusNew         = "new"
	StatusFixed       = "fixed"
	StatusNeedsReview = "needs_review"
	StatusReverted    = "reverted"
	StatusDismissed   = "dismissed"
)

// Generated-field statuses. Active and accepted values are served; cleared ones
// were reverted by the admin (and are not regenerated automatically); stale
// ones came from human content that has since changed.
const (
	GenActive   = "active"
	GenAccepted = "accepted"
	GenCleared  = "cleared"
	GenStale    = "stale"
)

// A Detection is one deduplicated problem: every report with the same
// Signature increments Count and refreshes LastSeen and Sample.
type Detection struct {
	ID         int64
	Kind       string
	Frontend   string
	Serve      string
	Route      string
	Collection string
	Item       string
	Field      string
	Expect     string
	Got        string
	Signature  string
	Count      int
	FirstSeen  time.Time
	LastSeen   time.Time
	Sample     json.RawMessage // the latest report, as data (untrusted input)
	Status     string
	Action     Action
	SeenAt     *time.Time
}

// Action records what the observer did (or proposes, when it needs review).
type Action struct {
	Type    string `json:"type,omitempty"`    // generate, coerce, pull, review, accept, edit, revert, dismiss, resolved, ...
	Summary string `json:"summary,omitempty"` // human-readable, shown in the admin
	Value   string `json:"value,omitempty"`   // a generated or proposed value
	Auto    bool   `json:"auto,omitempty"`    // applied by the observer itself
	Pulled  string `json:"pulled,omitempty"`  // front end pulled from the rotation, if any
	// Content drift (a creative rebuild): the revision the observer made, the
	// one that was active before, their numbers, and the end of the probation
	// during which errors roll the front end back to Previous.
	Revision       string    `json:"revision,omitempty"`
	RevisionNumber int       `json:"revisionNumber,omitempty"`
	Previous       string    `json:"previous,omitempty"`
	PreviousNumber int       `json:"previousNumber,omitempty"`
	Activated      bool      `json:"activated,omitempty"` // the observer made Revision active
	ProbationUntil time.Time `json:"probationUntil,omitzero"`
	// SourceHash is the item's content hash when the observer last acted, so it
	// doesn't retry an unfixable gap until the content changes.
	SourceHash string    `json:"sourceHash,omitempty"`
	At         time.Time `json:"at,omitzero"`
}

// GeneratedField is an observer-generated value for one field of one item.
type GeneratedField struct {
	Collection string
	Item       string
	Field      string
	Value      string // text as is; list, number and bool values as JSON
	Expect     string // text, list, number, bool
	Model      string // model id, or "human" for a value set in the admin
	SourceHash string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type ObserverStore interface {
	// RecordDetection inserts d, or, if its Signature exists, increments the
	// count and refreshes last_seen, sample, serve, route, expect and got from
	// the latest report (status, action and seen_at are kept). It returns the stored row and whether it was created.
	RecordDetection(ctx context.Context, d Detection) (Detection, bool, error)
	// Detection returns one detection; ErrNotFound if absent.
	Detection(ctx context.Context, id int64) (Detection, error)
	// DetectionBySignature returns one detection; ErrNotFound if absent.
	DetectionBySignature(ctx context.Context, sig string) (Detection, error)
	// Detections lists detections whose status is one of statuses (all if
	// none), most recently seen first, at most limit (0 means 200).
	Detections(ctx context.Context, limit int, statuses ...string) ([]Detection, error)
	// SetDetection sets a detection's status and action; ErrNotFound if absent.
	SetDetection(ctx context.Context, id int64, status string, a Action) error
	// MarkDetectionsSeen sets seen_at on those of ids not yet seen.
	MarkDetectionsSeen(ctx context.Context, ids ...int64) error
	// UnseenDetections counts detections never viewed in the admin.
	UnseenDetections(ctx context.Context) (int, error)
	// DetectionCounts counts detections by status.
	DetectionCounts(ctx context.Context) (map[string]int, error)

	// ClaimRebuild records the start of a creative rebuild (content drift)
	// for r.Frontend and r.Fingerprint, atomically across instances. It
	// fails with ErrRebuildDone if that pair was attempted before (one
	// attempt ever; an attempt left running longer than RebuildStale is taken
	// over instead), ErrRebuildBusy if another rebuild is running, and
	// ErrRebuildQuota if perDay rebuilds started in the last 24 hours.
	ClaimRebuild(ctx context.Context, r Rebuild, perDay int) (Rebuild, error)
	// FinishRebuild marks a rebuild done (with its revision) or failed.
	FinishRebuild(ctx context.Context, id int64, revision, errMsg string) error
	// CancelRebuild deletes a claimed rebuild that never ran, so it neither
	// counts nor blocks a later attempt.
	CancelRebuild(ctx context.Context, id int64) error
	// Rebuilds lists rebuilds, newest first, at most limit (0 means 50).
	Rebuilds(ctx context.Context, limit int) ([]Rebuild, error)

	// GeneratedField returns one generated value; ErrNotFound if absent.
	GeneratedField(ctx context.Context, collection, item, field string) (GeneratedField, error)
	// PutGeneratedField inserts or replaces a generated value (created_at is
	// kept on replace).
	PutGeneratedField(ctx context.Context, g GeneratedField) error
	// SetGeneratedStatus changes a generated value's status; ErrNotFound if absent.
	SetGeneratedStatus(ctx context.Context, collection, item, field, status string) error
	// GeneratedFields lists generated values with one of statuses (all if
	// none), ordered by collection, item, field.
	GeneratedFields(ctx context.Context, statuses ...string) ([]GeneratedField, error)
}

// Rebuild is one creative rebuild attempt by the observer (content drift).
type Rebuild struct {
	ID          int64
	Frontend    string
	Fingerprint string // the missing set it tried to fix
	DetectionID int64
	Parent      string // the revision it started from
	Revision    string // the revision it made, if any
	Status      string // RunRunning, RunDone, RunFailed
	Error       string
	StartedAt   time.Time
	FinishedAt  time.Time // zero while running
}

// RebuildStale is how long a rebuild may stay "running" before it is
// presumed dead (its instance stopped) and may be taken over.
const RebuildStale = 30 * time.Minute

var (
	ErrRebuildDone  = errors.New("store: this rebuild was attempted before")
	ErrRebuildBusy  = errors.New("store: another rebuild is running")
	ErrRebuildQuota = errors.New("store: daily rebuild budget used up")
)

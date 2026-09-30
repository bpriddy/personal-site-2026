package store

import (
	"context"
	"encoding/json"
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

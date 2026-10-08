package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Visitors persists the public builder's state (migration 0004): front ends
// owned by anonymous visitor sessions, the runs the build limits count, and
// submissions for Ben's review. Memory and Postgres implement it alongside
// Builder; the server finds it with a type assertion.
//
// Sessions are identified only by sha256(sid) (32 bytes); the raw cookie value
// never reaches the store.
type Visitors interface {
	// CreateVisitorFrontend registers a new prompted front end owned by
	// session (recording the session if it is new): not in the rotation, no
	// revisions. ErrExists if the ID is taken, by anyone.
	CreateVisitorFrontend(ctx context.Context, id, title string, session []byte) error
	// OwnedFrontend returns the front end if session owns it. ErrNotFound if it
	// doesn't exist or has another owner (or none), so ownership never leaks.
	OwnedFrontend(ctx context.Context, id string, session []byte) (FrontendInfo, error)
	// TransferFrontend makes session the owner of a visitor's front end
	// (recording the session if it is new); the previous owner loses it
	// (protocol v1.13, an email's link). ErrNotFound unless the front end
	// exists and a visitor owns it: Ben's own and the built-ins never move.
	TransferFrontend(ctx context.Context, id string, session []byte) error
	// SessionFrontends lists the session's front ends, most recently updated first.
	SessionFrontends(ctx context.Context, session []byte) ([]FrontendInfo, error)
	// VisitorFrontendIDs returns the IDs of every front end a visitor owns.
	VisitorFrontendIDs(ctx context.Context) (map[string]bool, error)

	// VisitorRunCounts counts visitor runs against q's windows, without
	// starting one (to tell a visitor up front that a limit is reached).
	VisitorRunCounts(ctx context.Context, session, ipHash []byte, q RunQuota) (RunCounts, error)
	// StartVisitorRun atomically checks q and, if no limit is reached, records
	// a visitor's run as running (like Builder.StartRun) and returns its ID.
	// A reached limit returns a *QuotaError. Across instances, the check and
	// the insert are serialized, so the global cap holds.
	StartVisitorRun(ctx context.Context, run Run, session, ipHash []byte, q RunQuota) (int64, error)

	// Submit marks revisionID (a revision of frontendID) submitted for review.
	// If the front end already has a pending submission, it now points at
	// revisionID. Submitting the revision of the latest submission again
	// changes nothing (whatever its status). ErrNotFound if the revision isn't
	// one of the front end's.
	Submit(ctx context.Context, frontendID, revisionID string) (Submission, error)
	// LatestSubmission returns the front end's most recent submission;
	// ErrNotFound if it was never submitted.
	LatestSubmission(ctx context.Context, frontendID string) (Submission, error)
	// Submissions lists pending submissions (oldest first), then up to limit
	// reviewed ones (most recently reviewed first).
	Submissions(ctx context.Context, limit int) ([]Submission, error)
	// PendingSubmissions counts submissions awaiting review.
	PendingSubmissions(ctx context.Context) (int, error)
	// ReviewSubmission approves or rejects a pending submission. Approving
	// makes its revision the front end's active revision and adds the front
	// end to the rotation, in one transaction. ErrNotFound for an unknown
	// submission, ErrNotPending if it was already reviewed.
	ReviewSubmission(ctx context.Context, id int64, approve bool) (Submission, error)
}

// ErrNotPending is returned when reviewing a submission that was already reviewed.
var ErrNotPending = errors.New("submission is not pending")

// Submission statuses.
const (
	SubmissionPending  = "pending"
	SubmissionApproved = "approved"
	SubmissionRejected = "rejected"
)

// Submission is a revision sent to Ben's review.
type Submission struct {
	ID          int64
	FrontendID  string
	RevisionID  string
	Status      string
	SubmittedAt time.Time
	ReviewedAt  time.Time // zero while pending

	// filled in by Submissions and LatestSubmission, for display
	Title          string // the front end's title
	RevisionNumber int
}

// RunQuota is the build limits for one visitor run, with the windows they
// count in. A limit <= 0 is not enforced.
type RunQuota struct {
	SessionHour int // runs per session since HourStart
	IPDay       int // runs per client IP since DayStart
	GlobalDay   int // visitor runs in total since DayStart
	Concurrent  int // running runs per session started since RunningSince

	HourStart    time.Time // e.g. now - 1h
	DayStart     time.Time // e.g. midnight UTC today
	RunningSince time.Time // runs older than this count as dead, not running
}

// RunCounts is what the limits count.
type RunCounts struct {
	Running     int // the session's running runs
	SessionHour int
	IPDay       int
	GlobalDay   int
}

// Limits that a QuotaError can name.
const (
	LimitConcurrent  = "concurrent"
	LimitSessionHour = "session-hour"
	LimitIPDay       = "ip-day"
	LimitGlobalDay   = "global-day"
)

// QuotaError says which build limit stopped a run.
type QuotaError struct{ Limit string }

func (e *QuotaError) Error() string { return fmt.Sprintf("build limit reached: %s", e.Limit) }

// Check returns a *QuotaError for the first limit c reaches, or nil. The
// order is the one a visitor can act on soonest: one at a time, then the
// hourly, daily and global limits.
func (q RunQuota) Check(c RunCounts) error {
	switch {
	case q.Concurrent > 0 && c.Running >= q.Concurrent:
		return &QuotaError{LimitConcurrent}
	case q.GlobalDay > 0 && c.GlobalDay >= q.GlobalDay:
		return &QuotaError{LimitGlobalDay}
	case q.IPDay > 0 && c.IPDay >= q.IPDay:
		return &QuotaError{LimitIPDay}
	case q.SessionHour > 0 && c.SessionHour >= q.SessionHour:
		return &QuotaError{LimitSessionHour}
	}
	return nil
}

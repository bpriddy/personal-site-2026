package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var _ Visitors = (*Postgres)(nil)

// visitorRunLock is the pg_advisory_xact_lock key that serializes visitor run
// starts across instances, so the limits' count-then-insert can't race.
const visitorRunLock = 7_310_201_412

// upsertSession records a visitor session (or refreshes its last_seen).
const upsertSession = `INSERT INTO visitor_sessions (id_hash) VALUES ($1)
	ON CONFLICT (id_hash) DO UPDATE SET last_seen = now()`

func (p *Postgres) CreateVisitorFrontend(ctx context.Context, id, title string, session []byte) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, upsertSession, session); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO frontends (ref, title, kind, in_rotation, owner_session)
			VALUES ($1, $2, 'prompted', false, $3)`, id, title, session)
		if pgErrCode(err) == "23505" {
			return ErrExists
		}
		return err
	})
}

func (p *Postgres) OwnedFrontend(ctx context.Context, id string, session []byte) (FrontendInfo, error) {
	if len(session) == 0 {
		return FrontendInfo{}, ErrNotFound
	}
	f, err := scanFrontendInfo(p.pool.QueryRow(ctx,
		`SELECT `+frontendInfoCols+` FROM frontends WHERE ref = $1 AND owner_session = $2`, id, session))
	return f, notFound(err)
}

func (p *Postgres) SessionFrontends(ctx context.Context, session []byte) ([]FrontendInfo, error) {
	if len(session) == 0 {
		return []FrontendInfo{}, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT `+frontendInfoCols+` FROM frontends
		WHERE owner_session = $1 ORDER BY updated_at DESC, ref`, session)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (FrontendInfo, error) { return scanFrontendInfo(r) })
	if out == nil {
		out = []FrontendInfo{}
	}
	return out, err
}

func (p *Postgres) VisitorFrontendIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := p.pool.Query(ctx, `SELECT ref FROM frontends WHERE owner_session IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func visitorCounts(ctx context.Context, q querier, session, ip []byte, quota RunQuota) (RunCounts, error) {
	var c RunCounts
	err := q.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE session_hash = $1 AND status = 'running' AND started_at >= $3),
		  count(*) FILTER (WHERE session_hash = $1 AND started_at >= $4),
		  count(*) FILTER (WHERE ip_hash = $2 AND started_at >= $5),
		  count(*) FILTER (WHERE started_at >= $5)
		FROM builder_runs
		WHERE session_hash IS NOT NULL AND started_at >= least($3::timestamptz, $4::timestamptz, $5::timestamptz)`,
		session, ip, quota.RunningSince, quota.HourStart, quota.DayStart).
		Scan(&c.Running, &c.SessionHour, &c.IPDay, &c.GlobalDay)
	return c, err
}

func (p *Postgres) VisitorRunCounts(ctx context.Context, session, ipHash []byte, q RunQuota) (RunCounts, error) {
	return visitorCounts(ctx, p.pool, session, ipHash, q)
}

func (p *Postgres) StartVisitorRun(ctx context.Context, run Run, session, ipHash []byte, q RunQuota) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(visitorRunLock)); err != nil {
			return err
		}
		c, err := visitorCounts(ctx, tx, session, ipHash, q)
		if err != nil {
			return err
		}
		if err := q.Check(c); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, upsertSession, session); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO builder_runs (frontend_id, parent_id, prompt, session_hash, ip_hash)
			VALUES ($1, nullif($2, ''), $3, $4, $5) RETURNING id`,
			run.FrontendID, run.ParentID, run.Prompt, session, ipHash).Scan(&id)
		if pgErrCode(err) == "23503" {
			return ErrNotFound
		}
		return err
	})
	return id, err
}

const submissionCols = `s.id, s.frontend_id, s.revision_id, s.status, s.submitted_at, s.reviewed_at, f.title, r.number`
const submissionFrom = ` FROM frontend_submissions s JOIN frontends f ON f.ref = s.frontend_id
	JOIN frontend_revisions r ON r.id = s.revision_id`

func scanSubmission(r pgx.Row) (Submission, error) {
	var s Submission
	var reviewed *time.Time
	err := r.Scan(&s.ID, &s.FrontendID, &s.RevisionID, &s.Status, &s.SubmittedAt, &reviewed, &s.Title, &s.RevisionNumber)
	if reviewed != nil {
		s.ReviewedAt = *reviewed
	}
	return s, err
}

func (p *Postgres) submission(ctx context.Context, q querier, id int64) (Submission, error) {
	s, err := scanSubmission(q.QueryRow(ctx, `SELECT `+submissionCols+submissionFrom+` WHERE s.id = $1`, id))
	return s, notFound(err)
}

func (p *Postgres) Submit(ctx context.Context, frontendID, revisionID string) (Submission, error) {
	var out Submission
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		// serialize submissions per front end (and check the revision is its)
		var one int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM frontends WHERE ref = $1 FOR UPDATE`, frontendID).Scan(&one); err != nil {
			return notFound(err)
		}
		var fe string
		if err := tx.QueryRow(ctx, `SELECT frontend_id FROM frontend_revisions WHERE id = $1`, revisionID).Scan(&fe); err != nil {
			return notFound(err)
		}
		if fe != frontendID {
			return ErrNotFound
		}
		var id int64
		var rev, status string
		err := tx.QueryRow(ctx, `SELECT id, revision_id, status FROM frontend_submissions
			WHERE frontend_id = $1 ORDER BY submitted_at DESC, id DESC LIMIT 1`, frontendID).Scan(&id, &rev, &status)
		switch {
		case err == nil && rev == revisionID:
			// unchanged
		case err == nil && status == SubmissionPending:
			if _, err := tx.Exec(ctx, `UPDATE frontend_submissions SET revision_id = $2, submitted_at = now() WHERE id = $1`,
				id, revisionID); err != nil {
				return err
			}
		case err == nil || errors.Is(err, pgx.ErrNoRows):
			if err := tx.QueryRow(ctx, `INSERT INTO frontend_submissions (frontend_id, revision_id) VALUES ($1, $2) RETURNING id`,
				frontendID, revisionID).Scan(&id); err != nil {
				return err
			}
		default:
			return err
		}
		out, err = p.submission(ctx, tx, id)
		return err
	})
	return out, err
}

func (p *Postgres) LatestSubmission(ctx context.Context, frontendID string) (Submission, error) {
	s, err := scanSubmission(p.pool.QueryRow(ctx, `SELECT `+submissionCols+submissionFrom+`
		WHERE s.frontend_id = $1 ORDER BY s.submitted_at DESC, s.id DESC LIMIT 1`, frontendID))
	return s, notFound(err)
}

func (p *Postgres) Submissions(ctx context.Context, limit int) ([]Submission, error) {
	collect := func(sql string, args ...any) ([]Submission, error) {
		rows, err := p.pool.Query(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Submission, error) { return scanSubmission(r) })
	}
	pending, err := collect(`SELECT ` + submissionCols + submissionFrom + `
		WHERE s.status = 'pending' ORDER BY s.submitted_at, s.id`)
	if err != nil {
		return nil, err
	}
	reviewed, err := collect(`SELECT `+submissionCols+submissionFrom+`
		WHERE s.status <> 'pending' ORDER BY s.reviewed_at DESC, s.id DESC LIMIT $1`, max(limit, 0))
	if err != nil {
		return nil, err
	}
	return append(append([]Submission{}, pending...), reviewed...), nil
}

func (p *Postgres) PendingSubmissions(ctx context.Context) (int, error) {
	var n int
	err := p.pool.QueryRow(ctx, `SELECT count(*) FROM frontend_submissions WHERE status = 'pending'`).Scan(&n)
	return n, err
}

func (p *Postgres) ReviewSubmission(ctx context.Context, id int64, approve bool) (Submission, error) {
	var out Submission
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		var fe, rev, status string
		err := tx.QueryRow(ctx, `SELECT frontend_id, revision_id, status FROM frontend_submissions WHERE id = $1 FOR UPDATE`, id).
			Scan(&fe, &rev, &status)
		if err != nil {
			return notFound(err)
		}
		if status != SubmissionPending {
			return ErrNotPending
		}
		next := SubmissionRejected
		if approve {
			next = SubmissionApproved
			tag, err := tx.Exec(ctx, `
				UPDATE frontends SET active_revision = $2, in_rotation = true, updated_at = now()
				WHERE ref = $1 AND EXISTS (SELECT 1 FROM frontend_revisions WHERE id = $2 AND frontend_id = $1)`, fe, rev)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE frontend_submissions SET status = $2, reviewed_at = now() WHERE id = $1`, id, next); err != nil {
			return err
		}
		out, err = p.submission(ctx, tx, id)
		return err
	})
	return out, err
}

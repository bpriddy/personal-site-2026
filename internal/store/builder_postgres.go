package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var _ Builder = (*Postgres)(nil)

// pgErrCode returns the SQLSTATE of a Postgres error, or "".
func pgErrCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

const frontendInfoCols = `ref, title, kind, in_rotation, coalesce(active_revision, ''), credit, credit_requested, updated_at`

func scanFrontendInfo(r pgx.Row) (FrontendInfo, error) {
	var f FrontendInfo
	err := r.Scan(&f.ID, &f.Title, &f.Kind, &f.InRotation, &f.ActiveRevision, &f.Credit, &f.CreditRequested, &f.UpdatedAt)
	return f, err
}

func (p *Postgres) BuilderFrontends(ctx context.Context) ([]FrontendInfo, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+frontendInfoCols+` FROM frontends ORDER BY ref`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FrontendInfo, error) { return scanFrontendInfo(r) })
}

func (p *Postgres) BuilderFrontend(ctx context.Context, id string) (FrontendInfo, error) {
	f, err := scanFrontendInfo(p.pool.QueryRow(ctx, `SELECT `+frontendInfoCols+` FROM frontends WHERE ref = $1`, id))
	return f, notFound(err)
}

func (p *Postgres) CreatePromptedFrontend(ctx context.Context, id, title string) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO frontends (ref, title, kind, in_rotation) VALUES ($1, $2, 'prompted', false)`, id, title)
	if pgErrCode(err) == "23505" { // unique_violation
		return ErrExists
	}
	return err
}

func (p *Postgres) SetCredit(ctx context.Context, id, credit string) error {
	return p.setCredit(ctx, `UPDATE frontends SET credit = $2 WHERE ref = $1`, id, credit)
}

func (p *Postgres) SetCreditRequested(ctx context.Context, id, credit string) error {
	return p.setCredit(ctx, `UPDATE frontends SET credit_requested = $2 WHERE ref = $1`, id, credit)
}

func (p *Postgres) setCredit(ctx context.Context, sql, id, credit string) error {
	tag, err := p.pool.Exec(ctx, sql, id, credit)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

const revisionCols = `id, frontend_id, number, coalesce(parent_id, ''), created_at, author, summary, conversation, files`

func scanRevision(r pgx.Row) (Revision, error) {
	var rev Revision
	var conv, files []byte
	err := r.Scan(&rev.ID, &rev.FrontendID, &rev.Number, &rev.ParentID, &rev.CreatedAt,
		&rev.Author, &rev.Summary, &conv, &files)
	if err != nil {
		return Revision{}, err
	}
	rev.Conversation = json.RawMessage(conv)
	if err := json.Unmarshal(files, &rev.Files); err != nil {
		return Revision{}, err
	}
	if rev.Files == nil {
		rev.Files = []FileInfo{}
	}
	return rev, nil
}

func (p *Postgres) AddRevision(ctx context.Context, rev Revision) (Revision, error) {
	conv := rev.Conversation
	if len(conv) == 0 {
		conv = json.RawMessage("[]")
	}
	if rev.Files == nil {
		rev.Files = []FileInfo{}
	}
	files, err := json.Marshal(rev.Files)
	if err != nil {
		return Revision{}, err
	}
	var out Revision
	err = pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		// serialize numbering per front end (and check it exists)
		var one int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM frontends WHERE ref = $1 FOR UPDATE`, rev.FrontendID).Scan(&one); err != nil {
			return notFound(err)
		}
		if rev.ParentID != "" {
			var fe string
			err := tx.QueryRow(ctx, `SELECT frontend_id FROM frontend_revisions WHERE id = $1`, rev.ParentID).Scan(&fe)
			if err != nil {
				return notFound(err)
			}
			if fe != rev.FrontendID {
				return ErrNotFound
			}
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO frontend_revisions (id, frontend_id, number, parent_id, author, summary, conversation, files)
			VALUES ($1, $2, (SELECT coalesce(max(number), 0) + 1 FROM frontend_revisions WHERE frontend_id = $2),
			        nullif($3, ''), $4, $5, $6, $7)
			RETURNING `+revisionCols,
			rev.ID, rev.FrontendID, rev.ParentID, rev.Author, rev.Summary, []byte(conv), files)
		var err error
		out, err = scanRevision(row)
		if pgErrCode(err) == "23505" {
			return ErrExists
		}
		return err
	})
	return out, err
}

func (p *Postgres) Revision(ctx context.Context, id string) (Revision, error) {
	rev, err := scanRevision(p.pool.QueryRow(ctx, `SELECT `+revisionCols+` FROM frontend_revisions WHERE id = $1`, id))
	return rev, notFound(err)
}

func (p *Postgres) Revisions(ctx context.Context, frontendID string) ([]Revision, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT `+revisionCols+` FROM frontend_revisions WHERE frontend_id = $1 ORDER BY number DESC`, frontendID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Revision, error) { return scanRevision(r) })
}

func (p *Postgres) SetActiveRevision(ctx context.Context, frontendID, revID string) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE frontends SET active_revision = $2, updated_at = now()
		WHERE ref = $1 AND EXISTS (SELECT 1 FROM frontend_revisions WHERE id = $2 AND frontend_id = $1)`,
		frontendID, revID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) StartRun(ctx context.Context, run Run) (int64, error) {
	var id int64
	err := p.pool.QueryRow(ctx, `
		INSERT INTO builder_runs (frontend_id, parent_id, prompt) VALUES ($1, nullif($2, ''), $3)
		RETURNING id`, run.FrontendID, run.ParentID, run.Prompt).Scan(&id)
	if pgErrCode(err) == "23503" { // foreign_key_violation
		return 0, ErrNotFound
	}
	return id, err
}

func (p *Postgres) FinishRun(ctx context.Context, id int64, revisionID, errMsg string) error {
	status := RunDone
	if errMsg != "" {
		status = RunFailed
	}
	tag, err := p.pool.Exec(ctx, `
		UPDATE builder_runs SET status = $2, revision_id = nullif($3, ''), error = $4, finished_at = now()
		WHERE id = $1`, id, status, revisionID, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) Runs(ctx context.Context, frontendID string, limit int) ([]Run, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, frontend_id, coalesce(parent_id, ''), prompt, status, coalesce(revision_id, ''), error,
		       started_at, finished_at
		FROM builder_runs WHERE frontend_id = $1 ORDER BY started_at DESC, id DESC LIMIT $2`, frontendID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) {
		var run Run
		var fin *time.Time
		err := r.Scan(&run.ID, &run.FrontendID, &run.ParentID, &run.Prompt, &run.Status, &run.RevisionID,
			&run.Error, &run.StartedAt, &fin)
		if fin != nil {
			run.FinishedAt = *fin
		}
		return run, err
	})
}

func (p *Postgres) DeleteRevision(ctx context.Context, frontendID, revID string) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		var active, parent string
		err := tx.QueryRow(ctx, `
			SELECT coalesce(f.active_revision, ''), coalesce(r.parent_id, '')
			FROM frontend_revisions r JOIN frontends f ON f.ref = r.frontend_id
			WHERE r.id = $1 AND r.frontend_id = $2 FOR UPDATE OF f`, revID, frontendID).Scan(&active, &parent)
		if err != nil {
			return notFound(err)
		}
		if active == revID {
			return ErrActiveRevision
		}
		var pending bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM frontend_submissions WHERE revision_id = $1 AND status = 'pending')`,
			revID).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return ErrPendingRevision
		}
		if _, err := tx.Exec(ctx, `UPDATE frontend_revisions SET parent_id = nullif($2, '') WHERE parent_id = $1`, revID, parent); err != nil {
			return err
		}
		for _, q := range []string{
			`UPDATE builder_runs SET parent_id = NULL WHERE parent_id = $1`,
			`UPDATE builder_runs SET revision_id = NULL WHERE revision_id = $1`,
			`DELETE FROM frontend_submissions WHERE revision_id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, revID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM frontend_revisions WHERE id = $1`, revID)
		return err
	})
}

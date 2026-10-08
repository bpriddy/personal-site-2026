package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var _ Notifies = (*Postgres)(nil)

func (p *Postgres) SetRunNotify(ctx context.Context, n RunNotify) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO build_notifications (run_id, email, link, address_hash)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (run_id) DO UPDATE SET email = $2, link = $3, address_hash = $4,
		       created_at = now(), sent_at = NULL, outcome = NULL`,
		n.RunID, n.Email, n.Link, n.AddressHash)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" { // foreign key: no such run
		return ErrNotFound
	}
	return err
}

func (p *Postgres) RunNotify(ctx context.Context, runID int64) (RunNotify, error) {
	n := RunNotify{RunID: runID}
	err := p.pool.QueryRow(ctx, `
		SELECT email, link, address_hash, created_at FROM build_notifications
		WHERE run_id = $1 AND email IS NOT NULL`, runID).Scan(&n.Email, &n.Link, &n.AddressHash, &n.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunNotify{}, ErrNotFound
	}
	return n, err
}

func (p *Postgres) ClearRunNotify(ctx context.Context, runID int64) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM build_notifications WHERE run_id = $1 AND email IS NOT NULL`, runID)
	return err
}

func (p *Postgres) TakeRunNotify(ctx context.Context, runID int64, outcome string) (RunNotify, bool, error) {
	n := RunNotify{RunID: runID}
	// the old values come back from a self-join on the locked row
	err := p.pool.QueryRow(ctx, `
		UPDATE build_notifications b SET email = NULL, link = NULL, sent_at = now(), outcome = $2
		FROM (SELECT run_id, email, link, address_hash, created_at FROM build_notifications
		      WHERE run_id = $1 AND email IS NOT NULL FOR UPDATE) old
		WHERE b.run_id = old.run_id
		RETURNING old.email, old.link, old.address_hash, old.created_at`, runID, outcome).
		Scan(&n.Email, &n.Link, &n.AddressHash, &n.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunNotify{}, false, nil
	}
	if err != nil {
		return RunNotify{}, false, err
	}
	return n, true, nil
}

func (p *Postgres) NotifyCount(ctx context.Context, addressHash []byte, since time.Time) (int, error) {
	var n int
	err := p.pool.QueryRow(ctx, `
		SELECT count(*) FROM build_notifications WHERE address_hash = $1 AND created_at >= $2`,
		addressHash, since).Scan(&n)
	return n, err
}

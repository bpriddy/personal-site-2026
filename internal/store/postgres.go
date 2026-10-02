package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the Store backed by PostgreSQL (Cloud SQL in production).
type Postgres struct {
	pool *pgxpool.Pool
}

// OpenPostgres connects, verifies the connection, and applies migrations.
// url is a libpq-style URL or DSN; on Cloud Run with Cloud SQL it uses the
// unix socket, e.g. "host=/cloudsql/PROJECT:REGION:INSTANCE dbname=site user=...".
func OpenPostgres(ctx context.Context, url string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() { p.pool.Close() }

// Ping checks the database is reachable (for health checks).
func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (p *Postgres) Page(ctx context.Context, slug string) (content.Page, error) {
	var pg content.Page
	err := p.pool.QueryRow(ctx,
		`SELECT slug, title, body, published, updated_at FROM pages WHERE slug = $1`, slug).
		Scan(&pg.Slug, &pg.Title, &pg.Body, &pg.Published, &pg.UpdatedAt)
	return pg, notFound(err)
}

func (p *Postgres) Pages(ctx context.Context) ([]content.Page, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT slug, title, body, published, updated_at FROM pages ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (content.Page, error) {
		var pg content.Page
		err := r.Scan(&pg.Slug, &pg.Title, &pg.Body, &pg.Published, &pg.UpdatedAt)
		return pg, err
	})
}

func (p *Postgres) SavePage(ctx context.Context, pg content.Page) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO pages (slug, title, body, published, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (slug) DO UPDATE
		SET title = excluded.title, body = excluded.body,
		    published = excluded.published, updated_at = now()`,
		pg.Slug, pg.Title, pg.Body, pg.Published)
	return err
}

func (p *Postgres) Experiment(ctx context.Context, slug string) (content.Experiment, error) {
	var e content.Experiment
	err := p.pool.QueryRow(ctx, `
		SELECT slug, title, summary, link, media, published, sort_order, updated_at
		FROM experiments WHERE slug = $1`, slug).
		Scan(&e.Slug, &e.Title, &e.Summary, &e.Link, &e.Media, &e.Published, &e.Order, &e.UpdatedAt)
	return e, notFound(err)
}

func (p *Postgres) Experiments(ctx context.Context) ([]content.Experiment, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT slug, title, summary, link, media, published, sort_order, updated_at
		FROM experiments ORDER BY sort_order, slug`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (content.Experiment, error) {
		var e content.Experiment
		err := r.Scan(&e.Slug, &e.Title, &e.Summary, &e.Link, &e.Media, &e.Published, &e.Order, &e.UpdatedAt)
		return e, err
	})
}

func (p *Postgres) SaveExperiment(ctx context.Context, e content.Experiment) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO experiments (slug, title, summary, link, media, published, sort_order, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (slug) DO UPDATE
		SET title = excluded.title, summary = excluded.summary, link = excluded.link,
		    media = excluded.media, published = excluded.published,
		    sort_order = excluded.sort_order, updated_at = now()`,
		e.Slug, e.Title, e.Summary, e.Link, mediaList(e.Media), e.Published, e.Order)
	return err
}

func (p *Postgres) Frontends(ctx context.Context) ([]content.Frontend, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT ref, title, in_rotation, updated_at FROM frontends ORDER BY ref`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (content.Frontend, error) {
		var f content.Frontend
		err := r.Scan(&f.Ref, &f.Title, &f.InRotation, &f.UpdatedAt)
		return f, err
	})
}

func (p *Postgres) SetFrontendInRotation(ctx context.Context, ref string, in bool) error {
	tag, err := p.pool.Exec(ctx,
		`UPDATE frontends SET in_rotation = $2, updated_at = now() WHERE ref = $1`, ref, in)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

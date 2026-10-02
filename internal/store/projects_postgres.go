package store

import (
	"context"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/jackc/pgx/v5"
)

var _ Projects = (*Postgres)(nil)

const projectColumns = `slug, title, client, agency, year, tags, roles, summary, contribution,
	body, link, palette, youtube, media, sort_order, published, updated_at`

func scanProject(row pgx.Row) (content.Project, error) {
	var p content.Project
	err := row.Scan(&p.Slug, &p.Title, &p.Client, &p.Agency, &p.Year, &p.Tags, &p.Roles,
		&p.Summary, &p.Contribution, &p.Body, &p.Link, &p.Palette, &p.YouTube, &p.Media,
		&p.Order, &p.Published, &p.UpdatedAt)
	return normProject(p), err
}

func (p *Postgres) Project(ctx context.Context, slug string) (content.Project, error) {
	pr, err := scanProject(p.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE slug = $1`, slug))
	return pr, notFound(err)
}

func (p *Postgres) Projects(ctx context.Context) ([]content.Project, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY sort_order, slug`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (content.Project, error) { return scanProject(r) })
}

func (p *Postgres) SaveProject(ctx context.Context, pr content.Project) error {
	pr = normProject(pr)
	_, err := p.pool.Exec(ctx, `
		INSERT INTO projects (slug, title, client, agency, year, tags, roles, summary, contribution,
		                      body, link, palette, youtube, media, sort_order, published, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, now())
		ON CONFLICT (slug) DO UPDATE
		SET title = excluded.title, client = excluded.client, agency = excluded.agency,
		    year = excluded.year, tags = excluded.tags, roles = excluded.roles,
		    summary = excluded.summary, contribution = excluded.contribution, body = excluded.body,
		    link = excluded.link, palette = excluded.palette, youtube = excluded.youtube,
		    media = excluded.media, sort_order = excluded.sort_order, published = excluded.published,
		    updated_at = now()`,
		pr.Slug, pr.Title, pr.Client, pr.Agency, pr.Year, pr.Tags, pr.Roles, pr.Summary,
		pr.Contribution, pr.Body, pr.Link, pr.Palette, pr.YouTube, pr.Media, pr.Order, pr.Published)
	return err
}

package store

import (
	"context"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/jackc/pgx/v5"
)

var _ ExperienceStore = (*Postgres)(nil)

const experienceColumns = `slug, role, company, start_month, end_month, current, note, sort_order, published, updated_at`

func scanExperience(row pgx.Row) (content.Experience, error) {
	var e content.Experience
	err := row.Scan(&e.Slug, &e.Role, &e.Company, &e.Start, &e.End, &e.Current, &e.Note, &e.Order, &e.Published, &e.UpdatedAt)
	return e, err
}

func (p *Postgres) ExperienceItem(ctx context.Context, slug string) (content.Experience, error) {
	e, err := scanExperience(p.pool.QueryRow(ctx, `SELECT `+experienceColumns+` FROM experience WHERE slug = $1`, slug))
	return e, notFound(err)
}

func (p *Postgres) Experience(ctx context.Context) ([]content.Experience, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+experienceColumns+` FROM experience ORDER BY sort_order, slug`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (content.Experience, error) { return scanExperience(r) })
}

func (p *Postgres) SaveExperience(ctx context.Context, e content.Experience) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO experience (slug, role, company, start_month, end_month, current, note, sort_order, published, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		ON CONFLICT (slug) DO UPDATE
		SET role = excluded.role, company = excluded.company, start_month = excluded.start_month,
		    end_month = excluded.end_month, current = excluded.current, note = excluded.note,
		    sort_order = excluded.sort_order, published = excluded.published, updated_at = now()`,
		e.Slug, e.Role, e.Company, e.Start, e.End, e.Current, e.Note, e.Order, e.Published)
	return err
}

func (p *Postgres) DeleteExperience(ctx context.Context, slug string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM experience WHERE slug = $1`, slug)
	return err
}

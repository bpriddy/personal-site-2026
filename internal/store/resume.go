package store

import (
	"context"
	"encoding/json"
	"errors"
	"maps"

	"github.com/jackc/pgx/v5"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

// ResumeStore keeps /resume's own content (migration 0015). Memory and
// Postgres implement it.
type ResumeStore interface {
	// Resume returns the stored resume; ok is false if none was ever saved.
	Resume(ctx context.Context) (r content.Resume, ok bool, err error)
	// SetResume stores it (whole).
	SetResume(ctx context.Context, r content.Resume) error
}

// ResumeOf returns st's ResumeStore, or nil if it has none.
func ResumeOf(st any) ResumeStore {
	r, _ := st.(ResumeStore)
	return r
}

var _ ResumeStore = (*Memory)(nil)

func (m *Memory) Resume(_ context.Context) (content.Resume, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.resume == nil {
		return content.Resume{}, false, nil
	}
	r := *m.resume
	r.Bullets = maps.Clone(r.Bullets)
	return r, true, nil
}

func (m *Memory) SetResume(_ context.Context, r content.Resume) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.Bullets = maps.Clone(r.Bullets)
	m.resume = &r
	return nil
}

var _ ResumeStore = (*Postgres)(nil)

func (p *Postgres) Resume(ctx context.Context) (content.Resume, bool, error) {
	var raw []byte
	err := p.pool.QueryRow(ctx, `SELECT data FROM site_resume WHERE id`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return content.Resume{}, false, nil
	}
	if err != nil {
		return content.Resume{}, false, err
	}
	var r content.Resume
	if err := json.Unmarshal(raw, &r); err != nil {
		return content.Resume{}, false, err
	}
	return r, true, nil
}

func (p *Postgres) SetResume(ctx context.Context, r content.Resume) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO site_resume (id, data) VALUES (true, $1)
		ON CONFLICT (id) DO UPDATE SET data = $1, updated_at = now()`, raw)
	return err
}

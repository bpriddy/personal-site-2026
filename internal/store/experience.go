package store

import (
	"context"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

// ExperienceStore persists the experience list (migration 0007). Memory and
// Postgres implement it; callers find it with ExperienceOf, so a Store
// without it simply has no experience.
type ExperienceStore interface {
	// ExperienceItem returns one role, published or not; ErrNotFound if absent.
	ExperienceItem(ctx context.Context, slug string) (content.Experience, error)
	// Experience lists every role, published or not, by Order then slug.
	Experience(ctx context.Context) ([]content.Experience, error)
	// SaveExperience inserts or replaces a role (keyed by slug) and stamps UpdatedAt.
	SaveExperience(ctx context.Context, e content.Experience) error
	// DeleteExperience removes a role (no error if it's absent).
	DeleteExperience(ctx context.Context, slug string) error
}

// ExperienceOf returns st's ExperienceStore, or nil if it has none.
func ExperienceOf(st any) ExperienceStore {
	e, _ := st.(ExperienceStore)
	return e
}

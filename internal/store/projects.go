package store

import (
	"context"
	"slices"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

// Projects persists client work (migration 0005). Memory and Postgres
// implement it alongside Store; callers find it with a type assertion
// (ProjectsOf), so a Store without it simply has no projects.
type Projects interface {
	// Project returns one project, published or not; ErrNotFound if absent.
	Project(ctx context.Context, slug string) (content.Project, error)
	// Projects lists every project, published or not, by Order then slug.
	Projects(ctx context.Context) ([]content.Project, error)
	// SaveProject inserts or replaces a project (keyed by slug) and stamps
	// UpdatedAt. Nil lists are stored as empty ones.
	SaveProject(ctx context.Context, p content.Project) error
}

// ProjectsOf returns st's Projects, or nil if it has none.
func ProjectsOf(st any) Projects {
	p, _ := st.(Projects)
	return p
}

// normProject gives a project non-nil lists, so every store returns the same
// shape for "nothing" ([] rather than nil), and copies them, so callers never
// share a slice with the store.
func normProject(p content.Project) content.Project {
	p.Tags = list(p.Tags)
	p.Roles = list(p.Roles)
	p.Palette = list(p.Palette)
	p.Media = mediaList(p.Media)
	return p
}

func list(l []string) []string {
	if l == nil {
		return []string{}
	}
	return slices.Clone(l)
}

func mediaList(l []content.Media) []content.Media {
	if l == nil {
		return []content.Media{}
	}
	return slices.Clone(l)
}

// Package store persists CMS content and the front-end registry. Postgres is
// the real implementation; Memory is an in-process stand-in for tests and
// database-less local dev. Both must pass the same conformance suite
// (storetest_test.go).
package store

import (
	"context"
	"errors"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

var ErrNotFound = errors.New("not found")

type Store interface {
	Page(ctx context.Context, slug string) (content.Page, error)
	Pages(ctx context.Context) ([]content.Page, error)
	SavePage(ctx context.Context, p content.Page) error

	Experiment(ctx context.Context, slug string) (content.Experiment, error)
	Experiments(ctx context.Context) ([]content.Experiment, error)
	SaveExperiment(ctx context.Context, e content.Experiment) error

	// Frontends lists every registered front end, ordered by ref.
	Frontends(ctx context.Context) ([]content.Frontend, error)
	// SetFrontendInRotation adds or removes a registered front end from the
	// rotation; ErrNotFound if the ref isn't registered.
	SetFrontendInRotation(ctx context.Context, ref string, in bool) error
}

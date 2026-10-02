package store

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

var _ Projects = (*Memory)(nil)

func (m *Memory) Project(_ context.Context, slug string) (content.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.projects[slug]
	if !ok {
		return content.Project{}, ErrNotFound
	}
	return normProject(p), nil
}

func (m *Memory) Projects(_ context.Context) ([]content.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]content.Project, 0, len(m.projects))
	for _, p := range m.projects {
		out = append(out, normProject(p))
	}
	slices.SortFunc(out, func(a, b content.Project) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Slug, b.Slug))
	})
	return out, nil
}

func (m *Memory) SaveProject(_ context.Context, p content.Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.projects == nil {
		m.projects = map[string]content.Project{}
	}
	p = normProject(p)
	p.UpdatedAt = time.Now()
	m.projects[p.Slug] = p
	return nil
}

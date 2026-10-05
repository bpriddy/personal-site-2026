package store

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

var _ ExperienceStore = (*Memory)(nil)

func (m *Memory) ExperienceItem(_ context.Context, slug string) (content.Experience, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.experience[slug]
	if !ok {
		return content.Experience{}, ErrNotFound
	}
	return e, nil
}

func (m *Memory) Experience(_ context.Context) ([]content.Experience, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]content.Experience, 0, len(m.experience))
	for _, e := range m.experience {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b content.Experience) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Slug, b.Slug))
	})
	return out, nil
}

func (m *Memory) SaveExperience(_ context.Context, e content.Experience) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.experience == nil {
		m.experience = map[string]content.Experience{}
	}
	e.UpdatedAt = time.Now()
	m.experience[e.Slug] = e
	return nil
}

func (m *Memory) DeleteExperience(_ context.Context, slug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.experience, slug)
	return nil
}

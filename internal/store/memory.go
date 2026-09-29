package store

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

// Memory is an in-process Store seeded with starter content. Everything is
// lost on restart — it exists so the site runs before the database is chosen.
type Memory struct {
	mu          sync.RWMutex
	pages       map[string]content.Page
	experiments map[string]content.Experiment
}

func NewMemory() *Memory {
	now := time.Now()
	return &Memory{
		pages: map[string]content.Page{
			"": {Slug: "", Title: "Ben Priddy", Body: "Home page copy goes here.", Published: true, UpdatedAt: now},
		},
		experiments: map[string]content.Experiment{
			"particle-stream": {
				Slug:      "particle-stream",
				Title:     "Particle Stream",
				Summary:   "Words as rocks in a stream: 500k WebGPU particles part around a cycling phrase.",
				Published: true,
				UpdatedAt: now,
			},
		},
	}
}

func (m *Memory) Page(_ context.Context, slug string) (content.Page, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.pages[slug]
	if !ok {
		return content.Page{}, ErrNotFound
	}
	return p, nil
}

func (m *Memory) Pages(_ context.Context) ([]content.Page, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]content.Page, 0, len(m.pages))
	for _, p := range m.pages {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b content.Page) int { return strings.Compare(a.Slug, b.Slug) })
	return out, nil
}

func (m *Memory) SavePage(_ context.Context, p content.Page) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.UpdatedAt = time.Now()
	m.pages[p.Slug] = p
	return nil
}

func (m *Memory) Experiment(_ context.Context, slug string) (content.Experiment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.experiments[slug]
	if !ok {
		return content.Experiment{}, ErrNotFound
	}
	return e, nil
}

func (m *Memory) Experiments(_ context.Context) ([]content.Experiment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]content.Experiment, 0, len(m.experiments))
	for _, e := range m.experiments {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b content.Experiment) int {
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.Slug, b.Slug)
	})
	return out, nil
}

func (m *Memory) SaveExperiment(_ context.Context, e content.Experiment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e.UpdatedAt = time.Now()
	m.experiments[e.Slug] = e
	return nil
}

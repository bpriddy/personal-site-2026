package store

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

// Memory is an in-process Store seeded with the same starter content as the
// initial migration. Everything is lost on restart.
type Memory struct {
	mu          sync.RWMutex
	pages       map[string]content.Page
	experiments map[string]content.Experiment
	frontends   map[string]content.Frontend
	builder     memBuilder                    // builder_memory.go
	visitors    memVisitors                   // visitor_memory.go
	projects    map[string]content.Project    // projects_memory.go
	experience  map[string]content.Experience // experience_memory.go
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
		frontends: map[string]content.Frontend{
			"builtin/site":            {Ref: "builtin/site", Title: "Site", InRotation: true, UpdatedAt: now},
			"builtin/particle-stream": {Ref: "builtin/particle-stream", Title: "Particle Stream", InRotation: true, UpdatedAt: now},
			"builtin/stream":          {Ref: "builtin/stream", Title: "Particle Stream (site)", UpdatedAt: now},
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
	e.Media = mediaList(e.Media)
	return e, nil
}

func (m *Memory) Experiments(_ context.Context) ([]content.Experiment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]content.Experiment, 0, len(m.experiments))
	for _, e := range m.experiments {
		e.Media = mediaList(e.Media)
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
	e.Media = mediaList(e.Media)
	m.experiments[e.Slug] = e
	return nil
}

func (m *Memory) Frontends(_ context.Context) ([]content.Frontend, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]content.Frontend, 0, len(m.frontends))
	for _, f := range m.frontends {
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b content.Frontend) int { return strings.Compare(a.Ref, b.Ref) })
	return out, nil
}

func (m *Memory) SetFrontendInRotation(_ context.Context, ref string, in bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.frontends[ref]
	if !ok {
		return ErrNotFound
	}
	f.InRotation = in
	f.UpdatedAt = time.Now()
	m.frontends[ref] = f
	return nil
}

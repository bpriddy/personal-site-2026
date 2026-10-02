package store

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"
)

// ObserverMemory is an in-process ObserverStore for tests and database-less
// local dev. Everything is lost on restart.
type ObserverMemory struct {
	mu        sync.RWMutex
	nextID    int64
	byID      map[int64]*Detection
	bySig     map[string]int64
	generated map[[3]string]GeneratedField

	rebuilds    []Rebuild // oldest first
	nextRebuild int64
	// RebuildNow is the clock for rebuild claims (nil: time.Now); for tests.
	RebuildNow func() time.Time
}

func NewObserverMemory() *ObserverMemory {
	return &ObserverMemory{
		byID:      map[int64]*Detection{},
		bySig:     map[string]int64{},
		generated: map[[3]string]GeneratedField{},
	}
}

// copyDetection returns a deep copy, so callers can't mutate stored rows.
func copyDetection(d *Detection) Detection {
	out := *d
	out.Sample = slices.Clone(d.Sample)
	if d.SeenAt != nil {
		t := *d.SeenAt
		out.SeenAt = &t
	}
	return out
}

func (m *ObserverMemory) RecordDetection(_ context.Context, d Detection) (Detection, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if len(d.Sample) == 0 {
		d.Sample = json.RawMessage("{}")
	}
	if id, ok := m.bySig[d.Signature]; ok {
		cur := m.byID[id]
		cur.Count++
		cur.LastSeen = now
		cur.Sample = slices.Clone(d.Sample)
		cur.Serve, cur.Route, cur.Expect, cur.Got = d.Serve, d.Route, d.Expect, d.Got
		return copyDetection(cur), false, nil
	}
	m.nextID++
	d.ID = m.nextID
	d.Count = 1
	d.FirstSeen, d.LastSeen = now, now
	if d.Status == "" {
		d.Status = StatusNew
	}
	d.SeenAt = nil
	stored := copyDetection(&d)
	m.byID[d.ID] = &stored
	m.bySig[d.Signature] = d.ID
	return copyDetection(&stored), true, nil
}

func (m *ObserverMemory) Detection(_ context.Context, id int64) (Detection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.byID[id]
	if !ok {
		return Detection{}, ErrNotFound
	}
	return copyDetection(d), nil
}

func (m *ObserverMemory) DetectionBySignature(ctx context.Context, sig string) (Detection, error) {
	m.mu.RLock()
	id, ok := m.bySig[sig]
	m.mu.RUnlock()
	if !ok {
		return Detection{}, ErrNotFound
	}
	return m.Detection(ctx, id)
}

func (m *ObserverMemory) Detections(_ context.Context, limit int, statuses ...string) ([]Detection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 200
	}
	var out []Detection
	for _, d := range m.byID {
		if len(statuses) == 0 || slices.Contains(statuses, d.Status) {
			out = append(out, copyDetection(d))
		}
	}
	slices.SortFunc(out, func(a, b Detection) int {
		if c := b.LastSeen.Compare(a.LastSeen); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *ObserverMemory) SetDetection(_ context.Context, id int64, status string, a Action) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.byID[id]
	if !ok {
		return ErrNotFound
	}
	d.Status, d.Action = status, a
	return nil
}

func (m *ObserverMemory) MarkDetectionsSeen(_ context.Context, ids ...int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, id := range ids {
		if d, ok := m.byID[id]; ok && d.SeenAt == nil {
			t := now
			d.SeenAt = &t
		}
	}
	return nil
}

func (m *ObserverMemory) UnseenDetections(context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, d := range m.byID {
		if d.SeenAt == nil {
			n++
		}
	}
	return n, nil
}

func (m *ObserverMemory) DetectionCounts(context.Context) (map[string]int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]int{}
	for _, d := range m.byID {
		out[d.Status]++
	}
	return out, nil
}

func (m *ObserverMemory) GeneratedField(_ context.Context, collection, item, field string) (GeneratedField, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	g, ok := m.generated[[3]string{collection, item, field}]
	if !ok {
		return GeneratedField{}, ErrNotFound
	}
	return g, nil
}

func (m *ObserverMemory) PutGeneratedField(_ context.Context, g GeneratedField) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := [3]string{g.Collection, g.Item, g.Field}
	now := time.Now()
	g.CreatedAt, g.UpdatedAt = now, now
	if old, ok := m.generated[k]; ok {
		g.CreatedAt = old.CreatedAt
	}
	if g.Status == "" {
		g.Status = GenActive
	}
	if g.Expect == "" {
		g.Expect = "text"
	}
	m.generated[k] = g
	return nil
}

func (m *ObserverMemory) SetGeneratedStatus(_ context.Context, collection, item, field, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := [3]string{collection, item, field}
	g, ok := m.generated[k]
	if !ok {
		return ErrNotFound
	}
	g.Status = status
	g.UpdatedAt = time.Now()
	m.generated[k] = g
	return nil
}

func (m *ObserverMemory) GeneratedFields(_ context.Context, statuses ...string) ([]GeneratedField, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []GeneratedField
	for _, g := range m.generated {
		if len(statuses) == 0 || slices.Contains(statuses, g.Status) {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(a, b GeneratedField) int {
		return cmp.Or(cmp.Compare(a.Collection, b.Collection), cmp.Compare(a.Item, b.Item), cmp.Compare(a.Field, b.Field))
	})
	return out, nil
}

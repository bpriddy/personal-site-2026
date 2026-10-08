package store

import (
	"bytes"
	"context"
	"slices"
	"time"
)

var _ Notifies = (*Memory)(nil)

// memNotify is a build_notifications row.
type memNotify struct {
	RunNotify
	taken   bool
	outcome string
}

func (m *Memory) SetRunNotify(_ context.Context, n RunNotify) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n.RunID < 1 || int(n.RunID) > len(m.builder.runs) {
		return ErrNotFound
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	n.AddressHash = slices.Clone(n.AddressHash)
	if m.notifies == nil {
		m.notifies = map[int64]*memNotify{}
	}
	m.notifies[n.RunID] = &memNotify{RunNotify: n}
	return nil
}

func (m *Memory) RunNotify(_ context.Context, runID int64) (RunNotify, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.notifies[runID]
	if !ok || r.taken {
		return RunNotify{}, ErrNotFound
	}
	return r.RunNotify, nil
}

func (m *Memory) ClearRunNotify(_ context.Context, runID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.notifies[runID]; ok && !r.taken {
		delete(m.notifies, runID)
	}
	return nil
}

func (m *Memory) TakeRunNotify(_ context.Context, runID int64, outcome string) (RunNotify, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.notifies[runID]
	if !ok || r.taken {
		return RunNotify{}, false, nil
	}
	out := r.RunNotify
	r.taken, r.outcome = true, outcome
	r.Email, r.Link = "", ""
	return out, true, nil
}

func (m *Memory) NotifyCount(_ context.Context, addressHash []byte, since time.Time) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, r := range m.notifies {
		if bytes.Equal(r.AddressHash, addressHash) && !r.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

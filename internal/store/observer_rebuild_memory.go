package store

import (
	"context"
	"slices"
	"time"
)

// Rebuild claims for ObserverMemory (see ObserverStore.ClaimRebuild). Its
// clock is time.Now unless RebuildNow is set (tests).

func (m *ObserverMemory) rebuildNow() time.Time {
	if m.RebuildNow != nil {
		return m.RebuildNow()
	}
	return time.Now()
}

func (m *ObserverMemory) ClaimRebuild(_ context.Context, r Rebuild, perDay int) (Rebuild, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.rebuildNow()
	prev := -1
	for i, b := range m.rebuilds {
		if b.Frontend == r.Frontend && b.Fingerprint == r.Fingerprint {
			if b.Status != RunRunning || now.Sub(b.StartedAt) <= RebuildStale {
				return Rebuild{}, ErrRebuildDone
			}
			prev = i
		}
	}
	n := 0
	for _, b := range m.rebuilds {
		if b.Status == RunRunning && now.Sub(b.StartedAt) <= RebuildStale {
			return Rebuild{}, ErrRebuildBusy
		}
		if now.Sub(b.StartedAt) < 24*time.Hour {
			n++
		}
	}
	if n >= perDay {
		return Rebuild{}, ErrRebuildQuota
	}
	r.Status, r.StartedAt, r.FinishedAt, r.Revision, r.Error = RunRunning, now, time.Time{}, "", ""
	if prev >= 0 {
		r.ID = m.rebuilds[prev].ID
		m.rebuilds[prev] = r
		return r, nil
	}
	m.nextRebuild++
	r.ID = m.nextRebuild
	m.rebuilds = append(m.rebuilds, r)
	return r, nil
}

func (m *ObserverMemory) FinishRebuild(_ context.Context, id int64, revision, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.rebuilds {
		if m.rebuilds[i].ID == id {
			b := &m.rebuilds[i]
			b.Status, b.Revision, b.Error, b.FinishedAt = RunDone, revision, errMsg, m.rebuildNow()
			if errMsg != "" {
				b.Status = RunFailed
			}
			return nil
		}
	}
	return ErrNotFound
}

func (m *ObserverMemory) CancelRebuild(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rebuilds = slices.DeleteFunc(m.rebuilds, func(b Rebuild) bool { return b.ID == id })
	return nil
}

func (m *ObserverMemory) Rebuilds(_ context.Context, limit int) ([]Rebuild, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	out := slices.Clone(m.rebuilds)
	slices.Reverse(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

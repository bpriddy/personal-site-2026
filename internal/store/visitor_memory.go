package store

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

// memVisitors is Memory's public-builder state (migration 0004), guarded by
// Memory.mu.
type memVisitors struct {
	owner   map[string][]byte    // front-end ID → owner session hash
	runMeta map[int64]memRunMeta // visitor run ID → who started it
	subs    []Submission         // ID = index + 1
}

type memRunMeta struct{ session, ip []byte }

var _ Visitors = (*Memory)(nil)

func (v *memVisitors) init() {
	if v.owner == nil {
		v.owner = map[string][]byte{}
		v.runMeta = map[int64]memRunMeta{}
	}
}

func (m *Memory) CreateVisitorFrontend(_ context.Context, id, title string, session []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.builder.init()
	m.visitors.init()
	if _, ok := m.frontends[id]; ok {
		return ErrExists
	}
	m.frontends[id] = content.Frontend{Ref: id, Title: title, UpdatedAt: time.Now()}
	m.builder.prompted[id] = true
	m.visitors.owner[id] = slices.Clone(session)
	return nil
}

func (m *Memory) OwnedFrontend(_ context.Context, id string, session []byte) (FrontendInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.frontends[id]
	owner := m.visitors.owner[id]
	if !ok || len(session) == 0 || owner == nil || !bytes.Equal(owner, session) {
		return FrontendInfo{}, ErrNotFound
	}
	return m.info(f), nil
}

func (m *Memory) SessionFrontends(_ context.Context, session []byte) ([]FrontendInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []FrontendInfo{}
	if len(session) == 0 {
		return out, nil
	}
	for id, owner := range m.visitors.owner {
		if bytes.Equal(owner, session) {
			out = append(out, m.info(m.frontends[id]))
		}
	}
	slices.SortFunc(out, func(a, b FrontendInfo) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (m *Memory) VisitorFrontendIDs(_ context.Context) (map[string]bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]bool{}
	for id := range m.visitors.owner {
		out[id] = true
	}
	return out, nil
}

// visitorCounts counts visitor runs; the caller holds m.mu.
func (m *Memory) visitorCounts(session, ip []byte, q RunQuota) RunCounts {
	var c RunCounts
	for _, r := range m.builder.runs {
		meta, ok := m.visitors.runMeta[r.ID]
		if !ok {
			continue // Ben's runs are never counted
		}
		mine := len(session) > 0 && bytes.Equal(meta.session, session)
		if mine && r.Status == RunRunning && !r.StartedAt.Before(q.RunningSince) {
			c.Running++
		}
		if mine && !r.StartedAt.Before(q.HourStart) {
			c.SessionHour++
		}
		if !r.StartedAt.Before(q.DayStart) {
			c.GlobalDay++
			if len(ip) > 0 && bytes.Equal(meta.ip, ip) {
				c.IPDay++
			}
		}
	}
	return c
}

func (m *Memory) VisitorRunCounts(_ context.Context, session, ipHash []byte, q RunQuota) (RunCounts, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.visitorCounts(session, ipHash, q), nil
}

func (m *Memory) StartVisitorRun(_ context.Context, run Run, session, ipHash []byte, q RunQuota) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.visitors.init()
	if _, ok := m.frontends[run.FrontendID]; !ok {
		return 0, ErrNotFound
	}
	if err := q.Check(m.visitorCounts(session, ipHash, q)); err != nil {
		return 0, err
	}
	run.ID = int64(len(m.builder.runs) + 1)
	run.Status = RunRunning
	run.StartedAt = time.Now()
	run.RevisionID, run.Error, run.FinishedAt = "", "", time.Time{}
	m.builder.runs = append(m.builder.runs, run)
	m.visitors.runMeta[run.ID] = memRunMeta{session: slices.Clone(session), ip: slices.Clone(ipHash)}
	return run.ID, nil
}

// withDisplay fills a submission's display fields; the caller holds m.mu.
func (m *Memory) withDisplay(s Submission) Submission {
	s.Title = m.frontends[s.FrontendID].Title
	s.RevisionNumber = m.builder.revs[s.RevisionID].Number
	return s
}

func (m *Memory) Submit(_ context.Context, frontendID, revisionID string) (Submission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.builder.revs[revisionID]
	if _, fok := m.frontends[frontendID]; !fok || !ok || r.FrontendID != frontendID {
		return Submission{}, ErrNotFound
	}
	latest := -1
	for i, s := range m.visitors.subs {
		if s.FrontendID == frontendID {
			latest = i
		}
	}
	if latest >= 0 {
		s := &m.visitors.subs[latest]
		if s.RevisionID == revisionID {
			return m.withDisplay(*s), nil
		}
		if s.Status == SubmissionPending {
			s.RevisionID, s.SubmittedAt = revisionID, time.Now()
			return m.withDisplay(*s), nil
		}
	}
	s := Submission{ID: int64(len(m.visitors.subs) + 1), FrontendID: frontendID, RevisionID: revisionID,
		Status: SubmissionPending, SubmittedAt: time.Now()}
	m.visitors.subs = append(m.visitors.subs, s)
	return m.withDisplay(s), nil
}

func (m *Memory) LatestSubmission(_ context.Context, frontendID string) (Submission, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := len(m.visitors.subs) - 1; i >= 0; i-- {
		if s := m.visitors.subs[i]; s.FrontendID == frontendID {
			return m.withDisplay(s), nil
		}
	}
	return Submission{}, ErrNotFound
}

func (m *Memory) Submissions(_ context.Context, limit int) ([]Submission, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var pending, reviewed []Submission
	for _, s := range m.visitors.subs {
		if s.Status == SubmissionPending {
			pending = append(pending, m.withDisplay(s))
		} else {
			reviewed = append(reviewed, m.withDisplay(s))
		}
	}
	slices.SortStableFunc(pending, func(a, b Submission) int {
		if c := a.SubmittedAt.Compare(b.SubmittedAt); c != 0 {
			return c
		}
		return int(a.ID - b.ID)
	})
	slices.SortStableFunc(reviewed, func(a, b Submission) int {
		if c := b.ReviewedAt.Compare(a.ReviewedAt); c != 0 {
			return c
		}
		return int(b.ID - a.ID)
	})
	if len(reviewed) > limit {
		reviewed = reviewed[:max(limit, 0)]
	}
	return append(append([]Submission{}, pending...), reviewed...), nil
}

func (m *Memory) PendingSubmissions(_ context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, s := range m.visitors.subs {
		if s.Status == SubmissionPending {
			n++
		}
	}
	return n, nil
}

func (m *Memory) ReviewSubmission(_ context.Context, id int64, approve bool) (Submission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.visitors.subs) {
		return Submission{}, ErrNotFound
	}
	s := &m.visitors.subs[id-1]
	if s.Status != SubmissionPending {
		return Submission{}, ErrNotPending
	}
	now := time.Now()
	if approve {
		f, ok := m.frontends[s.FrontendID]
		if r, rok := m.builder.revs[s.RevisionID]; !ok || !rok || r.FrontendID != s.FrontendID {
			return Submission{}, ErrNotFound
		}
		m.builder.init()
		// approving never moves a front end back to an older version
		if cur, ok := m.builder.revs[m.builder.active[s.FrontendID]]; !ok || cur.Number < m.builder.revs[s.RevisionID].Number {
			m.builder.active[s.FrontendID] = s.RevisionID
		}
		f.InRotation, f.UpdatedAt = true, now
		m.frontends[s.FrontendID] = f
		s.Status = SubmissionApproved
	} else {
		s.Status = SubmissionRejected
	}
	s.ReviewedAt = now
	return m.withDisplay(*s), nil
}

package store

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

// memBuilder is Memory's builder state, guarded by Memory.mu. Front ends
// themselves live in Memory.frontends (so Frontends and the rotation see
// prompted ones too); this holds what migration 0002 adds.
type memBuilder struct {
	prompted map[string]bool   // front-end ID → kind is prompted
	active   map[string]string // front-end ID → active revision ID
	credit   map[string]string // front-end ID → credit
	asked    map[string]string // front-end ID → credit requested
	revs     map[string]Revision
	runs     []Run
}

var _ Builder = (*Memory)(nil)

func (b *memBuilder) init() {
	if b.prompted == nil {
		b.prompted = map[string]bool{}
		b.active = map[string]string{}
		b.credit = map[string]string{}
		b.asked = map[string]string{}
		b.revs = map[string]Revision{}
	}
}

func (m *Memory) info(f content.Frontend) FrontendInfo {
	kind := KindBuiltin
	if m.builder.prompted[f.Ref] {
		kind = KindPrompted
	}
	return FrontendInfo{ID: f.Ref, Title: f.Title, Kind: kind, InRotation: f.InRotation,
		ActiveRevision: m.builder.active[f.Ref], Credit: m.builder.credit[f.Ref], CreditRequested: m.builder.asked[f.Ref], UpdatedAt: f.UpdatedAt}
}

func (m *Memory) BuilderFrontends(_ context.Context) ([]FrontendInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]FrontendInfo, 0, len(m.frontends))
	for _, f := range m.frontends {
		out = append(out, m.info(f))
	}
	slices.SortFunc(out, func(a, b FrontendInfo) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (m *Memory) BuilderFrontend(_ context.Context, id string) (FrontendInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.frontends[id]
	if !ok {
		return FrontendInfo{}, ErrNotFound
	}
	return m.info(f), nil
}

func (m *Memory) CreatePromptedFrontend(_ context.Context, id, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.builder.init()
	if _, ok := m.frontends[id]; ok {
		return ErrExists
	}
	m.frontends[id] = content.Frontend{Ref: id, Title: title, UpdatedAt: time.Now()}
	m.builder.prompted[id] = true
	return nil
}

func (m *Memory) AddRevision(_ context.Context, rev Revision) (Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.builder.init()
	if _, ok := m.frontends[rev.FrontendID]; !ok {
		return Revision{}, ErrNotFound
	}
	if _, ok := m.builder.revs[rev.ID]; ok {
		return Revision{}, ErrExists
	}
	if rev.ParentID != "" {
		if p, ok := m.builder.revs[rev.ParentID]; !ok || p.FrontendID != rev.FrontendID {
			return Revision{}, ErrNotFound
		}
	}
	rev.Number = 1
	for _, r := range m.builder.revs {
		if r.FrontendID == rev.FrontendID && r.Number >= rev.Number {
			rev.Number = r.Number + 1
		}
	}
	rev.CreatedAt = time.Now()
	if len(rev.Conversation) == 0 {
		rev.Conversation = json.RawMessage("[]")
	}
	rev.Conversation = slices.Clone(rev.Conversation)
	rev.Files = slices.Clone(rev.Files)
	if rev.Files == nil {
		rev.Files = []FileInfo{}
	}
	m.builder.revs[rev.ID] = rev
	return rev, nil
}

func (m *Memory) Revision(_ context.Context, id string) (Revision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.builder.revs[id]
	if !ok {
		return Revision{}, ErrNotFound
	}
	return r, nil
}

func (m *Memory) Revisions(_ context.Context, frontendID string) ([]Revision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []Revision{}
	for _, r := range m.builder.revs {
		if r.FrontendID == frontendID {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b Revision) int { return b.Number - a.Number })
	return out, nil
}

func (m *Memory) SetActiveRevision(_ context.Context, frontendID, revID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.builder.init()
	f, ok := m.frontends[frontendID]
	r, rok := m.builder.revs[revID]
	if !ok || !rok || r.FrontendID != frontendID {
		return ErrNotFound
	}
	m.builder.active[frontendID] = revID
	f.UpdatedAt = time.Now()
	m.frontends[frontendID] = f
	return nil
}

func (m *Memory) StartRun(_ context.Context, run Run) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.frontends[run.FrontendID]; !ok {
		return 0, ErrNotFound
	}
	run.ID = int64(len(m.builder.runs) + 1)
	run.Status = RunRunning
	run.StartedAt = time.Now()
	run.RevisionID, run.Error, run.FinishedAt = "", "", time.Time{}
	m.builder.runs = append(m.builder.runs, run)
	return run.ID, nil
}

func (m *Memory) FinishRun(_ context.Context, id int64, revisionID, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.builder.runs) {
		return ErrNotFound
	}
	r := &m.builder.runs[id-1]
	r.Status, r.RevisionID, r.Error, r.FinishedAt = RunDone, revisionID, errMsg, time.Now()
	if errMsg != "" {
		r.Status = RunFailed
	}
	return nil
}

func (m *Memory) Runs(_ context.Context, frontendID string, limit int) ([]Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []Run{}
	for i := len(m.builder.runs) - 1; i >= 0 && len(out) < limit; i-- {
		if m.builder.runs[i].FrontendID == frontendID {
			out = append(out, m.builder.runs[i])
		}
	}
	return out, nil
}

func (m *Memory) SetCredit(_ context.Context, id, credit string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.builder.init()
	if _, ok := m.frontends[id]; !ok {
		return ErrNotFound
	}
	m.builder.credit[id] = credit
	return nil
}

func (m *Memory) SetCreditRequested(_ context.Context, id, credit string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.builder.init()
	if _, ok := m.frontends[id]; !ok {
		return ErrNotFound
	}
	m.builder.asked[id] = credit
	return nil
}

func (m *Memory) DeleteRevision(_ context.Context, frontendID, revID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.builder.init()
	r, ok := m.builder.revs[revID]
	if !ok || r.FrontendID != frontendID {
		return ErrNotFound
	}
	if m.builder.active[frontendID] == revID {
		return ErrActiveRevision
	}
	for _, s := range m.visitors.subs {
		if s.RevisionID == revID && s.Status == SubmissionPending {
			return ErrPendingRevision
		}
	}
	for id, c := range m.builder.revs {
		if c.ParentID == revID {
			c.ParentID = r.ParentID
			m.builder.revs[id] = c
		}
	}
	for i := range m.builder.runs {
		if m.builder.runs[i].ParentID == revID {
			m.builder.runs[i].ParentID = ""
		}
		if m.builder.runs[i].RevisionID == revID {
			m.builder.runs[i].RevisionID = ""
		}
	}
	// submissions keep their index-based IDs: a deleted one becomes a tombstone
	for i := range m.visitors.subs {
		if m.visitors.subs[i].RevisionID == revID {
			m.visitors.subs[i] = Submission{ID: m.visitors.subs[i].ID, Status: submissionDeleted}
		}
	}
	delete(m.builder.revs, revID)
	return nil
}

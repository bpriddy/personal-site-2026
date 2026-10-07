package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/llm"
	"github.com/bpriddy/personal-site-2026/internal/observer"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// The observer's creative rebuilds (docs/observer.md, "Content drift") run
// the same builder agent as Ben's chat, with the same system prompt and
// quality brief: the server implements observer.Rebuilder.

var _ observer.Rebuilder = (*Server)(nil)

// Rebuild runs one builder turn on req.Parent with the observer's prompt and
// saves the result as a new revision by author "observer" (not activated:
// the observer verifies it first). It refuses with observer.ErrBusy while the
// front end has a chat run in progress.
func (s *Server) Rebuild(ctx context.Context, req observer.RebuildRequest) (store.Revision, error) {
	b := s.builderStore()
	if b == nil || s.builder.agent == nil {
		return store.Revision{}, errors.New("the builder is not available")
	}
	f, err := b.BuilderFrontend(ctx, req.FrontendID)
	if err != nil {
		return store.Revision{}, err
	}
	if f.Kind != store.KindPrompted {
		return store.Revision{}, fmt.Errorf("%s is not a prompted front end", f.ID)
	}
	parent, err := b.Revision(ctx, req.Parent)
	if err != nil {
		return store.Revision{}, err
	}
	if parent.FrontendID != f.ID {
		return store.Revision{}, fmt.Errorf("revision %s is not a revision of %s", parent.ID, f.ID)
	}
	files, err := s.builder.files.Read(ctx, parent.ID)
	if err != nil {
		return store.Revision{}, fmt.Errorf("read parent files: %w", err)
	}
	history := builder.ParseConversation(parent.Conversation)

	s.builder.mu.Lock()
	if s.builder.running[f.ID] {
		s.builder.mu.Unlock()
		return store.Revision{}, observer.ErrBusy
	}
	s.builder.running[f.ID] = true
	s.builder.mu.Unlock()
	defer func() {
		s.builder.mu.Lock()
		delete(s.builder.running, f.ID)
		s.builder.mu.Unlock()
	}()

	// the budget gate (v1.12): the observer never spends past it
	if st, err := s.budgetStatus(ctx, true); err != nil {
		return store.Revision{}, err
	} else if st.Closed {
		return store.Revision{}, fmt.Errorf("the builder's budget gate is closed (%s limit)", st.Limit)
	}
	runID, err := b.StartRun(ctx, store.Run{FrontendID: f.ID, ParentID: parent.ID, Prompt: req.Prompt})
	if err != nil {
		return store.Revision{}, err
	}
	started := s.now()
	cost := s.newRunCost(ctx, runID)
	res, err := s.builder.agent.Run(ctx, builder.Request{FrontendID: f.ID, Title: f.Title, Parent: files,
		History: history, Prompt: req.Prompt, Content: s.siteContent(ctx), Observer: true, OnCost: cost.add}, func(builder.Event) {})
	if se := llm.AsSpendLimit(err); se != nil {
		s.noteSpendLimit(ctx, se)
	}
	var rev store.Revision
	if err == nil {
		turns := append(slices.Clone(history),
			builder.Turn{Role: "user", Text: req.Prompt, At: started, By: "observer"},
			builder.Turn{Role: "assistant", Text: res.Summary, At: s.now(), Actions: res.Actions})
		conv, _ := json.Marshal(turns)
		rev, err = s.saveRevision(ctx, store.Revision{FrontendID: f.ID, ParentID: parent.ID, Author: "observer",
			Summary: res.Summary, Conversation: conv}, res.Files)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if ferr := b.FinishRun(ctx, runID, rev.ID, msg); ferr != nil {
		s.log.Error("observer rebuild: finish run", "err", ferr)
	}
	return rev, err
}

// contentChanged tells the observer the content changed, so it checks the
// front ends in the rotation for content they don't show.
func (s *Server) contentChanged() {
	if o := s.observer.o; o != nil {
		o.ContentChanged()
	}
}

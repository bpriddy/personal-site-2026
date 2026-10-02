package server

import (
	"context"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/store"
)

// A run in progress when the server stops is recorded as failed, so it
// doesn't count as "running" (blocking the visitor's next build) until it
// ages out.
func TestInterruptRunsFinishesRunsInProgress(t *testing.T) {
	s := newTestServer(t)
	b := s.builderStore()
	if b == nil {
		t.Skip("no builder store")
	}
	ctx := context.Background()
	if err := b.CreatePromptedFrontend(ctx, "fe/x", "X"); err != nil {
		t.Fatal(err)
	}
	id, err := b.StartRun(ctx, store.Run{FrontendID: "fe/x", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	s.builder.mu.Lock()
	s.builder.runs = map[int64]bool{id: true}
	s.builder.mu.Unlock()

	s.InterruptRuns(ctx)

	runs, err := b.Runs(ctx, "fe/x", 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	if runs[0].Status != store.RunFailed || runs[0].Error == "" {
		t.Fatalf("run after interrupt: status %q, error %q; want failed with a reason", runs[0].Status, runs[0].Error)
	}
}

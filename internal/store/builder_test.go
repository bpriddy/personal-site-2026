package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// builderConformance runs the behavior every Builder must share. newStore
// returns a fresh store holding only the seed data.
func builderConformance(t *testing.T, newStore func(t *testing.T) Builder) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Run("seed builtins", func(t *testing.T) {
		st := newStore(t)
		fes, err := st.BuilderFrontends(ctx)
		must(t, err)
		if len(fes) != 2 || fes[0].ID != "builtin/particle-stream" || fes[1].ID != "builtin/site" {
			t.Fatalf("frontends = %+v", fes)
		}
		for _, f := range fes {
			if f.Kind != KindBuiltin || f.ActiveRevision != "" || !f.InRotation {
				t.Fatalf("seeded builtin = %+v", f)
			}
		}
		if _, err := st.BuilderFrontend(ctx, "fe/nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing frontend err = %v", err)
		}
	})

	t.Run("prompted lifecycle", func(t *testing.T) {
		st := newStore(t)
		must(t, st.CreatePromptedFrontend(ctx, "fe/dark", "Dark"))
		if err := st.CreatePromptedFrontend(ctx, "fe/dark", "Again"); !errors.Is(err, ErrExists) {
			t.Fatalf("duplicate create err = %v", err)
		}
		f, err := st.BuilderFrontend(ctx, "fe/dark")
		must(t, err)
		if f.Kind != KindPrompted || f.Title != "Dark" || f.InRotation || f.ActiveRevision != "" {
			t.Fatalf("new prompted = %+v", f)
		}
		// it is a registered front end for the CMS store too (rotation toggle)
		if s, ok := st.(Store); ok {
			fes, _ := s.Frontends(ctx)
			if len(fes) != 3 || fes[2].Ref != "fe/dark" {
				t.Fatalf("Store.Frontends = %+v", fes)
			}
			must(t, s.SetFrontendInRotation(ctx, "fe/dark", true))
			f, _ = st.BuilderFrontend(ctx, "fe/dark")
			if !f.InRotation {
				t.Fatal("rotation toggle didn't stick")
			}
		}

		revs, err := st.Revisions(ctx, "fe/dark")
		must(t, err)
		if len(revs) != 0 {
			t.Fatalf("revisions before any = %+v", revs)
		}
		conv := json.RawMessage(`[{"role":"user","text":"make it dark"}]`)
		r1, err := st.AddRevision(ctx, Revision{ID: "aaaaaaaa1", FrontendID: "fe/dark", Author: "ben", Summary: "first",
			Conversation: conv, Files: []FileInfo{{Path: "index.html", Size: 10, SHA256: "ab"}}})
		must(t, err)
		if r1.Number != 1 || r1.CreatedAt.IsZero() || r1.ParentID != "" {
			t.Fatalf("r1 = %+v", r1)
		}
		r2, err := st.AddRevision(ctx, Revision{ID: "bbbbbbbb2", FrontendID: "fe/dark", ParentID: r1.ID, Author: "observer"})
		must(t, err)
		if r2.Number != 2 || r2.ParentID != r1.ID || len(r2.Files) != 0 || string(r2.Conversation) != "[]" {
			t.Fatalf("r2 = %+v", r2)
		}
		if _, err := st.AddRevision(ctx, Revision{ID: "aaaaaaaa1", FrontendID: "fe/dark", Author: "ben"}); !errors.Is(err, ErrExists) {
			t.Fatalf("duplicate revision err = %v", err)
		}
		if _, err := st.AddRevision(ctx, Revision{ID: "cccccccc3", FrontendID: "fe/none", Author: "ben"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("revision of unknown front end err = %v", err)
		}
		if _, err := st.AddRevision(ctx, Revision{ID: "cccccccc3", FrontendID: "fe/dark", ParentID: "zzzzzzzz9", Author: "ben"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown parent err = %v", err)
		}

		got, err := st.Revision(ctx, r1.ID)
		must(t, err)
		var c []map[string]string
		if err := json.Unmarshal(got.Conversation, &c); err != nil || len(c) != 1 || c[0]["text"] != "make it dark" {
			t.Fatalf("conversation = %s, %v", got.Conversation, err)
		}
		if len(got.Files) != 1 || got.Files[0] != (FileInfo{Path: "index.html", Size: 10, SHA256: "ab"}) || got.Author != "ben" || got.Summary != "first" {
			t.Fatalf("r1 read back = %+v", got)
		}
		if _, err := st.Revision(ctx, "nopenope1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing revision err = %v", err)
		}
		revs, _ = st.Revisions(ctx, "fe/dark")
		if len(revs) != 2 || revs[0].ID != r2.ID || revs[1].ID != r1.ID {
			t.Fatalf("revisions order = %+v", revs)
		}

		// a second front end numbers its own revisions and can't parent across
		must(t, st.CreatePromptedFrontend(ctx, "fe/light", "Light"))
		if _, err := st.AddRevision(ctx, Revision{ID: "dddddddd4", FrontendID: "fe/light", ParentID: r1.ID, Author: "ben"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-front-end parent err = %v", err)
		}
		l1, err := st.AddRevision(ctx, Revision{ID: "dddddddd4", FrontendID: "fe/light", Author: "ben"})
		must(t, err)
		if l1.Number != 1 {
			t.Fatalf("l1 number = %d", l1.Number)
		}

		// activation, rollback, and refusing another front end's revision
		must(t, st.SetActiveRevision(ctx, "fe/dark", r2.ID))
		f, _ = st.BuilderFrontend(ctx, "fe/dark")
		if f.ActiveRevision != r2.ID {
			t.Fatalf("active = %q", f.ActiveRevision)
		}
		must(t, st.SetActiveRevision(ctx, "fe/dark", r1.ID))
		f, _ = st.BuilderFrontend(ctx, "fe/dark")
		if f.ActiveRevision != r1.ID {
			t.Fatalf("after rollback active = %q", f.ActiveRevision)
		}
		if err := st.SetActiveRevision(ctx, "fe/dark", l1.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign revision err = %v", err)
		}
		if err := st.SetActiveRevision(ctx, "fe/nope", r1.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown front end err = %v", err)
		}
	})

	t.Run("runs", func(t *testing.T) {
		st := newStore(t)
		must(t, st.CreatePromptedFrontend(ctx, "fe/r", "R"))
		if _, err := st.StartRun(ctx, Run{FrontendID: "fe/none", Prompt: "x"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("run for unknown front end err = %v", err)
		}
		id1, err := st.StartRun(ctx, Run{FrontendID: "fe/r", Prompt: "first"})
		must(t, err)
		id2, err := st.StartRun(ctx, Run{FrontendID: "fe/r", Prompt: "second"})
		must(t, err)
		rev, err := st.AddRevision(ctx, Revision{ID: "eeeeeeee5", FrontendID: "fe/r", Author: "ben"})
		must(t, err)
		must(t, st.FinishRun(ctx, id1, rev.ID, ""))
		must(t, st.FinishRun(ctx, id2, "", "model refused"))
		if err := st.FinishRun(ctx, 9999, "", ""); !errors.Is(err, ErrNotFound) {
			t.Fatalf("finish unknown run err = %v", err)
		}
		runs, err := st.Runs(ctx, "fe/r", 10)
		must(t, err)
		if len(runs) != 2 || runs[0].ID != id2 || runs[1].ID != id1 {
			t.Fatalf("runs = %+v", runs)
		}
		if runs[0].Status != RunFailed || runs[0].Error != "model refused" || runs[0].Prompt != "second" || runs[0].FinishedAt.IsZero() {
			t.Fatalf("failed run = %+v", runs[0])
		}
		if runs[1].Status != RunDone || runs[1].RevisionID != rev.ID || runs[1].StartedAt.IsZero() {
			t.Fatalf("done run = %+v", runs[1])
		}
		if runs, _ := st.Runs(ctx, "fe/r", 1); len(runs) != 1 {
			t.Fatalf("limit: %d runs", len(runs))
		}
	})
}

func TestMemoryBuilderConformance(t *testing.T) {
	builderConformance(t, func(*testing.T) Builder { return NewMemory() })
}

func TestPostgresBuilderConformance(t *testing.T) {
	builderConformance(t, func(t *testing.T) Builder { return openPG(t, freshDB(t)) })
}

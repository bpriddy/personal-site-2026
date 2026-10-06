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

	t.Run("delete revision", func(t *testing.T) {
		st := newStore(t)
		must(t, st.CreatePromptedFrontend(ctx, "fe/d", "D"))
		must(t, st.CreatePromptedFrontend(ctx, "fe/other", "O"))
		add := func(id, parent string) {
			t.Helper()
			_, err := st.AddRevision(ctx, Revision{ID: id, FrontendID: "fe/d", ParentID: parent, Author: "ben"})
			must(t, err)
		}
		add("dddddddd1", "")
		add("dddddddd2", "dddddddd1")
		add("dddddddd3", "dddddddd2")
		must(t, st.SetActiveRevision(ctx, "fe/d", "dddddddd3"))
		runID, err := st.StartRun(ctx, Run{FrontendID: "fe/d", ParentID: "dddddddd2"})
		must(t, err)

		if err := st.DeleteRevision(ctx, "fe/d", "dddddddd3"); !errors.Is(err, ErrActiveRevision) {
			t.Fatalf("delete active: %v", err)
		}
		if err := st.DeleteRevision(ctx, "fe/other", "dddddddd2"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("delete another front end's: %v", err)
		}
		if err := st.DeleteRevision(ctx, "fe/d", "nopenope1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("delete unknown: %v", err)
		}
		must(t, st.DeleteRevision(ctx, "fe/d", "dddddddd2"))
		if _, err := st.Revision(ctx, "dddddddd2"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted revision still there: %v", err)
		}
		if r3, _ := st.Revision(ctx, "dddddddd3"); r3.ParentID != "dddddddd1" {
			t.Fatalf("child not re-parented: %+v", r3)
		}
		if runs, _ := st.Runs(ctx, "fe/d", 5); len(runs) != 1 || runs[0].ID != runID || runs[0].ParentID != "" {
			t.Fatalf("run = %+v", runs)
		}
		if revs, _ := st.Revisions(ctx, "fe/d"); len(revs) != 2 {
			t.Fatalf("revisions = %+v", revs)
		}

		// a revision waiting for review stays; once reviewed it can go
		if v, ok := st.(Visitors); ok {
			sub, err := v.Submit(ctx, "fe/d", "dddddddd1")
			must(t, err)
			if err := st.DeleteRevision(ctx, "fe/d", "dddddddd1"); !errors.Is(err, ErrPendingRevision) {
				t.Fatalf("delete pending: %v", err)
			}
			_, err = v.ReviewSubmission(ctx, sub.ID, false)
			must(t, err)
			must(t, st.DeleteRevision(ctx, "fe/d", "dddddddd1"))
			if subs, _ := v.Submissions(ctx, 10); len(subs) != 0 {
				t.Fatalf("submissions after delete = %+v", subs)
			}
			if r3, _ := st.Revision(ctx, "dddddddd3"); r3.ParentID != "" {
				t.Fatalf("r3 parent = %q", r3.ParentID)
			}
		}
	})

	t.Run("delete front end", func(t *testing.T) {
		st := newStore(t)
		must(t, st.CreatePromptedFrontend(ctx, "fe/gone", "Gone"))
		_, err := st.AddRevision(ctx, Revision{ID: "ggggggg01", FrontendID: "fe/gone", Author: "ben"})
		must(t, err)
		_, err = st.AddRevision(ctx, Revision{ID: "ggggggg02", FrontendID: "fe/gone", ParentID: "ggggggg01", Author: "ben"})
		must(t, err)
		must(t, st.SetActiveRevision(ctx, "fe/gone", "ggggggg02"))
		_, err = st.StartRun(ctx, Run{FrontendID: "fe/gone", ParentID: "ggggggg01"})
		must(t, err)
		if s, ok := st.(Store); ok {
			must(t, s.SetFrontendInRotation(ctx, "fe/gone", true))
			if err := st.DeleteFrontend(ctx, "fe/gone"); !errors.Is(err, ErrInRotation) {
				t.Fatalf("delete in rotation: %v", err)
			}
			must(t, s.SetFrontendInRotation(ctx, "fe/gone", false))
		}
		if v, ok := st.(Visitors); ok {
			_, err := v.Submit(ctx, "fe/gone", "ggggggg02")
			must(t, err)
		}
		if err := st.DeleteFrontend(ctx, "builtin/site"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("delete a builtin: %v", err)
		}
		must(t, st.DeleteFrontend(ctx, "fe/gone"))
		if _, err := st.BuilderFrontend(ctx, "fe/gone"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("still there: %v", err)
		}
		if _, err := st.Revision(ctx, "ggggggg01"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("revision still there: %v", err)
		}
		if v, ok := st.(Visitors); ok {
			if subs, _ := v.Submissions(ctx, 10); len(subs) != 0 {
				t.Fatalf("submissions = %+v", subs)
			}
		}
		if err := st.DeleteFrontend(ctx, "fe/gone"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("delete twice: %v", err)
		}
		// the slug is free again
		must(t, st.CreatePromptedFrontend(ctx, "fe/gone", "Again"))
	})

	t.Run("credit", func(t *testing.T) {
		st := newStore(t)
		must(t, st.CreatePromptedFrontend(ctx, "fe/c", "C"))
		must(t, st.SetCreditRequested(ctx, "fe/c", "Jo Smith"))
		f, _ := st.BuilderFrontend(ctx, "fe/c")
		if f.Credit != "" || f.CreditRequested != "Jo Smith" {
			t.Fatalf("after request = %+v", f)
		}
		must(t, st.SetCredit(ctx, "fe/c", "Jo"))
		if fes, _ := st.BuilderFrontends(ctx); fes[len(fes)-1].Credit != "Jo" {
			t.Fatalf("list = %+v", fes)
		}
		if err := st.SetCredit(ctx, "fe/nope", "x"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("SetCredit on a missing front end: %v", err)
		}
		if err := st.SetCreditRequested(ctx, "fe/nope", "x"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("SetCreditRequested on a missing front end: %v", err)
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

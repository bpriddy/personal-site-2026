package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

type visitorStore interface {
	Builder
	Visitors
}

func sessionHash(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

// visitorConformance runs the behavior every Visitors store must share.
func visitorConformance(t *testing.T, newStore func(t *testing.T) visitorStore) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	a, b := sessionHash("a"), sessionHash("b")

	t.Run("ownership", func(t *testing.T) {
		st := newStore(t)
		must(t, st.CreateVisitorFrontend(ctx, "fe/mine", "Mine", a))
		if err := st.CreateVisitorFrontend(ctx, "fe/mine", "Again", b); !errors.Is(err, ErrExists) {
			t.Fatalf("duplicate err = %v", err)
		}
		// visitors share the namespace with Ben's and the built-ins
		if err := st.CreateVisitorFrontend(ctx, "builtin/site", "x", b); !errors.Is(err, ErrExists) {
			t.Fatalf("builtin collision err = %v", err)
		}
		must(t, st.CreatePromptedFrontend(ctx, "fe/bens", "Ben's"))
		if err := st.CreateVisitorFrontend(ctx, "fe/bens", "x", a); !errors.Is(err, ErrExists) {
			t.Fatalf("Ben's collision err = %v", err)
		}

		f, err := st.OwnedFrontend(ctx, "fe/mine", a)
		must(t, err)
		if f.ID != "fe/mine" || f.Kind != KindPrompted || f.Title != "Mine" || f.InRotation {
			t.Fatalf("owned = %+v", f)
		}
		for _, c := range []struct {
			id      string
			session []byte
		}{{"fe/mine", b}, {"fe/mine", nil}, {"fe/bens", a}, {"builtin/site", a}, {"fe/none", a}} {
			if _, err := st.OwnedFrontend(ctx, c.id, c.session); !errors.Is(err, ErrNotFound) {
				t.Errorf("OwnedFrontend(%s) by another session: %v", c.id, err)
			}
		}
		// it's an ordinary prompted front end to the builder
		if bf, err := st.BuilderFrontend(ctx, "fe/mine"); err != nil || bf.Kind != KindPrompted {
			t.Fatalf("builder view = %+v, %v", bf, err)
		}

		time.Sleep(5 * time.Millisecond)
		must(t, st.CreateVisitorFrontend(ctx, "fe/mine-2", "Second", a))
		fes, err := st.SessionFrontends(ctx, a)
		must(t, err)
		if len(fes) != 2 || fes[0].ID != "fe/mine-2" || fes[1].ID != "fe/mine" {
			t.Fatalf("session frontends = %+v", fes)
		}
		if fes, _ := st.SessionFrontends(ctx, b); len(fes) != 0 {
			t.Fatalf("other session sees %+v", fes)
		}
		if fes, _ := st.SessionFrontends(ctx, nil); len(fes) != 0 {
			t.Fatalf("no session sees %+v", fes)
		}
		ids, err := st.VisitorFrontendIDs(ctx)
		must(t, err)
		if len(ids) != 2 || !ids["fe/mine"] || !ids["fe/mine-2"] || ids["fe/bens"] {
			t.Fatalf("visitor ids = %v", ids)
		}
	})

	t.Run("run limits", func(t *testing.T) {
		st := newStore(t)
		must(t, st.CreateVisitorFrontend(ctx, "fe/a", "A", a))
		must(t, st.CreateVisitorFrontend(ctx, "fe/b", "B", b))
		must(t, st.CreatePromptedFrontend(ctx, "fe/ben", "Ben"))
		ipA, ipB := sessionHash("ip-a"), sessionHash("ip-b")
		now := time.Now()
		q := RunQuota{SessionHour: 2, IPDay: 3, GlobalDay: 4, Concurrent: 1,
			HourStart: now.Add(-time.Hour), DayStart: now.Add(-24 * time.Hour), RunningSince: now.Add(-time.Hour)}
		quota := func(err error) string {
			var qe *QuotaError
			if errors.As(err, &qe) {
				return qe.Limit
			}
			if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			return ""
		}

		// Ben's runs are never counted
		for range 5 {
			if _, err := st.StartRun(ctx, Run{FrontendID: "fe/ben", Prompt: "x"}); err != nil {
				t.Fatal(err)
			}
		}
		if c, _ := st.VisitorRunCounts(ctx, a, ipA, q); c != (RunCounts{}) {
			t.Fatalf("counts with only Ben's runs = %+v", c)
		}

		id1, err := st.StartVisitorRun(ctx, Run{FrontendID: "fe/a", Prompt: "one"}, a, ipA, q)
		must(t, err)
		// one at a time per session
		if _, err := st.StartVisitorRun(ctx, Run{FrontendID: "fe/a", Prompt: "two"}, a, ipA, q); quota(err) != LimitConcurrent {
			t.Fatalf("concurrent: %v", err)
		}
		// a stale running run (older than RunningSince) doesn't block
		stale := q
		stale.RunningSince = time.Now().Add(time.Hour)
		if c, _ := st.VisitorRunCounts(ctx, a, ipA, stale); c.Running != 0 {
			t.Fatalf("stale run counted as running: %+v", c)
		}
		must(t, st.FinishRun(ctx, id1, "", "boom"))
		_, err = st.StartVisitorRun(ctx, Run{FrontendID: "fe/a", Prompt: "two"}, a, ipA, q)
		must(t, err)
		runs, _ := st.Runs(ctx, "fe/a", 10)
		for _, r := range runs {
			must(t, st.FinishRun(ctx, r.ID, "", "x"))
		}
		// session a: 2 in the hour
		if _, err := st.StartVisitorRun(ctx, Run{FrontendID: "fe/a", Prompt: "three"}, a, ipB, q); quota(err) != LimitSessionHour {
			t.Fatalf("session hour: %v", err)
		}
		// the window moves on
		later := q
		later.HourStart = time.Now().Add(time.Minute)
		id3, err := st.StartVisitorRun(ctx, Run{FrontendID: "fe/a", Prompt: "three"}, a, ipA, later)
		must(t, err)
		must(t, st.FinishRun(ctx, id3, "", "x"))
		// ip a: 3 today, even from another session
		if _, err := st.StartVisitorRun(ctx, Run{FrontendID: "fe/b", Prompt: "x"}, b, ipA, later); quota(err) != LimitIPDay {
			t.Fatalf("ip day: %v", err)
		}
		c, err := st.VisitorRunCounts(ctx, b, ipA, q)
		must(t, err)
		if c != (RunCounts{Running: 0, SessionHour: 0, IPDay: 3, GlobalDay: 3}) {
			t.Fatalf("counts = %+v", c)
		}
		// global: 4 visitor runs today across sessions and IPs
		id4, err := st.StartVisitorRun(ctx, Run{FrontendID: "fe/b", Prompt: "x"}, b, ipB, later)
		must(t, err)
		must(t, st.FinishRun(ctx, id4, "", "x"))
		if _, err := st.StartVisitorRun(ctx, Run{FrontendID: "fe/b", Prompt: "x"}, sessionHash("c"), sessionHash("ip-c"), later); quota(err) != LimitGlobalDay {
			t.Fatalf("global day: %v", err)
		}
		// unlimited quotas allow it; unknown front end is not found
		if _, err := st.StartVisitorRun(ctx, Run{FrontendID: "fe/none", Prompt: "x"}, a, ipA, RunQuota{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown front end: %v", err)
		}
		runs, _ = st.Runs(ctx, "fe/b", 10)
		if len(runs) != 1 || runs[0].Prompt != "x" {
			t.Fatalf("visitor runs listed = %+v", runs)
		}
	})

	t.Run("approving an old submission never rolls back", func(t *testing.T) {
		st := newStore(t)
		must(t, st.CreateVisitorFrontend(ctx, "fe/u", "Old sub", a))
		u1, err := st.AddRevision(ctx, Revision{ID: "uuuuuuuu1", FrontendID: "fe/u", Author: "visitor"})
		must(t, err)
		old, err := st.Submit(ctx, "fe/u", u1.ID)
		must(t, err)
		u2, err := st.AddRevision(ctx, Revision{ID: "uuuuuuuu2", FrontendID: "fe/u", ParentID: u1.ID, Author: "observer"})
		must(t, err)
		must(t, st.SetActiveRevision(ctx, "fe/u", u2.ID))
		if _, err := st.ReviewSubmission(ctx, old.ID, true); err != nil {
			t.Fatal(err)
		}
		if f, _ := st.BuilderFrontend(ctx, "fe/u"); f.ActiveRevision != u2.ID || !f.InRotation {
			t.Fatalf("approving v1 after v2 went live moved the front end back: %+v", f)
		}
	})

	t.Run("submissions", func(t *testing.T) {
		st := newStore(t)
		must(t, st.CreateVisitorFrontend(ctx, "fe/s", "Sub", a))
		must(t, st.CreateVisitorFrontend(ctx, "fe/t", "Other", b))
		r1, err := st.AddRevision(ctx, Revision{ID: "ssssssss1", FrontendID: "fe/s", Author: "visitor"})
		must(t, err)
		r2, err := st.AddRevision(ctx, Revision{ID: "ssssssss2", FrontendID: "fe/s", ParentID: r1.ID, Author: "visitor"})
		must(t, err)
		t1, err := st.AddRevision(ctx, Revision{ID: "tttttttt1", FrontendID: "fe/t", Author: "visitor"})
		must(t, err)

		if _, err := st.LatestSubmission(ctx, "fe/s"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("latest before any: %v", err)
		}
		if _, err := st.Submit(ctx, "fe/s", t1.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("submit another front end's revision: %v", err)
		}
		if _, err := st.Submit(ctx, "fe/none", r1.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("submit unknown front end: %v", err)
		}
		s1, err := st.Submit(ctx, "fe/s", r1.ID)
		must(t, err)
		if s1.Status != SubmissionPending || s1.RevisionID != r1.ID || s1.Title != "Sub" || s1.RevisionNumber != 1 || s1.SubmittedAt.IsZero() || !s1.ReviewedAt.IsZero() {
			t.Fatalf("s1 = %+v", s1)
		}
		// again while pending: same submission, now r2
		s2, err := st.Submit(ctx, "fe/s", r2.ID)
		must(t, err)
		if s2.ID != s1.ID || s2.RevisionID != r2.ID || s2.RevisionNumber != 2 {
			t.Fatalf("resubmit = %+v", s2)
		}
		if n, _ := st.PendingSubmissions(ctx); n != 1 {
			t.Fatalf("pending = %d", n)
		}
		time.Sleep(5 * time.Millisecond)
		st1, err := st.Submit(ctx, "fe/t", t1.ID)
		must(t, err)
		if n, _ := st.PendingSubmissions(ctx); n != 2 {
			t.Fatalf("pending = %d", n)
		}
		subs, err := st.Submissions(ctx, 10)
		must(t, err)
		if len(subs) != 2 || subs[0].ID != s1.ID || subs[1].ID != st1.ID {
			t.Fatalf("submissions = %+v", subs)
		}

		// approve: active + in rotation
		ap, err := st.ReviewSubmission(ctx, s1.ID, true)
		must(t, err)
		if ap.Status != SubmissionApproved || ap.ReviewedAt.IsZero() {
			t.Fatalf("approved = %+v", ap)
		}
		f, _ := st.BuilderFrontend(ctx, "fe/s")
		if f.ActiveRevision != r2.ID || !f.InRotation {
			t.Fatalf("after approve = %+v", f)
		}
		if _, err := st.ReviewSubmission(ctx, s1.ID, false); !errors.Is(err, ErrNotPending) {
			t.Fatalf("re-review: %v", err)
		}
		if _, err := st.ReviewSubmission(ctx, 9999, true); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown review: %v", err)
		}
		// reject keeps it private
		time.Sleep(5 * time.Millisecond)
		rj, err := st.ReviewSubmission(ctx, st1.ID, false)
		must(t, err)
		if rj.Status != SubmissionRejected {
			t.Fatalf("rejected = %+v", rj)
		}
		if f, _ := st.BuilderFrontend(ctx, "fe/t"); f.InRotation || f.ActiveRevision != "" {
			t.Fatalf("after reject = %+v", f)
		}
		// the same revision again changes nothing; a new one is a new submission
		if again, _ := st.Submit(ctx, "fe/t", t1.ID); again.ID != st1.ID || again.Status != SubmissionRejected {
			t.Fatalf("resubmit rejected revision = %+v", again)
		}
		r3, _ := st.AddRevision(ctx, Revision{ID: "ssssssss3", FrontendID: "fe/s", ParentID: r2.ID, Author: "visitor"})
		s3, err := st.Submit(ctx, "fe/s", r3.ID)
		must(t, err)
		if s3.ID == s1.ID || s3.Status != SubmissionPending {
			t.Fatalf("new submission = %+v", s3)
		}
		latest, err := st.LatestSubmission(ctx, "fe/s")
		must(t, err)
		if latest.ID != s3.ID {
			t.Fatalf("latest = %+v", latest)
		}
		// the approved revision stays active until the new one is approved
		if f, _ := st.BuilderFrontend(ctx, "fe/s"); f.ActiveRevision != r2.ID {
			t.Fatalf("active changed on submit: %+v", f)
		}
		subs, _ = st.Submissions(ctx, 10)
		if len(subs) != 3 || subs[0].ID != s3.ID || subs[1].ID != st1.ID || subs[2].ID != s1.ID {
			t.Fatalf("pending first, then most recently reviewed: %+v", subs)
		}
		if subs, _ := st.Submissions(ctx, 1); len(subs) != 2 {
			t.Fatalf("limit on reviewed: %d", len(subs))
		}
	})
}

func TestMemoryVisitorConformance(t *testing.T) {
	visitorConformance(t, func(*testing.T) visitorStore { return NewMemory() })
}

func TestPostgresVisitorConformance(t *testing.T) {
	visitorConformance(t, func(t *testing.T) visitorStore { return openPG(t, freshDB(t)) })
}

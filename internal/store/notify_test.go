package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

type notifyStore interface {
	Builder
	Notifies
}

// notifyConformance runs the behavior every Notifies must share.
func notifyConformance(t *testing.T, newStore func(t *testing.T) notifyStore) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	setup := func(t *testing.T) (notifyStore, int64, int64) {
		st := newStore(t)
		must(t, st.CreatePromptedFrontend(ctx, "fe/n", "N"))
		a, err := st.StartRun(ctx, Run{FrontendID: "fe/n", Prompt: "p"})
		must(t, err)
		b, err := st.StartRun(ctx, Run{FrontendID: "fe/n", Prompt: "q"})
		must(t, err)
		return st, a, b
	}
	hash := []byte("0123456789abcdef0123456789abcdef")

	t.Run("set, read, take once", func(t *testing.T) {
		st, a, _ := setup(t)
		if _, err := st.RunNotify(ctx, a); !errors.Is(err, ErrNotFound) {
			t.Fatalf("before set: %v", err)
		}
		must(t, st.SetRunNotify(ctx, RunNotify{RunID: a, Email: "x@example.com", Link: "tok1", AddressHash: hash}))
		must(t, st.SetRunNotify(ctx, RunNotify{RunID: a, Email: "y@example.com", Link: "tok2", AddressHash: hash})) // replaces
		n, err := st.RunNotify(ctx, a)
		must(t, err)
		if n.Email != "y@example.com" || n.Link != "tok2" {
			t.Fatalf("got %+v", n)
		}
		got, ok, err := st.TakeRunNotify(ctx, a, "ready")
		must(t, err)
		if !ok || got.Email != "y@example.com" || got.Link != "tok2" {
			t.Fatalf("take: %v %+v", ok, got)
		}
		if _, ok, _ := st.TakeRunNotify(ctx, a, "ready"); ok {
			t.Fatal("taken twice")
		}
		if _, err := st.RunNotify(ctx, a); !errors.Is(err, ErrNotFound) {
			t.Fatalf("after take: %v", err)
		}
	})

	t.Run("clear", func(t *testing.T) {
		st, a, _ := setup(t)
		must(t, st.SetRunNotify(ctx, RunNotify{RunID: a, Email: "x@example.com", Link: "t", AddressHash: hash}))
		must(t, st.ClearRunNotify(ctx, a))
		must(t, st.ClearRunNotify(ctx, a)) // none: fine
		if _, ok, _ := st.TakeRunNotify(ctx, a, "ready"); ok {
			t.Fatal("cleared request was taken")
		}
	})

	t.Run("unknown run", func(t *testing.T) {
		st, _, _ := setup(t)
		if err := st.SetRunNotify(ctx, RunNotify{RunID: 9999, Email: "x@example.com", AddressHash: hash}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("count per address, taken ones too", func(t *testing.T) {
		st, a, b := setup(t)
		since := time.Now().Add(-time.Minute)
		must(t, st.SetRunNotify(ctx, RunNotify{RunID: a, Email: "x@example.com", Link: "t", AddressHash: hash}))
		must(t, st.SetRunNotify(ctx, RunNotify{RunID: b, Email: "x@example.com", Link: "t", AddressHash: hash}))
		if _, _, err := st.TakeRunNotify(ctx, a, "failed"); err != nil {
			t.Fatal(err)
		}
		n, err := st.NotifyCount(ctx, hash, since)
		must(t, err)
		if n != 2 {
			t.Fatalf("count %d, want 2", n)
		}
		if n, _ := st.NotifyCount(ctx, []byte("other"), since); n != 0 {
			t.Fatalf("other address: %d", n)
		}
		if n, _ := st.NotifyCount(ctx, hash, time.Now().Add(time.Hour)); n != 0 {
			t.Fatalf("future since: %d", n)
		}
	})
}

func TestNotifyMemory(t *testing.T) {
	notifyConformance(t, func(*testing.T) notifyStore { return NewMemory() })
}

func TestNotifyPostgres(t *testing.T) {
	notifyConformance(t, func(t *testing.T) notifyStore { return openPG(t, freshDB(t)) })
}

package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// observerConformance runs the behavior every ObserverStore must share.
func observerConformance(t *testing.T, newStore func(t *testing.T) ObserverStore) {
	ctx := context.Background()

	t.Run("detections", func(t *testing.T) {
		st := newStore(t)
		if _, err := st.Detection(ctx, 999); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing detection err = %v", err)
		}
		d := Detection{Kind: KindContentGap, Frontend: "builtin/site", Collection: "pages", Item: "about",
			Field: "subtitle", Expect: "text", Got: "missing", Signature: "sig-a",
			Sample: json.RawMessage(`{"n":1}`)}
		first, created, err := st.RecordDetection(ctx, d)
		if err != nil || !created || first.ID == 0 || first.Count != 1 || first.Status != StatusNew || first.SeenAt != nil {
			t.Fatalf("first record = %+v, %v, %v", first, created, err)
		}
		if first.Field != "subtitle" || first.FirstSeen.IsZero() {
			t.Fatalf("fields not stored: %+v", first)
		}
		if err := st.SetDetection(ctx, first.ID, StatusFixed, Action{Type: "generate", Value: "v", Auto: true}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		d.Sample = json.RawMessage(`{"n":2}`)
		d.Expect, d.Got = "list", "string"
		again, created, err := st.RecordDetection(ctx, d)
		if err != nil || created || again.ID != first.ID || again.Count != 2 {
			t.Fatalf("dedupe = %+v, %v, %v", again, created, err)
		}
		if again.Status != StatusFixed || again.Action.Value != "v" || !again.Action.Auto {
			t.Fatalf("status/action not kept on dedupe: %+v", again)
		}
		if !again.LastSeen.After(first.LastSeen) || sampleN(again.Sample) != 2 || again.Expect != "list" || again.Got != "string" {
			t.Fatalf("last_seen/sample not refreshed: %+v", again)
		}
		bySig, err := st.DetectionBySignature(ctx, "sig-a")
		if err != nil || bySig.ID != first.ID {
			t.Fatalf("by signature = %+v, %v", bySig, err)
		}

		second, _, err := st.RecordDetection(ctx, Detection{Kind: KindFrontendError, Frontend: "builtin/x",
			Signature: "sig-b", Status: StatusNeedsReview})
		if err != nil || second.Status != StatusNeedsReview || sampleN(second.Sample) != 0 {
			t.Fatalf("second = %+v, %v", second, err)
		}
		all, err := st.Detections(ctx, 0)
		if err != nil || len(all) != 2 || all[0].ID != second.ID {
			t.Fatalf("all (latest first) = %+v, %v", all, err)
		}
		fixed, _ := st.Detections(ctx, 0, StatusFixed)
		if len(fixed) != 1 || fixed[0].ID != first.ID {
			t.Fatalf("fixed = %+v", fixed)
		}
		some, _ := st.Detections(ctx, 0, StatusNew, StatusNeedsReview)
		if len(some) != 1 || some[0].ID != second.ID {
			t.Fatalf("new+review = %+v", some)
		}
		if lim, _ := st.Detections(ctx, 1); len(lim) != 1 {
			t.Fatalf("limit ignored: %d rows", len(lim))
		}
		counts, err := st.DetectionCounts(ctx)
		if err != nil || counts[StatusFixed] != 1 || counts[StatusNeedsReview] != 1 {
			t.Fatalf("counts = %v, %v", counts, err)
		}

		if n, err := st.UnseenDetections(ctx); err != nil || n != 2 {
			t.Fatalf("unseen = %d, %v", n, err)
		}
		if err := st.MarkDetectionsSeen(ctx, first.ID); err != nil {
			t.Fatal(err)
		}
		if err := st.MarkDetectionsSeen(ctx); err != nil {
			t.Fatal(err)
		}
		if n, _ := st.UnseenDetections(ctx); n != 1 {
			t.Fatalf("unseen after marking = %d", n)
		}
		seen, _ := st.Detection(ctx, first.ID)
		if seen.SeenAt == nil {
			t.Fatal("seen_at not set")
		}
		at := *seen.SeenAt
		st.MarkDetectionsSeen(ctx, first.ID)
		if again, _ := st.Detection(ctx, first.ID); !again.SeenAt.Equal(at) {
			t.Fatal("seen_at overwritten by a second view")
		}
		if err := st.SetDetection(ctx, 999, StatusFixed, Action{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("set missing err = %v", err)
		}
	})

	t.Run("generated", func(t *testing.T) {
		st := newStore(t)
		if _, err := st.GeneratedField(ctx, "pages", "about", "subtitle"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing err = %v", err)
		}
		g := GeneratedField{Collection: "pages", Item: "about", Field: "subtitle", Value: "A short bio",
			Model: "m", SourceHash: "h1"}
		if err := st.PutGeneratedField(ctx, g); err != nil {
			t.Fatal(err)
		}
		got, err := st.GeneratedField(ctx, "pages", "about", "subtitle")
		if err != nil || got.Value != "A short bio" || got.Status != GenActive || got.Expect != "text" || got.CreatedAt.IsZero() {
			t.Fatalf("got = %+v, %v", got, err)
		}
		time.Sleep(5 * time.Millisecond)
		g.Value, g.SourceHash, g.Expect = `["a"]`, "h2", "list"
		if err := st.PutGeneratedField(ctx, g); err != nil {
			t.Fatal(err)
		}
		upd, _ := st.GeneratedField(ctx, "pages", "about", "subtitle")
		if upd.Value != `["a"]` || upd.SourceHash != "h2" || upd.Expect != "list" ||
			!upd.CreatedAt.Equal(got.CreatedAt) || !upd.UpdatedAt.After(got.UpdatedAt) {
			t.Fatalf("replace = %+v (was %+v)", upd, got)
		}
		st.PutGeneratedField(ctx, GeneratedField{Collection: "experiments", Item: "x", Field: "tagline", Value: "t", Model: "m", SourceHash: "h"})
		if err := st.SetGeneratedStatus(ctx, "pages", "about", "subtitle", GenStale); err != nil {
			t.Fatal(err)
		}
		if err := st.SetGeneratedStatus(ctx, "pages", "nope", "subtitle", GenStale); !errors.Is(err, ErrNotFound) {
			t.Fatalf("set status missing err = %v", err)
		}
		active, _ := st.GeneratedFields(ctx, GenActive, GenAccepted)
		if len(active) != 1 || active[0].Item != "x" {
			t.Fatalf("active = %+v", active)
		}
		all, _ := st.GeneratedFields(ctx)
		if len(all) != 2 || all[0].Collection != "experiments" || all[1].Status != GenStale {
			t.Fatalf("all = %+v", all)
		}
	})
}

func TestObserverMemoryConformance(t *testing.T) {
	observerConformance(t, func(*testing.T) ObserverStore { return NewObserverMemory() })
}

func TestObserverPostgresConformance(t *testing.T) {
	observerConformance(t, func(t *testing.T) ObserverStore { return openPG(t, freshDB(t)).Observer() })
}

// sampleN reads {"n": N} from a sample (jsonb doesn't keep the original bytes).
func sampleN(raw json.RawMessage) int {
	var v struct{ N int }
	json.Unmarshal(raw, &v)
	return v.N
}

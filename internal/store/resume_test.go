package store

import (
	"context"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/content"
)

func resumeConformance(t *testing.T, st ResumeStore) {
	ctx := context.Background()
	if _, ok, err := st.Resume(ctx); err != nil || ok {
		t.Fatalf("empty store: ok %v err %v", ok, err)
	}
	r := content.DefaultResume()
	if err := st.SetResume(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Bullets["x"] = "mutated after saving"
	got, ok, err := st.Resume(ctx)
	if err != nil || !ok || got.Profile != r.Profile || got.Bullets["stink-studios-technical-director"] == "" || got.Bullets["x"] != "" {
		t.Fatalf("round trip: ok %v err %v %+v", ok, err, got.Bullets)
	}
	got.Profile = "New profile."
	if err := st.SetResume(ctx, got); err != nil {
		t.Fatal(err)
	}
	if again, _, _ := st.Resume(ctx); again.Profile != "New profile." {
		t.Fatalf("update: %q", again.Profile)
	}
}

func TestResumeMemory(t *testing.T)   { resumeConformance(t, NewMemory()) }
func TestResumePostgres(t *testing.T) { resumeConformance(t, openPG(t, freshDB(t))) }

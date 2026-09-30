package frontend

import (
	"slices"
	"testing"
)

func TestParseRotation(t *testing.T) {
	good := map[string]Rotation{
		"builtin/site": {"builtin/site"},
		" builtin/site , builtin/particle-stream ": {"builtin/site", "builtin/particle-stream"},
		"builtin/a,builtin/a":                      {"builtin/a"},
	}
	for in, want := range good {
		got, err := ParseRotation(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("ParseRotation(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", ",", "builtin/site,", "builtin/site,,builtin/x", "builtin/Site", "draft/1/2", "builtin/../x"} {
		if r, err := ParseRotation(bad); err == nil {
			t.Errorf("ParseRotation(%q) = %v, want error", bad, r)
		}
	}
}

func TestRotationFromEnv(t *testing.T) {
	t.Setenv(RotationEnv, "")
	r, err := RotationFromEnv()
	if err != nil || !slices.Equal(r, DefaultRotation) {
		t.Errorf("unset: %v, %v", r, err)
	}
	t.Setenv(RotationEnv, "builtin/particle-stream")
	r, err = RotationFromEnv()
	if err != nil || !slices.Equal(r, Rotation{"builtin/particle-stream"}) {
		t.Errorf("set: %v, %v", r, err)
	}
	t.Setenv(RotationEnv, "builtin/ok,nope")
	if _, err := RotationFromEnv(); err == nil {
		t.Error("invalid: want error")
	}
}

func TestPick(t *testing.T) {
	r := Rotation{"builtin/a", "builtin/b"}
	if got := r.Pick(func(int) int { return 1 }); got != "builtin/b" {
		t.Errorf("Pick = %q", got)
	}
	if got := (Rotation{}).Pick(func(int) int { panic("called") }); got != DefaultRef {
		t.Errorf("empty Pick = %q", got)
	}
	if !r.Contains("builtin/a") || r.Contains("builtin/c") {
		t.Error("Contains")
	}
}

package frontend

import "testing"

func TestValid(t *testing.T) {
	for _, ok := range []string{"builtin/site", "builtin/particle-stream", DefaultRef} {
		if !Valid(ok) {
			t.Errorf("Valid(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "builtin/", "builtin/../x", "builtin/Site", "draft/1/2", "/builtin/site", "builtin/a/b", "builtin/-x"} {
		if Valid(bad) {
			t.Errorf("Valid(%q) = true", bad)
		}
	}
}

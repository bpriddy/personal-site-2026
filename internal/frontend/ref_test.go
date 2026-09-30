package frontend

import (
	"strings"
	"testing"
)

func TestValid(t *testing.T) {
	for _, ok := range []string{"builtin/site", "builtin/particle-stream", DefaultRef, "rev/abcd1234", "rev/" + NewRevisionID()} {
		if !Valid(ok) {
			t.Errorf("Valid(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "builtin/", "builtin/../x", "builtin/Site", "draft/1/2", "/builtin/site", "builtin/a/b", "builtin/-x",
		"fe/site", "rev/abc", "rev/ABCD1234", "rev/abcd-1234", "rev/abcd1234/x", "rev/../abcd1234", "rev/" + strings.Repeat("a", 41)} {
		if Valid(bad) {
			t.Errorf("Valid(%q) = true", bad)
		}
	}
}

func TestValidID(t *testing.T) {
	for _, ok := range []string{"builtin/site", "fe/my-page", "fe/a", "fe/0"} {
		if !ValidID(ok) {
			t.Errorf("ValidID(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "fe/", "fe/-x", "fe/A", "fe/a/b", "rev/abcd1234", "fe/../x", "builtin/Site"} {
		if ValidID(bad) {
			t.Errorf("ValidID(%q) = true", bad)
		}
	}
	if !IsPrompted("fe/x") || IsPrompted("builtin/x") || !IsBuiltin("builtin/x") || IsBuiltin("fe/x") {
		t.Error("IsPrompted/IsBuiltin")
	}
}

func TestRevisionIDs(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := NewRevisionID()
		if !ValidRevisionID(id) || seen[id] {
			t.Fatalf("NewRevisionID = %q (dup %v)", id, seen[id])
		}
		seen[id] = true
		if RevisionOf(RevRef(id)) != id {
			t.Fatalf("RevisionOf(RevRef(%q))", id)
		}
	}
	if RevisionOf("builtin/site") != "" || RevisionOf("rev/x") != "" {
		t.Error("RevisionOf non-rev")
	}
}

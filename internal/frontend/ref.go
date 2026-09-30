// Package frontend holds what the main site and the user-content service share
// about front ends: how they are named (IDs and servable Refs) and where their
// files live. See docs/frontend-protocol.md, "Front ends vs. revisions".
package frontend

import (
	"crypto/rand"
	"regexp"
	"strings"
)

// A Ref is either a front-end ID or a servable ref, depending on context:
//
//   - front-end IDs (ValidID) name a front end: "builtin/<name>" or
//     "fe/<slug>" (prompted). The rotation, the fe_pick cookie and the
//     frontends table hold IDs.
//   - servable refs (Valid) name files: "builtin/<name>" or "rev/<id>" (one
//     immutable revision of a prompted front end). Tokens carry servable refs.
//
// Built-ins are both: their ID is their servable ref.
type Ref = string

const DefaultRef Ref = "builtin/site"

var (
	builtinPattern  = regexp.MustCompile(`^builtin/[a-z0-9][a-z0-9-]{0,62}$`)
	promptedPattern = regexp.MustCompile(`^fe/[a-z0-9][a-z0-9-]{0,62}$`)
	revPattern      = regexp.MustCompile(`^rev/[a-z0-9]{8,40}$`)
	revIDPattern    = regexp.MustCompile(`^[a-z0-9]{8,40}$`)
	slugPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

// Valid reports whether ref is a well-formed servable ref: "builtin/<name>" or
// "rev/<id>". The user-content service must check this before using a ref as
// a storage path.
func Valid(ref string) bool { return builtinPattern.MatchString(ref) || revPattern.MatchString(ref) }

// ValidID reports whether id is a well-formed front-end ID: "builtin/<name>"
// or "fe/<slug>".
func ValidID(id string) bool {
	return builtinPattern.MatchString(id) || promptedPattern.MatchString(id)
}

// IsBuiltin reports whether id is a built-in front end (its own servable ref).
func IsBuiltin(id string) bool { return builtinPattern.MatchString(id) }

// IsPrompted reports whether id is a prompted front end ("fe/<slug>").
func IsPrompted(id string) bool { return promptedPattern.MatchString(id) }

// ValidSlug reports whether slug may follow "fe/".
func ValidSlug(slug string) bool { return slugPattern.MatchString(slug) }

// PromptedID returns the front-end ID for a prompted front end's slug.
func PromptedID(slug string) Ref { return "fe/" + slug }

// ValidRevisionID reports whether id is a well-formed revision ID.
func ValidRevisionID(id string) bool { return revIDPattern.MatchString(id) }

// RevRef returns the servable ref of a revision.
func RevRef(revID string) Ref { return "rev/" + revID }

// RevisionOf returns the revision ID of a "rev/<id>" ref, or "" if ref isn't one.
func RevisionOf(ref Ref) string {
	if !revPattern.MatchString(ref) {
		return ""
	}
	return strings.TrimPrefix(ref, "rev/")
}

// NewRevisionID returns a fresh random revision ID: 20 characters of
// [a-z0-9] (about 103 bits), unguessable, so a revision's files can't be
// enumerated even though they are only ever served behind a signed token.
func NewRevisionID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 20)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

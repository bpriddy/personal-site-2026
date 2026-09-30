// Package frontend holds what the main site and the user-content service share
// about front ends: how they are named (Refs) and where their files live.
package frontend

import "regexp"

// A Ref names one servable front end. Only built-ins exist today; drafts
// ("draft/<id>/<rev>") and approved snapshots ("snap/<id>") come with the builder.
type Ref = string

const DefaultRef Ref = "builtin/site"

var refPattern = regexp.MustCompile(`^builtin/[a-z0-9][a-z0-9-]{0,62}$`)

// Valid reports whether ref is well-formed. The user-content service must check
// this before using a ref as a storage path.
func Valid(ref string) bool { return refPattern.MatchString(ref) }

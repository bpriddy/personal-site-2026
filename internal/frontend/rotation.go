package frontend

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// A Rotation is the list of approved front ends a visitor may be shown. Each
// visit picks one at random and keeps it (see docs/frontend-protocol.md).
type Rotation []Ref

// DefaultRotation is the approved rotation while it is still static.
var DefaultRotation = Rotation{DefaultRef, "builtin/particle-stream"}

// RotationEnv names the env var that overrides DefaultRotation: comma-separated
// front-end IDs, each of which must pass ValidID. Unset or empty means DefaultRotation.
// It exists so tests (e.g. e2e) can pin the rotation to a known front end.
const RotationEnv = "FRONTEND_ROTATION"

// ParseRotation parses a comma-separated list of refs. Whitespace around refs is
// ignored; an empty list, an empty entry or an invalid ref is an error.
func ParseRotation(s string) (Rotation, error) {
	var out Rotation
	for part := range strings.SplitSeq(s, ",") {
		ref := strings.TrimSpace(part)
		if ref == "" {
			return nil, errors.New("frontend: empty ref in rotation")
		}
		if !ValidID(ref) {
			return nil, fmt.Errorf("frontend: invalid ref %q in rotation", ref)
		}
		if !slices.Contains(out, ref) {
			out = append(out, ref)
		}
	}
	return out, nil
}

// RotationFromEnv returns the rotation from FRONTEND_ROTATION, or
// DefaultRotation when it is unset or empty.
func RotationFromEnv() (Rotation, error) {
	v := strings.TrimSpace(os.Getenv(RotationEnv))
	if v == "" {
		return slices.Clone(DefaultRotation), nil
	}
	r, err := ParseRotation(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", RotationEnv, err)
	}
	return r, nil
}

// RotationOverride returns the FRONTEND_ROTATION rotation and true when the env
// var is set, so callers can skip the database-backed rotation.
func RotationOverride() (Rotation, bool, error) {
	if strings.TrimSpace(os.Getenv(RotationEnv)) == "" {
		return nil, false, nil
	}
	r, err := RotationFromEnv()
	return r, err == nil, err
}

// Contains reports whether ref is in the rotation.
func (r Rotation) Contains(ref Ref) bool { return slices.Contains(r, ref) }

// Pick chooses a ref at random; intn(n) must return a value in [0, n), like
// math/rand/v2.IntN. An empty rotation yields DefaultRef.
func (r Rotation) Pick(intn func(int) int) Ref {
	if len(r) == 0 {
		return DefaultRef
	}
	return r[intn(len(r))]
}

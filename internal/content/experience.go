package content

import (
	"regexp"
	"time"
)

// Experience is one role in Ben's career, for the home page's experience
// list (docs/frontend-protocol.md, v1.8): deliberately minimal (a role, a
// company, dates and at most one short line), so the CV lives in one place.
type Experience struct {
	Slug    string
	Role    string // "Global Head of AI Technology"
	Company string // "Anomaly"
	Start   string // "YYYY-MM" or "YYYY"
	End     string // "YYYY-MM", "YYYY", or "" while Current
	Current bool   // the role he holds now
	Note    string // one short line, or ""
	// Order: lower first (the list reads newest first by convention).
	Order     int
	Published bool
	UpdatedAt time.Time
}

var monthPattern = regexp.MustCompile(`^(19|20)\d\d(-(0[1-9]|1[0-2]))?$`)

// ValidMonth reports whether s is "YYYY-MM", "YYYY", or "".
func ValidMonth(s string) bool { return s == "" || monthPattern.MatchString(s) }

package content

import "regexp"

// A CopyLine is one site-wide line Ben can edit (protocol v1.10). Front ends
// read it with site.text(key, fallback); an empty stored value means Default.
type CopyLine struct {
	Key     string
	Label   string // for the admin form
	Hint    string
	Default string
}

// SiteCopy is every site-wide line, in admin order.
var SiteCopy = []CopyLine{
	{Key: "tagline", Label: "Tagline", Hint: "a short line under your name", Default: "Creative technology / AI"},
	{Key: "concept", Label: "Site bar line", Hint: "the concept line in the bar on every front end", Default: "This site is re-imagined by its visitors"},
	{Key: "experimentsEmpty", Label: "Experiments, when none are published", Default: "Coming soon."},
}

// MaxCopyLen bounds one line of copy (site-wide or a front end's own).
const MaxCopyLen = 2000

// CopyKey is the shape of a copy key: a front end's own keys and the
// site-wide ones alike.
var CopyKey = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,47}$`)

// ResolveSiteCopy fills the defaults in beneath the stored lines.
func ResolveSiteCopy(stored map[string]string) map[string]string {
	out := make(map[string]string, len(SiteCopy))
	for _, l := range SiteCopy {
		out[l.Key] = l.Default
		if v := stored[l.Key]; v != "" {
			out[l.Key] = v
		}
	}
	return out
}

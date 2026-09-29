// Package content defines the CMS's content types.
package content

import "time"

// Page is a CMS-managed page, served at /<slug> (the home page has slug "").
type Page struct {
	Slug      string
	Title     string
	Body      string // plain text for now; markdown rendering comes later
	Published bool
	UpdatedAt time.Time
}

// Experiment is a standalone piece built into experiments/<slug>/dist and
// served at /experiments/<slug>/. The CMS holds its listing metadata only.
type Experiment struct {
	Slug      string
	Title     string
	Summary   string
	Published bool
	Order     int
	UpdatedAt time.Time
}

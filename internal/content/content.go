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

// Experiment is a standalone piece built from experiments/<slug>/. The CMS
// holds its listing metadata only; the main site lists it but no longer serves
// its files (front ends are served by the user-content service).
type Experiment struct {
	Slug      string
	Title     string
	Summary   string
	Published bool
	Order     int
	UpdatedAt time.Time
}

// Frontend is a servable front end (see docs/frontends.md). Visitors are shown
// one at random from those InRotation.
type Frontend struct {
	Ref        string // e.g. "builtin/site"; see internal/frontend
	Title      string
	InRotation bool
	UpdatedAt  time.Time
}

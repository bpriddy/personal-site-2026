package store

import "context"

// CopyStore persists the site-wide copy (migration 0009): key → text. Memory
// and Postgres implement it; callers find it with CopyOf.
type CopyStore interface {
	// SiteCopy returns every stored line (keys absent from it use defaults).
	SiteCopy(ctx context.Context) (map[string]string, error)
	// SetSiteCopy stores the given lines; an empty value deletes the key.
	SetSiteCopy(ctx context.Context, lines map[string]string) error
}

// CopyOf returns st's CopyStore, or nil if it has none.
func CopyOf(st any) CopyStore {
	c, _ := st.(CopyStore)
	return c
}

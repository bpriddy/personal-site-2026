// Package contract builds the content contract front ends consume
// (docs/content-contract.json; docs/observer.md, "Layer 1").
package contract

import "context"

// Key identifies one content item: a collection ("pages", "experiments") and
// the item's key within it (its slug).
type Key struct {
	Collection string
	Item       string
}

// Generated supplies observer-generated field values, merged beneath human
// content: human value, then generated value, then the contract default.
// Implemented by the observer's store; may be nil.
type Generated interface {
	GeneratedFields(ctx context.Context) (map[Key]map[string]string, error)
}

package observer

import (
	"context"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/contract"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// The observer supplies generated values to the content contract.
var _ contract.Generated = (*Observer)(nil)

// served returns the generated values front ends should see: active and
// accepted values of existing items whose own field (if the field is one) is
// empty, since your value always wins. An active value whose item's content
// changed since it was generated is marked stale and withheld; the next gap
// report regenerates it. items may be nil (it is then loaded).
func (o *Observer) served(ctx context.Context, items map[[2]string]item) ([]store.GeneratedField, error) {
	rows, err := o.obs.GeneratedFields(ctx, store.GenActive, store.GenAccepted)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	if items == nil {
		if items, err = o.allItems(ctx); err != nil {
			return nil, err
		}
	}
	out := rows[:0]
	for _, g := range rows {
		it, ok := items[[2]string{g.Collection, g.Item}]
		if !ok {
			continue
		}
		if strings.TrimSpace(it.fields[g.Field]) != "" {
			continue // your value wins
		}
		if g.Status == store.GenActive && g.SourceHash != it.hash() {
			if err := o.obs.SetGeneratedStatus(ctx, g.Collection, g.Item, g.Field, store.GenStale); err != nil {
				o.log.Error("observer: mark stale", "err", err)
			}
			continue
		}
		out = append(out, g)
	}
	return out, nil
}

// GeneratedFields implements contract.Generated: the values to merge beneath
// human content, as stored strings (text as is; list, number and bool values
// JSON-encoded; see GeneratedValues for typed values).
func (o *Observer) GeneratedFields(ctx context.Context) (map[contract.Key]map[string]string, error) {
	rows, err := o.served(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := map[contract.Key]map[string]string{}
	for _, g := range rows {
		k := contract.Key{Collection: g.Collection, Item: g.Item}
		if out[k] == nil {
			out[k] = map[string]string{}
		}
		out[k][g.Field] = g.Value
	}
	return out, nil
}

// GeneratedValues is GeneratedFields with each value decoded to its type:
// string, []string, float64 or bool.
func (o *Observer) GeneratedValues(ctx context.Context) (map[contract.Key]map[string]any, error) {
	rows, err := o.served(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := map[contract.Key]map[string]any{}
	for _, g := range rows {
		k := contract.Key{Collection: g.Collection, Item: g.Item}
		if out[k] == nil {
			out[k] = map[string]any{}
		}
		out[k][g.Field] = decodeValue(g.Expect, g.Value)
	}
	return out, nil
}

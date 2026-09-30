package observer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/store"
)

// Server-side content checks: published content that front ends can't render
// correctly whatever they do. They run on a ticker (Start), so they need no
// hooks in the admin's save handlers. Each problem is a content-invalid
// detection that needs review; when a later check finds it gone, the
// detection is marked fixed.

type contentIssue struct {
	collection, item, field, summary string
}

func (c contentIssue) signature() string {
	return signature(store.KindContentInvalid, "", c.collection, c.item, c.field)
}

func findIssues(items map[[2]string]item) []contentIssue {
	var out []contentIssue
	if home, ok := items[[2]string{"pages", ""}]; !ok || !home.published {
		out = append(out, contentIssue{"pages", "", "", "the home page is missing or unpublished; the site has no front page"})
	}
	for _, it := range items {
		if !it.published {
			continue
		}
		for _, f := range it.names {
			if strings.TrimSpace(it.fields[f]) == "" {
				what := "a published " + strings.TrimSuffix(it.collection, "s")
				out = append(out, contentIssue{it.collection, it.slug, f,
					fmt.Sprintf("%s (%s) has an empty %s", what, it.label(), f)})
			}
		}
	}
	return out
}

// CheckContent runs the content checks and the stale sweep once.
func (o *Observer) CheckContent(ctx context.Context) {
	items, err := o.allItems(ctx)
	if err != nil {
		o.log.Error("observer: content check", "err", err)
		return
	}
	current := map[string]bool{}
	for _, is := range findIssues(items) {
		sig := is.signature()
		current[sig] = true
		a := store.Action{Type: "review", Summary: is.summary, At: o.now()}
		d, err := o.obs.DetectionBySignature(ctx, sig)
		switch {
		case errors.Is(err, store.ErrNotFound):
			_, _, err = o.obs.RecordDetection(ctx, store.Detection{Kind: store.KindContentInvalid,
				Collection: is.collection, Item: is.item, Field: is.field, Signature: sig,
				Status: store.StatusNeedsReview, Action: a})
		case err != nil:
		case d.Status == store.StatusFixed:
			// it came back: reopen (and count the recurrence)
			if _, _, err = o.obs.RecordDetection(ctx, d); err == nil {
				err = o.obs.SetDetection(ctx, d.ID, store.StatusNeedsReview, a)
			}
		}
		if err != nil {
			o.log.Error("observer: content check", "err", err)
		}
	}
	open, err := o.obs.Detections(ctx, 1000, store.StatusNew, store.StatusNeedsReview)
	if err != nil {
		o.log.Error("observer: content check", "err", err)
		return
	}
	for _, d := range open {
		if d.Kind == store.KindContentInvalid && !current[d.Signature] {
			o.obs.SetDetection(ctx, d.ID, store.StatusFixed, store.Action{Type: "resolved", At: o.now(),
				Summary: "fixed by an edit: " + d.Action.Summary})
		}
	}
	if _, err := o.served(ctx, items); err != nil {
		o.log.Error("observer: stale sweep", "err", err)
	}
}

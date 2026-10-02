package observer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/contract"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// heal fixes a content gap or type break. It decides from the current state
// (human value, generated value, content hash), not from whether the report is
// new, so repeated reports are cheap no-ops and a fix survives restarts.
// force (the admin's Regenerate) generates a new value even if one is active,
// cleared, or a previous attempt needed review. It returns the resulting
// detection's error, if any, for the admin.
func (o *Observer) heal(ctx context.Context, id int64, force bool) error {
	d, err := o.obs.Detection(ctx, id)
	if err != nil {
		return err
	}
	if d.Kind != store.KindContentGap && d.Kind != store.KindTypeBreak {
		return fmt.Errorf("observer: %s detections aren't healed by generation", d.Kind)
	}
	if d.Status == store.StatusDismissed && !force {
		return nil
	}
	it, err := o.loadItem(ctx, d.Collection, d.Item)
	if err != nil {
		return err
	}
	hash := it.hash()
	set := func(status string, a store.Action) error {
		a.At = o.now()
		if a.SourceHash == "" {
			a.SourceHash = hash
		}
		return o.obs.SetDetection(ctx, d.ID, status, a)
	}

	// 1. Your value always wins.
	if human := it.fields[d.Field]; strings.TrimSpace(human) != "" {
		if d.Kind == store.KindContentGap {
			if d.Status == store.StatusFixed {
				return nil
			}
			return set(store.StatusFixed, store.Action{Type: "resolved",
				Summary: fmt.Sprintf("%s of %s has your value now; nothing to generate", d.Field, it.label())})
		}
		// the report means normalization didn't coerce it, and changing your
		// value is yours to decide
		if v, ok := coerce(human, d.Expect); ok {
			return set(store.StatusNeedsReview, store.Action{Type: "review", Value: v,
				Summary: fmt.Sprintf("%s expects %s of %s to be %s, but your value is text; it converts losslessly to %s, but changing it is your call",
					d.Frontend, d.Field, it.label(), d.Expect, display(v))})
		}
		return set(store.StatusNeedsReview, store.Action{Type: "review",
			Summary: fmt.Sprintf("%s expects %s of %s to be %s, but your value is text; fixing it would overwrite your content",
				d.Frontend, d.Field, it.label(), d.Expect)})
	}

	// Facts (a client, a year, a link, a video, lists, media) are never
	// invented: the contract wouldn't serve them anyway (contract.Factual).
	if contract.Factual(d.Collection, d.Field) {
		return set(store.StatusNeedsReview, store.Action{Type: "review",
			Summary: fmt.Sprintf("%s wants %s of %s, which is empty; it's a fact, so it isn't generated: fill it in, or dismiss this if it's empty on purpose",
				d.Frontend, d.Field, it.label())})
	}

	// The contract merges generated values as strings, so only text can be
	// served until it carries typed values (Config.TypedValues).
	if d.Expect != "text" && !o.cfg.TypedValues {
		return set(store.StatusNeedsReview, store.Action{Type: "review",
			Summary: fmt.Sprintf("%s expects %s of %s to be a %s; generated values are served as text only, so this needs a schema field or a front-end change",
				d.Frontend, d.Field, it.label(), d.Expect)})
	}

	// 2. An existing generated value.
	g, gerr := o.obs.GeneratedField(ctx, d.Collection, d.Item, d.Field)
	switch {
	case errors.Is(gerr, store.ErrNotFound):
	case gerr != nil:
		return gerr
	case g.Status == store.GenCleared && !force:
		return nil // you reverted it; only Regenerate brings it back
	case g.Status == store.GenAccepted:
		if g.Expect == d.Expect {
			return set(store.StatusFixed, store.Action{Type: "resolved", Value: g.Value,
				Summary: fmt.Sprintf("%s of %s has an accepted value: %q", d.Field, it.label(), display(g.Value))})
		}
		if v, ok := coerceStored(g, d.Expect); ok {
			return set(store.StatusNeedsReview, store.Action{Type: "review", Value: v,
				Summary: fmt.Sprintf("%s expects %s of %s to be %s, but the accepted value is %s; proposed: %s",
					d.Frontend, d.Field, it.label(), d.Expect, g.Expect, display(v))})
		}
		return set(store.StatusNeedsReview, store.Action{Type: "review",
			Summary: fmt.Sprintf("%s expects %s of %s to be %s, but the accepted value is %s",
				d.Frontend, d.Field, it.label(), d.Expect, g.Expect)})
	case g.Status == store.GenActive && g.SourceHash == hash && !force:
		if g.Expect == d.Expect {
			if d.Status != store.StatusFixed {
				return set(store.StatusFixed, store.Action{Type: "generate", Auto: true, Value: g.Value,
					Summary: fmt.Sprintf("%s of %s already generated: %q", d.Field, it.label(), display(g.Value))})
			}
			return nil
		}
		if v, ok := coerceStored(g, d.Expect); ok {
			g.Value, g.Expect = v, d.Expect
			if err := o.obs.PutGeneratedField(ctx, g); err != nil {
				return err
			}
			return set(store.StatusFixed, store.Action{Type: "coerce", Auto: true, Value: v,
				Summary: fmt.Sprintf("converted the generated %s of %s to %s: %s", d.Field, it.label(), d.Expect, display(v))})
		}
		// not convertible: generate a value of the type now expected
	}

	// 3. Generate.
	if o.model == nil {
		return set(store.StatusNew, store.Action{Type: "disabled",
			Summary: "healing is off (ANTHROPIC_API_KEY unset); the front end shows its fallback"})
	}
	if !force && d.Status == store.StatusNeedsReview && d.Action.SourceHash == hash {
		return nil // already tried on this content; wait for an edit
	}
	src := it.others(d.Field)
	if !enoughSource(src) {
		return set(store.StatusNeedsReview, store.Action{Type: "review",
			Summary: fmt.Sprintf("too little content in %s to generate %s from; please write it", it.label(), d.Field)})
	}
	if errors.Is(gerr, store.ErrNotFound) {
		if n, err := o.generatedCount(ctx, d.Collection, d.Item); err != nil {
			return err
		} else if n >= o.cfg.MaxGenItem {
			return set(store.StatusNeedsReview, store.Action{Type: "review",
				Summary: fmt.Sprintf("%s already has %d generated fields; not generating %s without review", it.label(), n, d.Field)})
		}
	}
	if !force && !o.takeBudget() {
		// leave the source hash unset so the next report retries
		return o.obs.SetDetection(ctx, d.ID, d.Status, store.Action{Type: "deferred", At: o.now(),
			Summary: "generation budget for this hour is used up; the next report retries"})
	}
	gctx, cancel := context.WithTimeout(ctx, genTimeout)
	defer cancel()
	req := GenerateRequest{Collection: d.Collection, Item: d.Item, Field: d.Field, Expect: d.Expect, Content: src}
	var res Generation
	var value string
	var verr error
	// one retry when the model thinks the content suffices but its answer is
	// unusable (invalid, or oddly low confidence): that is usually noise
	for attempt := 0; attempt < 2; attempt++ {
		res, err = o.model.Generate(gctx, req)
		if err != nil {
			break
		}
		value, verr = encodeValue(d.Expect, res.Value)
		if !res.EnoughContent || (verr == nil && res.Confidence >= minConfidence) {
			break
		}
	}
	if errors.Is(err, ErrRefused) {
		return set(store.StatusNeedsReview, store.Action{Type: "review",
			Summary: fmt.Sprintf("the model declined to write %s for %s; please write it", d.Field, it.label())})
	}
	if err != nil {
		o.log.Error("observer: generate", "err", err)
		o.obs.SetDetection(ctx, d.ID, d.Status, store.Action{Type: "error", At: o.now(),
			Summary: "generation failed; the next report retries"})
		return err
	}
	if !res.EnoughContent || verr != nil || res.Confidence < minConfidence {
		why := "the content doesn't support a value"
		if res.EnoughContent && verr != nil {
			why = "the generated value was invalid: " + verr.Error()
		} else if res.EnoughContent {
			why = fmt.Sprintf("low confidence (%.2f)", res.Confidence)
		}
		return set(store.StatusNeedsReview, store.Action{Type: "review", Value: value,
			Summary: fmt.Sprintf("not generating %s for %s: %s", d.Field, it.label(), why)})
	}
	model := res.Model
	if model == "" {
		model = o.cfg.ModelName
	}
	if err := o.obs.PutGeneratedField(ctx, store.GeneratedField{
		Collection: d.Collection, Item: d.Item, Field: d.Field, Value: value, Expect: d.Expect,
		Model: model, SourceHash: hash, Status: store.GenActive,
	}); err != nil {
		return err
	}
	o.log.Info("observer: generated", "collection", d.Collection, "item", d.Item, "field", d.Field)
	return set(store.StatusFixed, store.Action{Type: "generate", Auto: true, Value: value,
		Summary: fmt.Sprintf("generated %s for %s: %q", d.Field, it.label(), display(value))})
}

// genTimeout bounds one model call.
const genTimeout = 90 * time.Second

// minConfidence is the model confidence below which a value needs review.
const minConfidence = 0.5

// coerceStored converts a generated value to another type, losslessly.
func coerceStored(g store.GeneratedField, expect string) (string, bool) {
	if g.Expect == "text" {
		return coerce(g.Value, expect)
	}
	if expect == "text" && g.Expect != "list" {
		return g.Value, true // a number or bool reads fine as text
	}
	return "", false
}

func (o *Observer) generatedCount(ctx context.Context, collection, slug string) (int, error) {
	all, err := o.obs.GeneratedFields(ctx, store.GenActive, store.GenAccepted, store.GenStale)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, g := range all {
		if g.Collection == collection && g.Item == slug {
			n++
		}
	}
	return n, nil
}

// registered reports whether ref is a registered front end.
func (o *Observer) registered(ctx context.Context, ref string) (bool, error) {
	fes, err := o.content.Frontends(ctx)
	if err != nil {
		return false, err
	}
	for _, f := range fes {
		if f.Ref == ref {
			return true, nil
		}
	}
	return false, nil
}

var notReadyPattern = regexp.MustCompile(`(?i)time[sd]?[ -]?out|not ready|never (got |became |reached )?ready|fall(ing)? ?back`)

// isNotReady reports whether an error report means the front end never got
// ready (the parent's ready timeout or fallback).
func isNotReady(r Report) bool {
	return r.Got == "timeout" || notReadyPattern.MatchString(r.Message)
}

// frontendError handles one front-end error report: the detection needs
// review (patching front-end code is a later phase), and a front end that
// keeps failing is pulled from the rotation so the default serves instead.
func (o *Observer) frontendError(ctx context.Context, d store.Detection, r Report, created bool) {
	if created {
		msg := cmp.Or(r.Message, "(no message)")
		o.obs.SetDetection(ctx, d.ID, store.StatusNeedsReview, store.Action{Type: "review", At: o.now(),
			Summary: fmt.Sprintf("%s reported an error: %s. Front-end patches aren't automated yet.", d.Frontend, display(msg))})
	}
	now := o.now()
	o.mu.Lock()
	marks := o.errors[d.Frontend][:0:0]
	for _, m := range o.errors[d.Frontend] {
		if now.Sub(m.at) < o.cfg.ErrorWindow {
			marks = append(marks, m)
		}
	}
	marks = append(marks, errorMark{at: now, notReady: isNotReady(r)})
	total, notReady := len(marks), 0
	for _, m := range marks {
		if m.notReady {
			notReady++
		}
	}
	pull := total >= o.cfg.ErrorLimit || notReady >= o.cfg.NotReady
	if pull {
		delete(o.errors, d.Frontend)
	} else {
		o.errors[d.Frontend] = marks
	}
	o.mu.Unlock()
	if !pull || d.Frontend == frontend.DefaultRef || d.Status == store.StatusDismissed {
		return
	}
	in, err := o.inRotation(ctx, d.Frontend)
	if err != nil || !in {
		return
	}
	if err := o.content.SetFrontendInRotation(ctx, d.Frontend, false); err != nil {
		o.log.Error("observer: pull from rotation", "frontend", d.Frontend, "err", err)
		return
	}
	why := fmt.Sprintf("%d errors", total)
	if notReady >= o.cfg.NotReady {
		why = fmt.Sprintf("%d reports of never getting ready", notReady)
	}
	o.log.Warn("observer: pulled front end from the rotation", "frontend", d.Frontend, "why", why)
	o.obs.SetDetection(ctx, d.ID, store.StatusNeedsReview, store.Action{Type: "pull", Auto: true, At: now,
		Pulled: d.Frontend,
		Summary: fmt.Sprintf("pulled %s from the rotation after %s in %s; the default front end serves. Revert re-adds it. Last error: %s",
			d.Frontend, why, o.cfg.ErrorWindow, display(cmp.Or(r.Message, "(no message)")))})
}

func (o *Observer) inRotation(ctx context.Context, ref string) (bool, error) {
	fes, err := o.content.Frontends(ctx)
	if err != nil {
		return false, err
	}
	for _, f := range fes {
		if f.Ref == ref {
			return f.InRotation, nil
		}
	}
	return false, nil
}

package observer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/store"
)

// Admin actions on a detection (docs/observer.md, "Detections and the admin").
// Errors wrapping ErrAction are the admin's mistakes (shown to them); others
// are internal.

var ErrAction = errors.New("observer: action not possible")

func actionErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrAction, fmt.Sprintf(format, a...))
}

func (o *Observer) fieldDetection(ctx context.Context, id int64) (store.Detection, item, error) {
	d, err := o.obs.Detection(ctx, id)
	if err != nil {
		return d, item{}, err
	}
	if d.Kind != store.KindContentGap && d.Kind != store.KindTypeBreak {
		return d, item{}, actionErr("only content gaps and type breaks have a value")
	}
	it, err := o.loadItem(ctx, d.Collection, d.Item)
	if errors.Is(err, errNoItem) {
		return d, it, actionErr("%s %q no longer exists or is unpublished", d.Collection, d.Item)
	}
	return d, it, err
}

// saveHuman writes value into an item's own field through the content store.
func (o *Observer) saveHuman(ctx context.Context, collection, slug, field, value string) error {
	switch collection {
	case "pages":
		p, err := o.content.Page(ctx, slug)
		if err != nil {
			return err
		}
		switch field {
		case "title":
			p.Title = value
		case "body":
			p.Body = value
		}
		return o.content.SavePage(ctx, p)
	case "experiments":
		e, err := o.content.Experiment(ctx, slug)
		if err != nil {
			return err
		}
		switch field {
		case "title":
			e.Title = value
		case "summary":
			e.Summary = value
		}
		return o.content.SaveExperiment(ctx, e)
	case "projects":
		ps := store.ProjectsOf(o.content)
		if ps == nil {
			return actionErr("unknown collection")
		}
		p, err := ps.Project(ctx, slug)
		if err != nil {
			return err
		}
		switch field {
		case "title":
			p.Title = value
		case "summary":
			p.Summary = value
		}
		return ps.SaveProject(ctx, p)
	}
	return actionErr("unknown collection")
}

// Accept promotes the generated value: into your content for the item's own
// fields (page title/body, experiment title/summary), or, for extra fields,
// by marking it accepted (served and kept, no longer regenerated).
func (o *Observer) Accept(ctx context.Context, id int64) error {
	d, it, err := o.fieldDetection(ctx, id)
	if err != nil {
		return err
	}
	g, err := o.obs.GeneratedField(ctx, d.Collection, d.Item, d.Field)
	if errors.Is(err, store.ErrNotFound) || (err == nil && g.Status != store.GenActive && g.Status != store.GenStale) {
		return actionErr("there is no generated value to accept")
	}
	if err != nil {
		return err
	}
	summary := fmt.Sprintf("accepted %s for %s as an extra field: %q", d.Field, it.label(), display(g.Value))
	if it.native(d.Field) {
		if it.fields[d.Field] != "" {
			return actionErr("%s already has your value; accepting would overwrite it", d.Field)
		}
		if err := o.saveHuman(ctx, d.Collection, d.Item, d.Field, g.Value); err != nil {
			return err
		}
		summary = fmt.Sprintf("saved the generated %s into %s: %q", d.Field, it.label(), display(g.Value))
	}
	if err := o.obs.SetGeneratedStatus(ctx, d.Collection, d.Item, d.Field, store.GenAccepted); err != nil {
		return err
	}
	return o.obs.SetDetection(ctx, d.ID, store.StatusFixed, store.Action{Type: "accept", Value: g.Value, Summary: summary, At: o.now()})
}

// Edit sets the value yourself: the item's own field through the content
// store, or an extra field as an accepted value.
func (o *Observer) Edit(ctx context.Context, id int64, input string) error {
	d, it, err := o.fieldDetection(ctx, id)
	if err != nil {
		return err
	}
	expect := d.Expect
	if it.native(d.Field) {
		expect = "text" // the item's own fields are text
	}
	if expect != "text" && !o.cfg.TypedValues {
		return actionErr("generated values are served as text only; a %s needs a schema field", expect)
	}
	value, err := parseEdit(expect, input)
	if err != nil {
		return actionErr("%v", err)
	}
	if it.native(d.Field) {
		if err := o.saveHuman(ctx, d.Collection, d.Item, d.Field, value); err != nil {
			return err
		}
		// any generated value is now shadowed; record that it was replaced
		if err := o.obs.SetGeneratedStatus(ctx, d.Collection, d.Item, d.Field, store.GenCleared); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	} else {
		it, _ = o.loadItem(ctx, d.Collection, d.Item)
		if err := o.obs.PutGeneratedField(ctx, store.GeneratedField{
			Collection: d.Collection, Item: d.Item, Field: d.Field, Value: value, Expect: expect,
			Model: "human", SourceHash: it.hash(), Status: store.GenAccepted,
		}); err != nil {
			return err
		}
	}
	return o.obs.SetDetection(ctx, d.ID, store.StatusFixed, store.Action{Type: "edit", Value: value, At: o.now(),
		Summary: fmt.Sprintf("you set %s for %s: %q", d.Field, it.label(), display(value))})
}

// Regenerate generates a new value now, replacing an active, stale or cleared
// generated value (never your content).
func (o *Observer) Regenerate(ctx context.Context, id int64) error {
	d, it, err := o.fieldDetection(ctx, id)
	if err != nil {
		return err
	}
	if o.model == nil {
		return actionErr("healing is off (ANTHROPIC_API_KEY unset)")
	}
	if it.fields[d.Field] != "" {
		return actionErr("%s has your value; it isn't regenerated", d.Field)
	}
	if g, err := o.obs.GeneratedField(ctx, d.Collection, d.Item, d.Field); err == nil && g.Status == store.GenAccepted {
		return actionErr("the value was accepted; edit it instead")
	}
	return o.heal(ctx, id, true)
}

// Revert undoes what the observer did: clears a generated value (the front end
// shows its fallback, and the value isn't regenerated unless you ask), or
// puts a pulled front end back in the rotation.
func (o *Observer) Revert(ctx context.Context, id int64) error {
	d, err := o.obs.Detection(ctx, id)
	if err != nil {
		return err
	}
	a := store.Action{Type: "revert", At: o.now()}
	switch d.Kind {
	case store.KindContentGap, store.KindTypeBreak:
		g, err := o.obs.GeneratedField(ctx, d.Collection, d.Item, d.Field)
		if errors.Is(err, store.ErrNotFound) || (err == nil && g.Status == store.GenCleared) {
			return actionErr("there is no generated value to revert")
		}
		if err != nil {
			return err
		}
		if err := o.obs.SetGeneratedStatus(ctx, d.Collection, d.Item, d.Field, store.GenCleared); err != nil {
			return err
		}
		a.Value = g.Value
		a.Summary = fmt.Sprintf("cleared the generated %s for %q (was %q)", d.Field, d.Item, display(g.Value))
	case store.KindFrontendError:
		if d.Action.Pulled == "" {
			return actionErr("nothing to revert: the front end wasn't pulled")
		}
		if err := o.content.SetFrontendInRotation(ctx, d.Action.Pulled, true); err != nil {
			return err
		}
		o.mu.Lock()
		delete(o.errors, d.Action.Pulled)
		o.mu.Unlock()
		a.Summary = fmt.Sprintf("put %s back in the rotation", d.Action.Pulled)
	case store.KindContentDrift:
		if !d.Action.Activated || d.Action.Previous == "" {
			return actionErr("nothing to revert: the observer didn't activate a rebuilt revision")
		}
		b, _ := o.content.(store.Builder)
		if b == nil {
			return actionErr("no front-end revisions here")
		}
		if err := b.SetActiveRevision(ctx, d.Frontend, d.Action.Previous); err != nil {
			return err
		}
		o.mu.Lock()
		delete(o.probation, d.Frontend)
		o.mu.Unlock()
		a = d.Action
		a.Type, a.Auto, a.Activated, a.ProbationUntil, a.At = "revert", false, false, time.Time{}, o.now()
		a.Summary = fmt.Sprintf("you reverted %s to v%d; the observer's v%d is kept in its history",
			d.Frontend, d.Action.PreviousNumber, d.Action.RevisionNumber)
	default:
		return actionErr("nothing to revert")
	}
	return o.obs.SetDetection(ctx, d.ID, store.StatusReverted, a)
}

// Dismiss closes a detection without changing anything. A dismissed detection
// is not healed again.
func (o *Observer) Dismiss(ctx context.Context, id int64) error {
	d, err := o.obs.Detection(ctx, id)
	if err != nil {
		return err
	}
	return o.obs.SetDetection(ctx, d.ID, store.StatusDismissed, d.Action)
}

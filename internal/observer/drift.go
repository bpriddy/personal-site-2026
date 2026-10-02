package observer

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bpriddy/personal-site-2026/internal/contract"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// Content drift (docs/observer.md, "Content drift"): the content grows (a new
// collection, a newly used field) while a prompted front end in the rotation,
// made before, never shows it. The observer finds that with a static scan of
// the active revision, and rebuilds the front end with the builder agent so
// its creative concept accommodates the new content. A rebuild that provably
// reads the content (the scan again) is activated, then watched for a
// probation period and rolled back if it fails.

// Rebuilder makes a new revision of a prompted front end from a prompt: the
// builder agent, with the normal system prompt and quality brief. The server
// implements it (cmd/server wires it with SetRebuilder).
type Rebuilder interface {
	// Rebuild applies req.Prompt to the revision req.Parent of
	// req.FrontendID (with its conversation as history) and stores the
	// result as a new revision by author "observer", without activating
	// it. ErrBusy means the front end is being edited; try later.
	Rebuild(ctx context.Context, req RebuildRequest) (store.Revision, error)
}

// RebuildRequest is one creative rebuild.
type RebuildRequest struct {
	FrontendID string
	Parent     string // the active revision
	Prompt     string
}

// ErrBusy is a Rebuilder's answer when the front end has a builder run in
// progress (Ben's chat): the rebuild is deferred to the next check.
var ErrBusy = errors.New("observer: the front end is being edited")

// SetRebuilder enables rebuilds (nil disables them).
func (o *Observer) SetRebuilder(r Rebuilder) {
	o.mu.Lock()
	o.rebuilder = r
	o.mu.Unlock()
}

func (o *Observer) getRebuilder() Rebuilder {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.rebuilder
}

// RebuildEnabled reports whether content drift is healed by rebuilds.
func (o *Observer) RebuildEnabled() bool { return o.getRebuilder() != nil }

// ContentChanged tells the observer the content changed (a save, an import):
// a drift check runs shortly after (bursts are coalesced). It never blocks.
func (o *Observer) ContentChanged() {
	o.changes.Add(1)
	select {
	case o.changed <- struct{}{}:
	default:
	}
}

// ContentChanges counts ContentChanged calls (for tests).
func (o *Observer) ContentChanges() int64 { return o.changes.Load() }

// driftLoop runs drift checks: DriftDelay after start, every DriftEvery, and
// DriftDebounce after a content change.
func (o *Observer) driftLoop(ctx context.Context) {
	first := time.NewTimer(o.cfg.DriftDelay)
	defer first.Stop()
	t := time.NewTicker(o.cfg.DriftEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-t.C:
		case <-o.changed:
			select {
			case <-ctx.Done():
				return
			case <-time.After(o.cfg.DriftDebounce):
			}
		}
		if err := o.CheckDrift(ctx); err != nil && ctx.Err() == nil {
			o.log.Error("observer: drift check", "err", err)
		}
	}
}

// driftSample is a content-drift detection's sample: what is missing, for the
// admin and the rebuild prompt.
type driftSample struct {
	Frontend string            `json:"frontend"`
	Title    string            `json:"title"`
	Revision string            `json:"revision,omitempty"` // "" for a built-in
	Number   int               `json:"number,omitempty"`
	Headline string            `json:"headline"`
	Shape    string            `json:"shape"` // contract.Shape fingerprint
	Missing  []driftCollection `json:"missing"`
}

// driftCollection groups what is missing from one collection.
type driftCollection struct {
	Collection string                `json:"collection"`
	Items      int                   `json:"items"`
	Whole      bool                  `json:"whole"`  // the front end doesn't read the collection at all
	Fields     []contract.FieldShape `json:"fields"` // whole: every field with values; else the missing ones
	Example    map[string]any        `json:"example,omitempty"`
}

func (c driftCollection) fieldList() string {
	parts := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		parts[i] = fmt.Sprintf("%s (%d of %d)", f.Name, f.NonEmpty, c.Items)
	}
	return strings.Join(parts, ", ")
}

func fieldNames(fs []contract.FieldShape) string {
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = f.Name
	}
	return joinAnd(names)
}

func joinAnd(l []string) string {
	switch len(l) {
	case 0:
		return ""
	case 1:
		return l[0]
	}
	return strings.Join(l[:len(l)-1], ", ") + " and " + l[len(l)-1]
}

// groupMissing turns a missing set into per-collection groups, in the shape's
// order, with an example item each.
func groupMissing(m []missing, shape contract.Shape, site contract.Site) []driftCollection {
	var out []driftCollection
	for _, c := range shape.Collections {
		g := driftCollection{Collection: c.Name, Items: c.Items}
		for _, x := range m {
			if x.Collection != c.Name {
				continue
			}
			if x.Field == "" {
				g.Whole = true
				g.Fields = slices.Clone(c.Fields)
				break
			}
			g.Fields = append(g.Fields, contract.FieldShape{Name: x.Field, NonEmpty: x.NonEmpty})
		}
		if !g.Whole && len(g.Fields) == 0 {
			continue
		}
		for _, col := range site.Collections() {
			if col.Name == c.Name {
				g.Example = example(col.Items, g)
			}
		}
		out = append(out, g)
	}
	return out
}

// example picks the item with the most of the group's fields filled and
// trims it for a prompt: long text cut, at most two media items.
func example(items []contract.Item, g driftCollection) map[string]any {
	best, bestN := -1, -1
	for i, it := range items {
		n := 0
		for _, f := range g.Fields {
			if contract.NonEmpty(it[f.Name]) {
				n++
			}
		}
		if n > bestN {
			best, bestN = i, n
		}
	}
	if best < 0 {
		return nil
	}
	out := map[string]any{}
	for k, v := range items[best] {
		if strings.HasPrefix(k, "_") || contract.IgnoredFields[k] {
			continue
		}
		switch x := v.(type) {
		case string:
			if utf8.RuneCountInString(x) > 160 {
				x = string([]rune(x)[:157]) + "…"
			}
			v = x
		case []contract.MediaItem:
			if len(x) > 2 {
				v = x[:2]
			}
		}
		out[k] = v
	}
	return out
}

// headline is the detection's one-line description, e.g. `"Layers" v2
// doesn't show projects`.
func headline(title string, number int, groups []driftCollection) string {
	var whole, fields []string
	for _, g := range groups {
		if g.Whole {
			whole = append(whole, g.Collection)
		} else {
			fields = append(fields, g.Collection+"' "+fieldNames(g.Fields))
		}
	}
	who := title
	if number > 0 {
		who = fmt.Sprintf("%s v%d", title, number)
	}
	switch {
	case len(whole) > 0 && len(fields) > 0:
		return fmt.Sprintf("%s doesn't show %s, or %s", who, joinAnd(whole), joinAnd(fields))
	case len(whole) > 0:
		return fmt.Sprintf("%s doesn't show %s", who, joinAnd(whole))
	}
	return fmt.Sprintf("%s doesn't show %s", who, joinAnd(fields))
}

// accessors names the host API calls that read a collection.
var accessors = map[string]string{
	contract.Pages:       "site.pages() / site.page(slug)",
	contract.Experiments: "site.experiments()",
	contract.Projects:    "site.projects() / site.project(slug)",
}

// RebuildPrompt is the builder prompt for a drift rebuild. The content is
// data from the CMS (Ben's own), quoted as JSON.
func rebuildPrompt(groups []driftCollection) string {
	var sb strings.Builder
	sb.WriteString("The site's content has grown since this front end was made, and it doesn't show all of it yet:\n\n")
	for _, g := range groups {
		ex, _ := json.Marshal(g.Example)
		if g.Whole {
			fmt.Fprintf(&sb, "- %s: %d published items, which this front end doesn't show at all. Fields with values: %s. Example item: %s\n",
				g.Collection, g.Items, g.fieldList(), ex)
		} else {
			fmt.Fprintf(&sb, "- %s (%d published items): this front end shows them, but not these fields: %s. Example item: %s\n",
				g.Collection, g.Items, g.fieldList(), ex)
		}
	}
	sb.WriteString("\nUpdate this front end so it presents this new content within its existing creative concept (the same idea, the same visual language), and keep everything that already works. ")
	var apis []string
	work := false
	for _, g := range groups {
		apis = append(apis, accessors[g.Collection])
		work = work || g.Collection == contract.Projects
	}
	fmt.Fprintf(&sb, "Read it through the host API: %s, and site.field(item, name, {expect, fallback, optional}) for every field you display (optional: true for ones that may legitimately be empty, like a link or a film). ",
		strings.Join(apis, ", "))
	if work {
		sb.WriteString(`Projects live on the "work" route (the index) and "work/<slug>" (one project); add Work to the navigation. `)
	}
	sb.WriteString("Present each field listed above where it fits, show media with the paths exactly as given, and open links and films with site.openExternal. Change only what this needs.")
	return sb.String()
}

// CheckDrift runs one content-drift check over the front ends in the
// rotation, and rebuilds (at most one at a time) where it can. Without
// revision files (Config.Files) or a builder store it does nothing.
func (o *Observer) CheckDrift(ctx context.Context) error {
	b, _ := o.content.(store.Builder)
	if b == nil || o.files == nil {
		return nil
	}
	o.driftMu.Lock()
	defer o.driftMu.Unlock()

	site, err := contract.Build(ctx, o.content, o)
	if err != nil {
		return err
	}
	shape := contract.ShapeOf(site)
	fes, err := b.BuilderFrontends(ctx)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, f := range fes {
		if !o.rotating(f) {
			continue
		}
		var miss []missing
		var rev store.Revision
		switch {
		case frontend.IsBuiltin(f.ID):
			miss = builtinDrift(f.ID, shape)
		case f.Kind == store.KindPrompted && f.ActiveRevision != "":
			if rev, err = b.Revision(ctx, f.ActiveRevision); err != nil {
				o.log.Error("observer: drift: revision", "frontend", f.ID, "err", err)
				continue
			}
			files, err := o.files.Read(ctx, rev.ID)
			if err != nil {
				o.log.Error("observer: drift: files", "frontend", f.ID, "err", err)
				continue
			}
			miss = scanDrift(files, shape)
		default:
			continue
		}
		if len(miss) == 0 {
			continue
		}
		fp := fingerprint(miss)
		sig := signature(store.KindContentDrift, f.ID, rev.ID, fp)
		current[sig] = true
		groups := groupMissing(miss, shape, site)
		title := cmp.Or(f.Title, f.ID)
		sample := driftSample{Frontend: f.ID, Title: title, Revision: rev.ID, Number: rev.Number,
			Headline: headline(title, rev.Number, groups), Shape: shape.Fingerprint(), Missing: groups}
		raw, _ := json.Marshal(sample)
		var cols []string
		for _, g := range groups {
			cols = append(cols, g.Collection)
		}
		serve := f.ID
		if rev.ID != "" {
			serve = frontend.RevRef(rev.ID)
		}
		d, created, err := o.obs.RecordDetection(ctx, store.Detection{Kind: store.KindContentDrift,
			Frontend: f.ID, Serve: serve, Collection: strings.Join(cols, ", "), Signature: sig,
			Sample: raw, Status: store.StatusNew})
		if err != nil {
			return err
		}
		if created {
			o.log.Info("observer: content drift", "frontend", f.ID, "missing", sample.Headline)
		}
		if frontend.IsBuiltin(f.ID) {
			if created {
				o.obs.SetDetection(ctx, d.ID, store.StatusNeedsReview, store.Action{Type: "review", At: o.now(),
					Summary: fmt.Sprintf("%s. It's a built-in front end, so its code is in the repo: update it there (or dismiss this if it's deliberate).", sample.Headline)})
			}
			continue
		}
		if d.Status == store.StatusNew {
			o.rebuild(ctx, b, d, f, rev, miss, fp, shape, sample)
		}
	}

	// open drift detections that no longer apply (Ben reprompted it, took it
	// out of the rotation, or the content changed)
	open, err := o.obs.Detections(ctx, 1000, store.StatusNew, store.StatusNeedsReview)
	if err != nil {
		return err
	}
	for _, d := range open {
		if d.Kind == store.KindContentDrift && !current[d.Signature] {
			a := d.Action
			a.Type, a.At = "resolved", o.now()
			a.Summary = "No longer applies (the front end's active revision, its place in the rotation, or the content changed since). Was: " + d.Action.Summary
			o.obs.SetDetection(ctx, d.ID, store.StatusFixed, a)
		}
	}
	return nil
}

// rotating reports whether f is in the rotation (FRONTEND_ROTATION, if set).
func (o *Observer) rotating(f store.FrontendInfo) bool {
	if o.override != nil {
		return o.override.Contains(f.ID)
	}
	return f.InRotation
}

// rebuild heals one content-drift detection on a prompted front end.
func (o *Observer) rebuild(ctx context.Context, b store.Builder, d store.Detection, f store.FrontendInfo,
	rev store.Revision, miss []missing, fp string, shape contract.Shape, sample driftSample) {
	set := func(status string, a store.Action) {
		a.At = o.now()
		if err := o.obs.SetDetection(ctx, d.ID, status, a); err != nil {
			o.log.Error("observer: drift: set detection", "err", err)
		}
	}
	rb := o.getRebuilder()
	if rb == nil {
		if d.Action.Type != "disabled" {
			set(store.StatusNew, store.Action{Type: "disabled",
				Summary: sample.Headline + ". Rebuilding is off (ANTHROPIC_API_KEY unset); reprompt it in the builder."})
		}
		return
	}
	claim, err := o.obs.ClaimRebuild(ctx, store.Rebuild{Frontend: f.ID, Fingerprint: fp, DetectionID: d.ID, Parent: rev.ID},
		o.cfg.RebuildsPerDay)
	switch {
	case errors.Is(err, store.ErrRebuildDone):
		set(store.StatusNeedsReview, store.Action{Type: "review", Previous: rev.ID, PreviousNumber: rev.Number,
			Summary: sample.Headline + ". The observer already tried rebuilding it for this content once, so it needs you: reprompt it in the builder, or dismiss this."})
		return
	case errors.Is(err, store.ErrRebuildBusy), errors.Is(err, store.ErrRebuildQuota):
		why := "another rebuild is running"
		if errors.Is(err, store.ErrRebuildQuota) {
			why = fmt.Sprintf("the daily rebuild budget (%d) is used up", o.cfg.RebuildsPerDay)
		}
		if d.Action.Summary != sample.Headline+"; deferred: "+why+"." {
			set(store.StatusNew, store.Action{Type: "deferred", Summary: sample.Headline + "; deferred: " + why + "."})
		}
		return
	case err != nil:
		o.log.Error("observer: drift: claim", "err", err)
		return
	}

	set(store.StatusNew, store.Action{Type: "rebuilding", Previous: rev.ID, PreviousNumber: rev.Number,
		Summary: sample.Headline + "; rebuilding it now."})
	o.log.Info("observer: rebuilding", "frontend", f.ID, "from", rev.ID)
	rctx, cancel := context.WithTimeout(ctx, o.cfg.RebuildTimeout)
	defer cancel()
	newRev, err := rb.Rebuild(rctx, RebuildRequest{FrontendID: f.ID, Parent: rev.ID, Prompt: rebuildPrompt(sample.Missing)})
	if errors.Is(err, ErrBusy) {
		o.obs.CancelRebuild(ctx, claim.ID)
		set(store.StatusNew, store.Action{Type: "deferred", Summary: sample.Headline + "; deferred: it's being edited in the builder."})
		return
	}
	if err != nil {
		o.log.Error("observer: rebuild", "frontend", f.ID, "err", err)
		o.obs.FinishRebuild(ctx, claim.ID, "", err.Error())
		set(store.StatusNeedsReview, store.Action{Type: "review", Previous: rev.ID, PreviousNumber: rev.Number,
			Summary: fmt.Sprintf("%s. The observer's rebuild failed: %s", sample.Headline, display(err.Error()))})
		return
	}
	if err := o.obs.FinishRebuild(ctx, claim.ID, newRev.ID, ""); err != nil {
		o.log.Error("observer: finish rebuild", "err", err)
	}
	a := store.Action{Revision: newRev.ID, RevisionNumber: newRev.Number, Previous: rev.ID, PreviousNumber: rev.Number}
	moved := fmt.Sprintf("v%d → v%d", rev.Number, newRev.Number)

	// verify: the new revision must read what was missing
	files, err := o.files.Read(ctx, newRev.ID)
	if err != nil {
		a.Type, a.Summary = "review", fmt.Sprintf("Rebuilt %s, but its files can't be read (%v); not activated.", moved, err)
		set(store.StatusNeedsReview, a)
		return
	}
	if left := unresolved(miss, scanDrift(files, shape)); len(left) > 0 {
		keys := make([]string, len(left))
		for i, m := range left {
			keys[i] = m.key()
		}
		a.Type = "review"
		a.Summary = fmt.Sprintf("Rebuilt %s by the observer, but v%d still doesn't read %s, so it wasn't activated. Preview it, and activate it in the builder if it's right.",
			moved, newRev.Number, joinAnd(keys))
		set(store.StatusNeedsReview, a)
		return
	}
	// don't override a revision Ben activated meanwhile
	if cur, err := b.BuilderFrontend(ctx, f.ID); err != nil || cur.ActiveRevision != rev.ID {
		a.Type, a.Summary = "review", fmt.Sprintf("Rebuilt %s by the observer; the active revision changed meanwhile, so it wasn't activated.", moved)
		set(store.StatusNeedsReview, a)
		return
	}
	if err := b.SetActiveRevision(ctx, f.ID, newRev.ID); err != nil {
		a.Type, a.Summary = "review", fmt.Sprintf("Rebuilt %s, but activating it failed: %v", moved, err)
		set(store.StatusNeedsReview, a)
		return
	}
	a.Type, a.Auto, a.Activated = "rebuild", true, true
	a.ProbationUntil = o.now().Add(o.cfg.Probation)
	a.Summary = fmt.Sprintf("Rebuilt %s by the observer and activated it. %s", moved, strings.TrimSpace(newRev.Summary))
	set(store.StatusFixed, a)
	o.mu.Lock()
	o.probation[f.ID] = &probation{detection: d.ID, revision: newRev.ID, previous: rev.ID, until: a.ProbationUntil}
	o.mu.Unlock()
	o.log.Info("observer: rebuilt and activated", "frontend", f.ID, "from", rev.ID, "to", newRev.ID)
}

// probation is the watch on a front end the observer just activated.
type probation struct {
	detection          int64
	revision, previous string
	until              time.Time
	errors, notReady   int
}

// probationFor returns fe's probation, if it is on one: from memory, or
// (another instance activated it) from its detection.
func (o *Observer) probationFor(ctx context.Context, fe string) *probation {
	now := o.now()
	o.mu.Lock()
	p := o.probation[fe]
	if p != nil && !now.Before(p.until) {
		delete(o.probation, fe)
		p = nil
	}
	o.mu.Unlock()
	if p != nil {
		return p
	}
	dets, err := o.obs.Detections(ctx, 200, store.StatusFixed)
	if err != nil {
		return nil
	}
	for _, d := range dets {
		a := d.Action
		if d.Kind == store.KindContentDrift && d.Frontend == fe && a.Activated && now.Before(a.ProbationUntil) {
			o.mu.Lock()
			defer o.mu.Unlock()
			if p = o.probation[fe]; p == nil {
				p = &probation{detection: d.ID, revision: a.Revision, previous: a.Previous, until: a.ProbationUntil}
				o.probation[fe] = p
			}
			return p
		}
	}
	return nil
}

// onProbation handles a front-end error during a probation: it counts it and
// rolls the front end back to its previous revision past the limits. It
// reports whether the front end is on probation; if so, the pull-from-
// rotation rule doesn't apply (rolling back is the gentler fix).
func (o *Observer) onProbation(ctx context.Context, d store.Detection, r Report) bool {
	p := o.probationFor(ctx, d.Frontend)
	if p == nil {
		return false
	}
	if r.Serve != "" && r.Serve != frontend.RevRef(p.revision) {
		return true // an older revision still open in someone's tab
	}
	o.mu.Lock()
	p.errors++
	if isNotReady(r) {
		p.notReady++
	}
	roll := p.errors >= o.cfg.ProbationErrors || p.notReady >= o.cfg.ProbationNotReady
	if roll {
		delete(o.probation, d.Frontend)
		delete(o.errors, d.Frontend)
	}
	n, nr := p.errors, p.notReady
	o.mu.Unlock()
	if !roll {
		return true
	}
	b, _ := o.content.(store.Builder)
	if b == nil {
		return true
	}
	if err := b.SetActiveRevision(ctx, d.Frontend, p.previous); err != nil {
		o.log.Error("observer: roll back", "frontend", d.Frontend, "err", err)
		return true
	}
	why := fmt.Sprintf("%d errors", n)
	if nr >= o.cfg.ProbationNotReady {
		why = fmt.Sprintf("%d reports of never getting ready", nr)
	}
	o.log.Warn("observer: rolled back a rebuilt front end", "frontend", d.Frontend, "to", p.previous, "why", why)
	det, err := o.obs.Detection(ctx, p.detection)
	if err != nil {
		return true
	}
	a := det.Action
	a.Type, a.Auto, a.Activated, a.ProbationUntil, a.At = "rollback", true, false, time.Time{}, o.now()
	a.Summary = fmt.Sprintf("Rolled back v%d → v%d after %s during its probation (last error: %s). The rebuilt v%d is kept: preview it, fix it in the builder, or dismiss this.",
		a.RevisionNumber, a.PreviousNumber, why, display(cmp.Or(r.Message, "(no message)")), a.RevisionNumber)
	o.obs.SetDetection(ctx, det.ID, store.StatusNeedsReview, a)
	return true
}

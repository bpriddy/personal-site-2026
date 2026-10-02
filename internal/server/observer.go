package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/contract"
	"github.com/bpriddy/personal-site-2026/internal/observer"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// The site observer: ingests detections (content gaps, type breaks, front-end
// failures), heals them, and reports them in the admin (docs/observer.md;
// docs/frontend-protocol.md, "Observer"). The work is in internal/observer;
// this file is its HTTP surface.

// observerState holds the observer's dependencies (set by an Option).
type observerState struct {
	o *observer.Observer
}

// WithObserver enables the observer. Its workers must be started separately
// (observer.Start), so tests can drive it synchronously.
func WithObserver(o *observer.Observer) Option {
	return func(s *Server) error {
		s.observer.o = o
		return nil
	}
}

// observerRoutes registers public routes (POST /api/observe) on mux and admin
// routes under /admin/observer/.
func (s *Server) observerRoutes(mux, admin *http.ServeMux) {
	if o := s.observer.o; o != nil {
		mux.Handle("POST /api/observe", o.IngestHandler(s.cfg.MainOrigin))
	} else {
		mux.HandleFunc("POST /api/observe", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	}
	admin.HandleFunc("GET /admin/observer/{$}", s.adminObserver)
	admin.HandleFunc("GET /admin/observer/unseen.json", s.adminObserverUnseen)
	admin.HandleFunc("POST /admin/observer/{id}/{action}", s.adminObserverAction)
}

// contentGenerated returns the observer's generated-field source for the
// content contract, or nil when the observer is not configured.
func (s *Server) contentGenerated() contract.Generated {
	if s.observer.o == nil {
		return nil
	}
	return s.observer.o
}

// observer tabs: which statuses each shows
var observerTabs = []struct {
	Key, Label string
	Statuses   []string
}{
	{"review", "New / needs review", []string{store.StatusNew, store.StatusNeedsReview}},
	{"fixed", "Fixed", []string{store.StatusFixed}},
	{"closed", "Dismissed / reverted", []string{store.StatusDismissed, store.StatusReverted}},
}

type observerTab struct {
	Key, Label string
	Count      int
	Active     bool
}

type observerRow struct {
	store.Detection
	New       bool                  // not seen before this view
	Generated *store.GeneratedField // the field's generated value, if any
}

// Message is the error message from a front-end error's latest report.
func (r observerRow) Message() string {
	var rep struct{ Message, Stack string }
	json.Unmarshal(r.Sample, &rep)
	return rep.Message
}

// Stack is the stack trace from a front-end error's latest report.
func (r observerRow) Stack() string {
	var rep struct{ Stack string }
	json.Unmarshal(r.Sample, &rep)
	return rep.Stack
}

// driftView is a content-drift detection's sample, for the admin.
type driftView struct {
	Title    string `json:"title"`
	Number   int    `json:"number"`
	Headline string `json:"headline"`
	Missing  []struct {
		Collection string `json:"collection"`
		Items      int    `json:"items"`
		Whole      bool   `json:"whole"`
		Fields     []struct {
			Name     string `json:"name"`
			NonEmpty int    `json:"nonEmpty"`
		} `json:"fields"`
	} `json:"missing"`
}

// Drift is a content-drift detection's details.
func (r observerRow) Drift() driftView {
	var v driftView
	json.Unmarshal(r.Sample, &v)
	return v
}

// RevisionLink opens a revision of the detection's front end in the builder.
func (r observerRow) RevisionLink(rev string) string {
	slug, ok := strings.CutPrefix(r.Frontend, "fe/")
	if !ok || rev == "" {
		return ""
	}
	return "/admin/builder/fe/" + url.PathEscape(slug) + "?" + url.Values{"rev": {rev}}.Encode()
}

// EditLink is the content form for a content-invalid detection's item.
func (r observerRow) EditLink() string {
	switch r.Collection {
	case "pages":
		return "/admin/pages/edit?" + url.Values{"slug": {r.Item}}.Encode()
	case "experiments":
		if r.Item != "" {
			return "/admin/experiments/" + url.PathEscape(r.Item)
		}
	}
	return ""
}

func (s *Server) adminObserver(w http.ResponseWriter, r *http.Request) {
	s.renderObserver(w, r, http.StatusOK, "")
}

func (s *Server) renderObserver(w http.ResponseWriter, r *http.Request, status int, problem string) {
	data := map[string]any{"Configured": s.observer.o != nil, "Problem": problem, "Done": r.URL.Query().Get("done")}
	o := s.observer.o
	if o == nil {
		s.render(w, "admin/observer.html", status, data)
		return
	}
	ctx := r.Context()
	st := o.Store()
	counts, err := st.DetectionCounts(ctx)
	if err != nil {
		s.fail(w, "observer counts", err)
		return
	}
	key := r.URL.Query().Get("tab")
	var tabs []observerTab
	var statuses []string
	for i, t := range observerTabs {
		active := t.Key == key || (i == 0 && !validTab(key))
		n := 0
		for _, st := range t.Statuses {
			n += counts[st]
		}
		tabs = append(tabs, observerTab{Key: t.Key, Label: t.Label, Count: n, Active: active})
		if active {
			statuses, key = t.Statuses, t.Key
		}
	}
	dets, err := st.Detections(ctx, 200, statuses...)
	if err != nil {
		s.fail(w, "observer detections", err)
		return
	}
	gens, err := st.GeneratedFields(ctx)
	if err != nil {
		s.fail(w, "observer generated", err)
		return
	}
	byField := map[[3]string]*store.GeneratedField{}
	for i := range gens {
		g := &gens[i]
		byField[[3]string{g.Collection, g.Item, g.Field}] = g
	}
	rows := make([]observerRow, 0, len(dets))
	var unseen []int64
	for _, d := range dets {
		row := observerRow{Detection: d, New: d.SeenAt == nil}
		if d.Field != "" && d.Kind != store.KindContentInvalid {
			row.Generated = byField[[3]string{d.Collection, d.Item, d.Field}]
		}
		if row.New {
			unseen = append(unseen, d.ID)
		}
		rows = append(rows, row)
	}
	// viewing a tab marks its rows seen; this render still shows their dots
	if err := st.MarkDetectionsSeen(ctx, unseen...); err != nil {
		s.log.Error("observer: mark seen", "err", err)
	}
	data["Tabs"], data["Tab"], data["Rows"], data["Generated"] = tabs, key, rows, gens
	data["Healing"] = o.HealingEnabled()
	data["Rebuilding"] = o.RebuildEnabled()
	s.render(w, "admin/observer.html", status, data)
}

func validTab(key string) bool {
	for _, t := range observerTabs {
		if t.Key == key {
			return true
		}
	}
	return false
}

func (s *Server) adminObserverUnseen(w http.ResponseWriter, r *http.Request) {
	n := 0
	if o := s.observer.o; o != nil {
		var err error
		if n, err = o.Store().UnseenDetections(r.Context()); err != nil {
			s.fail(w, "observer unseen", err)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]int{"unseen": n})
}

func (s *Server) adminObserverAction(w http.ResponseWriter, r *http.Request) {
	o := s.observer.o
	if o == nil {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	action := r.PathValue("action")
	switch action {
	case "accept":
		err = o.Accept(ctx, id)
	case "edit":
		err = o.Edit(ctx, id, r.FormValue("value"))
	case "regenerate":
		err = o.Regenerate(ctx, id)
	case "revert":
		err = o.Revert(ctx, id)
	case "dismiss":
		err = o.Dismiss(ctx, id)
	default:
		http.NotFound(w, r)
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, observer.ErrAction):
		s.renderObserver(w, r, http.StatusConflict, err.Error())
		return
	case err != nil && action == "regenerate":
		s.log.Error("observer regenerate", "err", err)
		s.renderObserver(w, r, http.StatusBadGateway, "Generation failed; try again later.")
		return
	case err != nil:
		s.fail(w, "observer "+action, err)
		return
	}
	tab := r.FormValue("tab")
	if !validTab(tab) {
		tab = "review"
	}
	http.Redirect(w, r, "/admin/observer/?"+url.Values{"tab": {tab}, "done": {action}}.Encode(), http.StatusSeeOther)
}

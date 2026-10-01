package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// The public builder (docs/frontend-protocol.md, "v1.2 additions"): any
// visitor can prompt a front end of their own. It runs on the admin builder's
// engine (runChat, saveRevision), but every front end here belongs to the
// visitor session that made it: only that session can open, chat with,
// preview, view live or submit it; anyone else gets a 404. Everything a
// visitor sends is untrusted.

// liveCookie names the front end a visitor is viewing live (their own draft).
const liveCookie = "fe_live"

// reservedBuildSlugs can't be visitor slugs: they are /build/<word> routes.
var reservedBuildSlugs = map[string]bool{"new": true, "preview": true}

// buildRoutes registers the public builder. POSTs are behind
// CrossOriginProtection; every route gets a visitor session.
func (s *Server) buildRoutes(mux *http.ServeMux) {
	cop := http.NewCrossOriginProtection()
	post := func(h http.HandlerFunc) http.Handler { return cop.Handler(s.withSession(h)) }
	mux.HandleFunc("GET /build", s.withSession(s.buildIndex))
	mux.HandleFunc("GET /build/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/build", http.StatusMovedPermanently)
	})
	mux.Handle("POST /build/new", post(s.buildNew))
	mux.HandleFunc("GET /build/preview", s.withSession(s.buildPreview))
	mux.HandleFunc("GET /build/{slug}", s.withSession(s.buildFrontend))
	mux.Handle("POST /build/{slug}/chat", post(s.buildChat))
	mux.Handle("POST /build/{slug}/live", post(s.buildLive))
	mux.Handle("POST /build/{slug}/submit", post(s.buildSubmit))
}

// visitorStore is the store's public-builder side, or nil.
func (s *Server) visitorStore() store.Visitors {
	v, _ := s.store.(store.Visitors)
	if s.builderStore() == nil {
		return nil
	}
	return v
}

// buildDisabled reports whether visitors can't start runs (no model, or a
// store without the public builder).
func (s *Server) buildDisabled() bool {
	return s.builder.agent == nil || s.visitorStore() == nil
}

// renderBuild renders a public-builder page: its own layout, under the public
// CSP, never cached or indexed (the pages are private to a session).
func (s *Server) renderBuild(w http.ResponseWriter, name string, status int, data map[string]any) {
	h := w.Header()
	h.Set("Content-Security-Policy", s.publicCSP)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Referrer-Policy", "no-referrer")
	data["MaxPrompt"] = s.limits.MaxPrompt
	s.render(w, "build/"+name, status, data)
}

// ownedFrontend returns the request session's front end for {slug}, or
// answers 404 (for missing and other people's front ends alike).
func (s *Server) ownedFrontend(w http.ResponseWriter, r *http.Request) (store.FrontendInfo, bool) {
	v := s.visitorStore()
	slug := r.PathValue("slug")
	if v == nil || !frontend.ValidSlug(slug) {
		s.buildNotFound(w)
		return store.FrontendInfo{}, false
	}
	f, err := v.OwnedFrontend(r.Context(), frontend.PromptedID(slug), readSession(r))
	if errors.Is(err, store.ErrNotFound) || (err == nil && f.Kind != store.KindPrompted) {
		s.buildNotFound(w)
		return store.FrontendInfo{}, false
	} else if err != nil {
		s.fail(w, "build: frontend", err)
		return store.FrontendInfo{}, false
	}
	return f, true
}

func (s *Server) buildNotFound(w http.ResponseWriter) {
	s.renderBuild(w, "notfound.html", http.StatusNotFound, map[string]any{})
}

// visitorStatus is a front end's review status, in the visitor's words.
type visitorStatus struct {
	Key   string // "draft", "pending", "approved", "rejected"
	Label string
	Note  string
	Rev   string // the submitted revision
	RevN  int
}

func (s *Server) visitorStatusOf(r *http.Request, f store.FrontendInfo) (visitorStatus, error) {
	sub, err := s.visitorStore().LatestSubmission(r.Context(), f.ID)
	if errors.Is(err, store.ErrNotFound) {
		return visitorStatus{Key: "draft", Label: "Draft", Note: "Only you can see it."}, nil
	} else if err != nil {
		return visitorStatus{}, err
	}
	st := visitorStatus{Key: sub.Status, Rev: sub.RevisionID, RevN: sub.RevisionNumber}
	switch sub.Status {
	case store.SubmissionPending:
		st.Label, st.Note = "Waiting for review", "You submitted version "+strconv.Itoa(sub.RevisionNumber)+". Ben will take a look."
	case store.SubmissionApproved:
		st.Label, st.Note = "Approved", "Ben approved version "+strconv.Itoa(sub.RevisionNumber)+": it's now one of the looks visitors see on the site."
	case store.SubmissionRejected:
		st.Label, st.Note = "Not this time", "Ben didn't add version "+strconv.Itoa(sub.RevisionNumber)+" to the site. You can keep changing it and submit again."
	}
	return st, nil
}

type buildRow struct {
	store.FrontendInfo
	Slug      string
	Revisions int
	Status    visitorStatus
}

// buildIndex is /build: the prompt box and this session's front ends.
func (s *Server) buildIndex(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Disabled": s.buildDisabled(), "Message": buildMessage(r.URL.Query().Get("msg"), s.limits)}
	if data["Disabled"] == true {
		data["DisabledText"] = buildMessage(msgDisabled, s.limits)
	}
	v := s.visitorStore()
	if v != nil {
		ctx := r.Context()
		fes, err := v.SessionFrontends(ctx, readSession(r))
		if err != nil {
			s.fail(w, "build: session frontends", err)
			return
		}
		var rows []buildRow
		for _, f := range fes {
			revs, err := s.builderStore().Revisions(ctx, f.ID)
			if err != nil {
				s.fail(w, "build: revisions", err)
				return
			}
			st, err := s.visitorStatusOf(r, f)
			if err != nil {
				s.fail(w, "build: status", err)
				return
			}
			rows = append(rows, buildRow{FrontendInfo: f, Slug: strings.TrimPrefix(f.ID, "fe/"), Revisions: len(revs), Status: st})
		}
		data["Frontends"] = rows
		if !s.buildDisabled() {
			// say up front when today's builds are used up
			c, err := v.VisitorRunCounts(ctx, readSession(r), s.clientIPHash(r), s.runQuota())
			if err == nil {
				if qerr := s.runQuota().Check(c); qerr != nil && data["Message"] == "" {
					var qe *store.QuotaError
					if errors.As(qerr, &qe) && qe.Limit != store.LimitConcurrent {
						data["Message"] = buildMessage(qe.Limit, s.limits)
					}
				}
			}
		}
	}
	s.renderBuild(w, "index.html", http.StatusOK, data)
}

// buildNew creates a front end from a prompt and opens it; its page sends
// the prompt on arrival (#start=, like the admin's prompt-first create).
func (s *Server) buildNew(w http.ResponseWriter, r *http.Request) {
	back := func(key string) { http.Redirect(w, r, "/build?msg="+key, http.StatusSeeOther) }
	v := s.visitorStore()
	if s.buildDisabled() {
		back(msgDisabled)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		back(msgTooLong)
		return
	}
	prompt := strings.TrimSpace(r.PostForm.Get("prompt"))
	title := strings.Join(strings.Fields(r.PostForm.Get("title")), " ")
	switch {
	case prompt == "":
		back(msgEmpty)
		return
	case utf8.RuneCountInString(prompt) > s.limits.MaxPrompt:
		back(msgTooLong)
		return
	}
	if utf8.RuneCountInString(title) > 80 || !utf8.ValidString(title) {
		title = ""
	}
	if title == "" {
		title = titleFromPrompt(prompt)
	}
	ctx := r.Context()
	session, ip := readSession(r), s.clientIPHash(r)
	// don't make a front end that can't be built today
	c, err := v.VisitorRunCounts(ctx, session, ip, s.runQuota())
	if err != nil {
		s.fail(w, "build: counts", err)
		return
	}
	var qe *store.QuotaError
	if errors.As(s.runQuota().Check(c), &qe) && qe.Limit != store.LimitConcurrent {
		back(qe.Limit)
		return
	}
	if !s.newLimiter.allow(string(ip), s.limits.NewPerIPWindow, s.limits.NewWindow, s.now()) {
		back(msgSlowNew)
		return
	}
	// a free slug in the shared namespace; never an existing front end
	var slug string
	for range 5 {
		slug = s.freeSlug(r, slugify(title))
		err = v.CreateVisitorFrontend(ctx, frontend.PromptedID(slug), title, session)
		if !errors.Is(err, store.ErrExists) {
			break
		}
	}
	if err != nil {
		s.fail(w, "build: create", err)
		return
	}
	http.Redirect(w, r, "/build/"+slug+"#start="+url.QueryEscape(prompt), http.StatusSeeOther)
}

// buildFrontend is a visitor's front-end page: chat, preview, versions, View
// live and Submit. Owner only.
func (s *Server) buildFrontend(w http.ResponseWriter, r *http.Request) {
	f, ok := s.ownedFrontend(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	b := s.builderStore()
	revs, err := b.Revisions(ctx, f.ID)
	if err != nil {
		s.fail(w, "build: revisions", err)
		return
	}
	// selected: ?rev=, else the newest
	sel := r.URL.Query().Get("rev")
	if !slices.ContainsFunc(revs, func(x store.Revision) bool { return x.ID == sel }) {
		sel = ""
		if len(revs) > 0 {
			sel = revs[0].ID
		}
	}
	numbers := map[string]int{}
	for _, rv := range revs {
		numbers[rv.ID] = rv.Number
	}
	var rows []revisionRow
	var selected *revisionRow
	for _, rv := range revs {
		rows = append(rows, revisionRow{Revision: rv, Active: rv.ID == f.ActiveRevision, Selected: rv.ID == sel, ParentNumber: numbers[rv.ParentID]})
	}
	for i := range rows {
		if rows[i].Selected {
			selected = &rows[i]
		}
	}
	var conv []builder.Turn
	previewRef := ""
	if selected != nil {
		conv = builder.ParseConversation(selected.Conversation)
		previewRef = frontend.RevRef(selected.ID)
	}
	st, err := s.visitorStatusOf(r, f)
	if err != nil {
		s.fail(w, "build: status", err)
		return
	}
	s.builder.mu.Lock()
	running := s.builder.running[f.ID]
	s.builder.mu.Unlock()
	live := false
	if c, err := r.Cookie(liveCookie); err == nil && c.Value == f.ID {
		live = true
	}
	data := map[string]any{
		"FE": f, "Slug": strings.TrimPrefix(f.ID, "fe/"), "Revisions": rows, "Selected": selected,
		"Conversation": conv, "PreviewRef": previewRef, "Running": running, "Status": st, "Live": live,
		"Disabled": s.buildDisabled(), "DisabledText": buildMessage(msgDisabled, s.limits),
		"Message": buildMessage(r.URL.Query().Get("msg"), s.limits),
	}
	s.renderBuild(w, "frontend.html", http.StatusOK, data)
}

// buildChat runs a visitor's chat turn with the limits applied (runChat).
func (s *Server) buildChat(w http.ResponseWriter, r *http.Request) {
	f, ok := s.ownedFrontend(w, r)
	if !ok {
		return
	}
	if s.buildDisabled() {
		http.Error(w, buildMessage(msgDisabled, s.limits), http.StatusServiceUnavailable)
		return
	}
	s.runChat(w, r, f, chatCaller{author: "visitor", visitor: true, session: readSession(r), ipHash: s.clientIPHash(r)})
}

// buildPreview mints a preview URL for one of the session's own revisions.
func (s *Server) buildPreview(w http.ResponseWriter, r *http.Request) {
	v := s.visitorStore()
	rev := frontend.RevisionOf(r.URL.Query().Get("ref"))
	if v == nil || rev == "" {
		http.NotFound(w, r)
		return
	}
	rv, err := s.builderStore().Revision(r.Context(), rev)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "build: revision", err)
		return
	}
	if _, err := v.OwnedFrontend(r.Context(), rv.FrontendID, readSession(r)); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "build: owner", err)
		return
	}
	ref := frontend.RevRef(rv.ID)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ref": ref, "url": s.signedIndexURL(ref)})
}

// buildLive turns View live on (on=1: the fe_live cookie names this front
// end, and the visitor lands on the site) or off (on=0).
func (s *Server) buildLive(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if r.FormValue("on") != "1" {
		// leaving is always allowed: it only clears this browser's cookie
		s.clearLive(w)
		dest := "/build"
		if v := s.visitorStore(); v != nil {
			if f, err := v.OwnedFrontend(r.Context(), frontend.PromptedID(r.PathValue("slug")), readSession(r)); err == nil {
				dest = "/build/" + strings.TrimPrefix(f.ID, "fe/")
			}
		}
		http.Redirect(w, r, dest, http.StatusSeeOther)
		return
	}
	f, ok := s.ownedFrontend(w, r)
	if !ok {
		return
	}
	revs, err := s.builderStore().Revisions(r.Context(), f.ID)
	if err != nil {
		s.fail(w, "build: revisions", err)
		return
	}
	if len(revs) == 0 {
		http.Redirect(w, r, "/build/"+strings.TrimPrefix(f.ID, "fe/")+"?msg="+msgNoRevision, http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: liveCookie, Value: f.ID, Path: "/", HttpOnly: true, Secure: !s.cfg.Dev(), SameSite: http.SameSiteLaxMode,
		// no MaxAge: a session cookie
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) clearLive(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: liveCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: !s.cfg.Dev(), SameSite: http.SameSiteLaxMode})
}

// buildSubmit sends one of the front end's revisions to Ben's review.
func (s *Server) buildSubmit(w http.ResponseWriter, r *http.Request) {
	f, ok := s.ownedFrontend(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	rev := r.FormValue("rev")
	if !frontend.ValidRevisionID(rev) {
		s.buildNotFound(w)
		return
	}
	_, err := s.visitorStore().Submit(r.Context(), f.ID, rev)
	if errors.Is(err, store.ErrNotFound) {
		s.buildNotFound(w)
		return
	} else if err != nil {
		s.fail(w, "build: submit", err)
		return
	}
	http.Redirect(w, r, "/build/"+strings.TrimPrefix(f.ID, "fe/")+"?"+url.Values{"rev": {rev}, "msg": {msgSubmitted}}.Encode(), http.StatusSeeOther)
}

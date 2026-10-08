package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/notify"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// The public builder (docs/frontend-protocol.md, "v1.2 additions"): any
// visitor can prompt a front end of their own. Building happens on the site
// itself: a modal drawn by the public shell (web/static/build-modal.js) talks
// to the JSON endpoints below, and the only preview is the live site, which
// shows the revision the visitor picked (the fe_live cookie, /api/frontend).
//
// It runs on the admin builder's engine (runChat, saveRevision), but every
// front end here belongs to the visitor session that made it: only that
// session can list, chat with, view live or submit it; anyone else gets a
// 404. Everything a visitor sends is untrusted.

// liveCookie names what a visitor is viewing live (their own draft):
// "<front-end id>:<revision id>", e.g. "fe/night-sky:k3j9x0a1b2".
const liveCookie = "fe_live"

// reservedBuildSlugs can't be visitor slugs: they are (or were) /build/<word>
// routes.
var reservedBuildSlugs = map[string]bool{"new": true, "preview": true, "api": true}

// buildExitPath clears fe_live (the draft banner's and the modal's Exit).
const buildExitPath = "/build/api/exit"

// buildRoutes registers the public builder's API. POSTs are behind
// CrossOriginProtection; the session routes get a visitor session.
func (s *Server) buildRoutes(mux *http.ServeMux) {
	cop := http.NewCrossOriginProtection()
	post := func(h http.HandlerFunc) http.Handler { return cop.Handler(s.withSession(h)) }
	// the builder used to be pages; now it's a modal on the site, and the old
	// links open it (build-modal.js reads ?build=1 and cleans the URL)
	toModal := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/?build=1", http.StatusFound)
	}
	mux.HandleFunc("GET /build", toModal)
	mux.HandleFunc("GET /build/{$}", toModal)
	mux.HandleFunc("GET /build/{slug}", toModal)

	mux.HandleFunc("GET /build/api/frontends", s.withSession(s.buildList))
	mux.Handle("POST /build/api/new", post(s.buildNew))
	mux.Handle("POST /build/api/fe/{slug}/chat", post(s.buildChat))
	mux.Handle("POST /build/api/fe/{slug}/live", post(s.buildLive))
	mux.Handle("POST /build/api/fe/{slug}/submit", post(s.buildSubmit))
	mux.Handle("POST /build/api/fe/{slug}/cancel", post(s.buildCancel))
	mux.Handle("POST "+buildExitPath, cop.Handler(http.HandlerFunc(s.buildExit)))
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

// writeJSON answers with v as JSON, never cached (it's private to a session).
func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// buildError answers {"error": <friendly message>, "key": key}.
func (s *Server) buildError(w http.ResponseWriter, status int, key string) {
	writeJSON(w, status, map[string]string{"error": buildMessage(key, s.limits), "key": key})
}

func buildNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found."})
}

// readJSON decodes a small JSON request body into v.
func readJSON(w http.ResponseWriter, r *http.Request, max int64, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	return json.NewDecoder(r.Body).Decode(v)
}

// ownedFrontend returns the request session's front end for {slug}, or
// answers 404 (for missing and other people's front ends alike).
func (s *Server) ownedFrontend(w http.ResponseWriter, r *http.Request) (store.FrontendInfo, bool) {
	v := s.visitorStore()
	slug := r.PathValue("slug")
	if v == nil || !frontend.ValidSlug(slug) {
		buildNotFound(w)
		return store.FrontendInfo{}, false
	}
	f, err := v.OwnedFrontend(r.Context(), frontend.PromptedID(slug), readSession(r))
	if errors.Is(err, store.ErrNotFound) || (err == nil && f.Kind != store.KindPrompted) {
		buildNotFound(w)
		return store.FrontendInfo{}, false
	} else if err != nil {
		s.fail(w, "build: frontend", err)
		return store.FrontendInfo{}, false
	}
	return f, true
}

// visitorStatus is a front end's review status, in the visitor's words.
type visitorStatus struct {
	Key   string `json:"key"` // "draft", "pending", "approved", "rejected"
	Label string `json:"label"`
	Note  string `json:"note"`
	Rev   string `json:"revision,omitempty"` // the submitted revision
	RevN  int    `json:"number,omitempty"`
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

// The modal's view of the session's work (GET /build/api/frontends).
type (
	buildState struct {
		Enabled bool   `json:"enabled"`          // new runs can start
		Notice  string `json:"notice,omitempty"` // why not, or a limit already reached
		// Paused (v1.12): building is paused for the budget (enabled is
		// false); the modal draws its own state for it, so Notice is empty
		Paused    *buildPause `json:"paused"`
		MaxPrompt int         `json:"maxPrompt"`
		Live      *buildLive  `json:"live"` // what the site shows this session, if a draft
		// Notify (v1.13): the modal can offer to email the visitor when a build ends
		Notify    bool            `json:"notify"`
		Frontends []buildFrontend `json:"frontends"`
	}
	buildLive struct {
		Frontend string `json:"frontend"`
		Slug     string `json:"slug"`
		Revision string `json:"revision"`
		Number   int    `json:"number"`
	}
	buildFrontend struct {
		ID        string          `json:"id"`
		Slug      string          `json:"slug"`
		Title     string          `json:"title"`
		Credit    string          `json:"credit"` // how the visitor asked to be credited
		UpdatedAt time.Time       `json:"updatedAt"`
		Running   bool            `json:"running"`       // a run is in progress
		Run       *buildRun       `json:"run,omitempty"` // the run in progress (v1.11)
		Status    visitorStatus   `json:"status"`
		Revisions []buildRevision `json:"revisions"` // newest first
	}
	buildRun struct {
		Prompt    string    `json:"prompt"`
		StartedAt time.Time `json:"startedAt"`
		Notify    string    `json:"notify,omitempty"` // v1.13: the masked address it'll email, if asked
	}
	buildRevision struct {
		ID           string    `json:"id"`
		Number       int       `json:"number"`
		ParentNumber int       `json:"parentNumber,omitempty"`
		Prompt       string    `json:"prompt"` // what the visitor asked for
		Summary      string    `json:"summary"`
		CreatedAt    time.Time `json:"createdAt"`
	}
)

// buildList is the session's front ends with their revisions and statuses,
// what the site is showing them, and whether they can build right now.
func (s *Server) buildList(w http.ResponseWriter, r *http.Request) {
	out := buildState{Enabled: !s.buildDisabled(), MaxPrompt: s.limits.MaxPrompt, Frontends: []buildFrontend{}, Notify: s.notifyStore() != nil}
	v := s.visitorStore()
	if v == nil {
		out.Notice = buildMessage(msgDisabled, s.limits)
		writeJSON(w, http.StatusOK, out)
		return
	}
	ctx := r.Context()
	session := readSession(r)
	fes, err := v.SessionFrontends(ctx, session)
	if err != nil {
		s.fail(w, "build: session frontends", err)
		return
	}
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
		s.builder.mu.Lock()
		running := s.builder.running[f.ID]
		s.builder.mu.Unlock()
		// the store knows about runs on every instance (a page reload may land
		// on another one): the newest run, if it's still running and young
		// enough to be alive
		var run *buildRun
		if runs, err := s.builderStore().Runs(ctx, f.ID, 1); err == nil && len(runs) == 1 &&
			runs[0].Status == store.RunRunning && s.now().Sub(runs[0].StartedAt) < RunTimeout+time.Minute {
			running = true
			run = &buildRun{Prompt: runs[0].Prompt, StartedAt: runs[0].StartedAt}
			if ns := s.notifyStore(); ns != nil {
				if n, err := ns.RunNotify(ctx, runs[0].ID); err == nil {
					run.Notify = notify.Mask(n.Email)
				}
			}
		}
		numbers := map[string]int{}
		for _, rv := range revs {
			numbers[rv.ID] = rv.Number
		}
		row := buildFrontend{ID: f.ID, Slug: strings.TrimPrefix(f.ID, "fe/"), Title: f.Title, Credit: f.CreditRequested, UpdatedAt: f.UpdatedAt,
			Running: running, Run: run, Status: st, Revisions: []buildRevision{}}
		for _, rv := range revs {
			row.Revisions = append(row.Revisions, buildRevision{ID: rv.ID, Number: rv.Number, ParentNumber: numbers[rv.ParentID],
				Prompt: lastPrompt(rv.Conversation), Summary: rv.Summary, CreatedAt: rv.CreatedAt})
		}
		out.Frontends = append(out.Frontends, row)
	}
	if f, rv, err := s.liveSelection(r); err == nil {
		out.Live = &buildLive{Frontend: f.ID, Slug: strings.TrimPrefix(f.ID, "fe/"), Revision: rv.ID, Number: rv.Number}
	}
	if !out.Enabled {
		out.Notice = buildMessage(msgDisabled, s.limits)
	} else if p := s.visitorPause(r, false); p != nil {
		out.Enabled, out.Paused = false, p
	} else if c, err := v.VisitorRunCounts(ctx, session, s.clientIPHash(r), s.runQuota()); err == nil {
		// say up front when today's builds are used up
		var qe *store.QuotaError
		if errors.As(s.runQuota().Check(c), &qe) && qe.Limit != store.LimitConcurrent {
			out.Notice = buildMessage(qe.Limit, s.limits)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// lastPrompt is the visitor's last prompt in a revision's conversation.
func lastPrompt(conv json.RawMessage) string {
	turns := builder.ParseConversation(conv)
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "user" {
			return turns[i].Text
		}
	}
	return ""
}

// buildNew creates a front end from a prompt: JSON {"prompt", "title"?} →
// 201 {"id", "slug", "title"}. The modal then sends the prompt to its chat
// (prompt-first, like the admin's create). Refusals are {"error"} with the
// friendly message.
func (s *Server) buildNew(w http.ResponseWriter, r *http.Request) {
	v := s.visitorStore()
	if s.buildDisabled() {
		s.buildError(w, http.StatusServiceUnavailable, msgDisabled)
		return
	}
	var in struct {
		Prompt string `json:"prompt"`
		Title  string `json:"title"`
	}
	if err := readJSON(w, r, 64<<10, &in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.buildError(w, http.StatusBadRequest, msgTooLong)
		} else {
			s.buildError(w, http.StatusBadRequest, msgEmpty)
		}
		return
	}
	prompt := strings.TrimSpace(in.Prompt)
	title := strings.Join(strings.Fields(in.Title), " ")
	switch {
	case prompt == "":
		s.buildError(w, http.StatusBadRequest, msgEmpty)
		return
	case utf8.RuneCountInString(prompt) > s.limits.MaxPrompt:
		s.buildError(w, http.StatusBadRequest, msgTooLong)
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
	// don't make a front end that can't be built now: building is paused
	// (v1.12; the budget may have run out since the modal loaded)
	if p := s.visitorPause(r, true); p != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": pausedMessage(p), "key": msgPaused, "paused": p})
		return
	}
	// don't make a front end that can't be built today
	c, err := v.VisitorRunCounts(ctx, session, ip, s.runQuota())
	if err != nil {
		s.fail(w, "build: counts", err)
		return
	}
	var qe *store.QuotaError
	if errors.As(s.runQuota().Check(c), &qe) && qe.Limit != store.LimitConcurrent {
		s.buildError(w, http.StatusTooManyRequests, qe.Limit)
		return
	}
	if !s.newLimiter.allow(string(ip), s.limits.NewPerIPWindow, s.limits.NewWindow, s.now()) {
		s.buildError(w, http.StatusTooManyRequests, msgSlowNew)
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
	writeJSON(w, http.StatusCreated, map[string]string{"id": frontend.PromptedID(slug), "slug": slug, "title": title})
}

// buildChat runs a visitor's chat turn with the limits applied (runChat):
// JSON {"prompt", "parent"} → the same SSE stream as the admin chat.
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

// buildLive shows one of the front end's revisions on the site, for this
// browser only: JSON {"rev": "<id>"} ("" = the newest) sets fe_live to
// "<front-end id>:<revision id>" → {"frontend", "slug", "revision", "number"}.
// The modal then reloads the site's front end (frontend-host.js).
func (s *Server) buildLive(w http.ResponseWriter, r *http.Request) {
	f, ok := s.ownedFrontend(w, r)
	if !ok {
		return
	}
	var in struct {
		Rev string `json:"rev"`
	}
	if err := readJSON(w, r, 4<<10, &in); err != nil {
		buildNotFound(w)
		return
	}
	var rv store.Revision
	if in.Rev == "" {
		revs, err := s.builderStore().Revisions(r.Context(), f.ID)
		if err != nil {
			s.fail(w, "build: revisions", err)
			return
		}
		if len(revs) == 0 {
			s.buildError(w, http.StatusConflict, msgNoRevision)
			return
		}
		rv = revs[0]
	} else {
		var err error
		if !frontend.ValidRevisionID(in.Rev) {
			buildNotFound(w)
			return
		}
		rv, err = s.builderStore().Revision(r.Context(), in.Rev)
		if errors.Is(err, store.ErrNotFound) || (err == nil && rv.FrontendID != f.ID) {
			buildNotFound(w)
			return
		} else if err != nil {
			s.fail(w, "build: revision", err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: liveCookie, Value: f.ID + ":" + rv.ID, Path: "/", HttpOnly: true, Secure: !s.cfg.Dev(), SameSite: http.SameSiteLaxMode,
		// no MaxAge: a session cookie
	})
	writeJSON(w, http.StatusOK, buildLive{Frontend: f.ID, Slug: strings.TrimPrefix(f.ID, "fe/"), Revision: rv.ID, Number: rv.Number})
}

// buildExit stops showing a draft: it clears this browser's fe_live, so it's
// always allowed.
func (s *Server) buildExit(w http.ResponseWriter, r *http.Request) {
	s.clearLive(w)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearLive(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: liveCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: !s.cfg.Dev(), SameSite: http.SameSiteLaxMode})
}

// errNoLive: the request has no fe_live cookie.
var errNoLive = errors.New("no fe_live cookie")

// liveSelection resolves the fe_live cookie: a front end the request's
// session owns and one of that front end's revisions. A legacy value without
// a revision means the newest. errNoLive if there's no cookie,
// store.ErrNotFound if it names anything else (the caller clears it), or a
// store error.
func (s *Server) liveSelection(r *http.Request) (store.FrontendInfo, store.Revision, error) {
	c, err := r.Cookie(liveCookie)
	if err != nil {
		return store.FrontendInfo{}, store.Revision{}, errNoLive
	}
	id, revID, named := strings.Cut(c.Value, ":")
	v := s.visitorStore()
	if v == nil || !frontend.IsPrompted(id) || (named && !frontend.ValidRevisionID(revID)) {
		return store.FrontendInfo{}, store.Revision{}, store.ErrNotFound
	}
	ctx := r.Context()
	f, err := v.OwnedFrontend(ctx, id, readSession(r))
	if err != nil {
		return store.FrontendInfo{}, store.Revision{}, err
	}
	var rv store.Revision
	if revID == "" {
		revs, err := s.builderStore().Revisions(ctx, f.ID)
		if err != nil {
			return store.FrontendInfo{}, store.Revision{}, err
		}
		if len(revs) == 0 {
			return store.FrontendInfo{}, store.Revision{}, store.ErrNotFound
		}
		rv = revs[0]
	} else if rv, err = s.builderStore().Revision(ctx, revID); err != nil {
		return store.FrontendInfo{}, store.Revision{}, err
	}
	if rv.FrontendID != f.ID {
		return store.FrontendInfo{}, store.Revision{}, store.ErrNotFound
	}
	return f, rv, nil
}

// buildSubmit sends one of the front end's revisions to Ben's review: JSON
// {"rev"} → {"status": {...}, "message"}.
func (s *Server) buildSubmit(w http.ResponseWriter, r *http.Request) {
	f, ok := s.ownedFrontend(w, r)
	if !ok {
		return
	}
	var in struct {
		Rev    string  `json:"rev"`
		Credit *string `json:"credit"` // optional: how to credit the visitor ("" = none)
	}
	if err := readJSON(w, r, 4<<10, &in); err != nil || !frontend.ValidRevisionID(in.Rev) {
		buildNotFound(w)
		return
	}
	if in.Credit != nil {
		// it goes public only when Ben approves the submission
		if err := s.builderStore().SetCreditRequested(r.Context(), f.ID, cleanCredit(*in.Credit)); err != nil {
			s.fail(w, "build: credit", err)
			return
		}
	}
	_, err := s.visitorStore().Submit(r.Context(), f.ID, in.Rev)
	if errors.Is(err, store.ErrNotFound) {
		buildNotFound(w)
		return
	} else if err != nil {
		s.fail(w, "build: submit", err)
		return
	}
	st, err := s.visitorStatusOf(r, f)
	if err != nil {
		s.fail(w, "build: status", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "message": buildMessage(msgSubmitted, s.limits)})
}

// buildCancel stops the visitor's running build of {slug} (v1.11): JSON
// {"canceled": n}. The run ends with a "canceled" event and no new version.
func (s *Server) buildCancel(w http.ResponseWriter, r *http.Request) {
	f, ok := s.ownedFrontend(w, r)
	if !ok {
		return
	}
	n, err := s.cancelRuns(r.Context(), f.ID)
	if err != nil {
		s.fail(w, "build: cancel", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"canceled": n})
}

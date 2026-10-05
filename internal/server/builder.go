package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bpriddy/personal-site-2026/internal/builder"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// The admin-only front-end builder: chat with Claude to create and reprompt
// front ends as immutable revisions, preview them, publish them into the
// rotation and roll back (docs/observer.md, "Front-end revisions and
// reprompting"; docs/frontend-protocol.md, "Front ends vs. revisions").

// builderState holds the builder's dependencies (set by WithBuilder).
type builderState struct {
	agent *builder.Builder // nil: chat disabled (no ANTHROPIC_API_KEY)
	files revfiles.Store   // revision files; defaults to FRONTENDS_DIR

	mu      sync.Mutex
	running map[string]bool // front-end IDs with a chat run in progress
	runs    map[int64]bool  // their builder_runs IDs, to close if the server stops mid-run
}

// InterruptRuns records the chat runs still in progress on this instance as
// failed. Call it when the server is stopping (a deploy, a scale-down): the
// runs die with the process, and left "running" they would count against the
// visitor's one-at-a-time limit until they aged out.
func (s *Server) InterruptRuns(ctx context.Context) {
	b := s.builderStore()
	if b == nil {
		return
	}
	s.builder.mu.Lock()
	ids := make([]int64, 0, len(s.builder.runs))
	for id := range s.builder.runs {
		ids = append(ids, id)
	}
	s.builder.mu.Unlock()
	for _, id := range ids {
		if err := b.FinishRun(ctx, id, "", "interrupted: the server stopped mid-run"); err != nil {
			s.log.Error("builder: interrupt run", "run", id, "err", err)
		} else {
			s.log.Warn("builder: run interrupted by shutdown", "run", id)
		}
	}
}

// RunTimeout bounds one builder chat run (many model turns).
const RunTimeout = 28 * time.Minute // under the 30-minute Cloud Run request timeout; 3D builds take 15-20

// WithBuilder configures the builder. agent may be nil (chat disabled, with a
// message; everything else still works); files may be nil (FRONTENDS_DIR).
func WithBuilder(agent *builder.Builder, files revfiles.Store) Option {
	return func(s *Server) error {
		s.builder.agent = agent
		s.builder.files = files
		return nil
	}
}

// builderRoutes registers admin routes under /admin/builder/.
func (s *Server) builderRoutes(admin *http.ServeMux) {
	if s.builder.files == nil {
		s.builder.files = revfiles.NewDir(s.cfg.FrontendsDir)
	}
	s.builder.running = map[string]bool{}
	admin.HandleFunc("GET /admin/builder/{$}", s.builderIndex)
	admin.HandleFunc("POST /admin/builder/new", s.builderNew)
	admin.HandleFunc("POST /admin/builder/rotation", s.builderRotation)
	admin.HandleFunc("GET /admin/builder/preview", s.builderPreview)
	admin.HandleFunc("GET /admin/builder/builtin/{name}", s.builderBuiltin)
	admin.HandleFunc("GET /admin/builder/fe/{slug}", s.builderFrontend)
	admin.HandleFunc("POST /admin/builder/fe/{slug}/chat", s.builderChat)
	admin.HandleFunc("POST /admin/builder/fe/{slug}/activate", s.builderActivate)
	admin.HandleFunc("POST /admin/builder/fe/{slug}/import", s.builderImport)
	admin.HandleFunc("GET /admin/builder/rev/{id}/{path...}", s.builderSource)
}

func (s *Server) builderDisabledReason() string {
	if s.builder.agent == nil {
		return "Chat is disabled: ANTHROPIC_API_KEY is not set on the server. You can still preview, publish and roll back revisions."
	}
	return ""
}

// needBuilderStore returns the builder store or answers 503.
func (s *Server) needBuilderStore(w http.ResponseWriter) store.Builder {
	b := s.builderStore()
	if b == nil {
		http.Error(w, "the builder needs a store with front-end revisions", http.StatusServiceUnavailable)
	}
	return b
}

type builderRow struct {
	store.FrontendInfo
	Href      string
	Revisions int
	Active    *store.Revision
	Servable  bool
}

func (s *Server) builderIndex(w http.ResponseWriter, r *http.Request) {
	b := s.needBuilderStore(w)
	if b == nil {
		return
	}
	fes, err := b.BuilderFrontends(r.Context())
	if err != nil {
		s.fail(w, "builder: frontends", err)
		return
	}
	var builtins, prompted, visitors []builderRow
	var subs []submissionRow
	visitorIDs := map[string]bool{}
	if v := s.visitorStore(); v != nil {
		if visitorIDs, err = v.VisitorFrontendIDs(r.Context()); err != nil {
			s.fail(w, "builder: visitor frontends", err)
			return
		}
		all, err := v.Submissions(r.Context(), 20)
		if err != nil {
			s.fail(w, "builder: submissions", err)
			return
		}
		for _, sub := range all {
			subs = append(subs, submissionRow{Submission: sub, Slug: strings.TrimPrefix(sub.FrontendID, "fe/")})
		}
	}
	for _, f := range fes {
		row := builderRow{FrontendInfo: f}
		if frontend.IsBuiltin(f.ID) {
			row.Href = "/admin/builder/builtin/" + strings.TrimPrefix(f.ID, "builtin/")
			row.Servable = true
			builtins = append(builtins, row)
			continue
		}
		if !frontend.IsPrompted(f.ID) {
			continue
		}
		row.Href = "/admin/builder/fe/" + strings.TrimPrefix(f.ID, "fe/")
		revs, err := b.Revisions(r.Context(), f.ID)
		if err != nil {
			s.fail(w, "builder: revisions", err)
			return
		}
		row.Revisions = len(revs)
		for i := range revs {
			if revs[i].ID == f.ActiveRevision {
				row.Active = &revs[i]
				row.Servable = true
			}
		}
		if visitorIDs[f.ID] {
			if row.Revisions > 0 { // empty visitor drafts are just noise here
				visitors = append(visitors, row)
			}
			continue
		}
		prompted = append(prompted, row)
	}
	pending := 0
	for _, sub := range subs {
		if sub.Status == store.SubmissionPending {
			pending++
		}
	}
	s.render(w, "admin/builder.html", http.StatusOK, map[string]any{
		"Builtins": builtins, "Prompted": prompted, "Visitors": visitors, "Submissions": subs, "Pending": pending,
		"Disabled":           s.builderDisabledReason(),
		"RotationOverridden": s.rotationOverride != nil, "Error": r.URL.Query().Get("error"),
	})
}

func (s *Server) builderNew(w http.ResponseWriter, r *http.Request) {
	b := s.needBuilderStore(w)
	if b == nil {
		return
	}
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	slug := strings.TrimSpace(strings.ToLower(r.FormValue("slug")))
	title := strings.TrimSpace(r.FormValue("title"))
	if prompt != "" && slug == "" {
		// prompt-first: name it from the prompt; the chat starts on arrival
		if title == "" {
			title = titleFromPrompt(prompt)
		}
		slug = s.freeSlug(r, slugify(title))
	}
	if title == "" {
		title = slug
	}
	if !frontend.ValidSlug(slug) || len(title) > 200 || len(prompt) > 8000 {
		http.Redirect(w, r, "/admin/builder/?error="+urlQuery("Slugs are lowercase letters, digits and dashes, starting with a letter or digit."), http.StatusSeeOther)
		return
	}
	err := b.CreatePromptedFrontend(r.Context(), frontend.PromptedID(slug), title)
	if errors.Is(err, store.ErrExists) {
		http.Redirect(w, r, "/admin/builder/?error="+urlQuery("fe/"+slug+" already exists."), http.StatusSeeOther)
		return
	} else if err != nil {
		s.fail(w, "builder: create", err)
		return
	}
	dest := "/admin/builder/fe/" + slug
	if prompt != "" {
		// builder.js reads #start=, fills the chat and sends it
		dest += "#start=" + url.QueryEscape(prompt)
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// titleFromPrompt names a front end after the first few words of its prompt,
// without a dangling small word at the end ("Make it feel like a quiet
// gallery", not "Make it feel like a"): the title is shown large in the
// builder.
func titleFromPrompt(prompt string) string {
	words := strings.FieldsFunc(prompt, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '\''
	})
	if len(words) > 7 {
		words = words[:7]
	}
	for len(words) > 1 && danglingWords[strings.ToLower(words[len(words)-1])] {
		words = words[:len(words)-1]
	}
	t := strings.Join(words, " ")
	if t == "" {
		return "Untitled"
	}
	rs := []rune(t)
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

// danglingWords don't end a title.
var danglingWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true, "of": true, "to": true,
	"in": true, "on": true, "at": true, "for": true, "with": true, "by": true, "from": true, "like": true,
	"as": true, "is": true, "it": true, "its": true, "my": true, "your": true, "that": true, "this": true,
}

// slugify turns a title into a valid fe/ slug.
func slugify(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if !frontend.ValidSlug(s) {
		return "frontend"
	}
	return s
}

// freeSlug returns base, or base-2, base-3, ... if taken.
func (s *Server) freeSlug(r *http.Request, base string) string {
	b := s.builderStore()
	for i := 1; i < 100; i++ {
		cand := base
		if i > 1 {
			cand = base + "-" + strconv.Itoa(i)
		}
		if reservedBuildSlugs[cand] { // /build/new, /build/preview
			continue
		}
		if _, err := b.BuilderFrontend(r.Context(), frontend.PromptedID(cand)); errors.Is(err, store.ErrNotFound) {
			return cand
		}
	}
	return base + "-" + strconv.FormatInt(time.Now().Unix(), 36)
}

// builderRotation adds a front end to the rotation or removes it, then
// returns to the builder page it came from. A prompted front end needs an
// active revision first.
func (s *Server) builderRotation(w http.ResponseWriter, r *http.Request) {
	b := s.needBuilderStore(w)
	if b == nil {
		return
	}
	id := r.FormValue("id")
	in := r.FormValue("in_rotation") == "1"
	back := r.FormValue("back")
	if !strings.HasPrefix(back, "/admin/builder/") || strings.ContainsAny(back, "\\\r\n") || strings.HasPrefix(back, "//") {
		back = "/admin/builder/"
	}
	f, err := b.BuilderFrontend(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "builder: frontend", err)
		return
	}
	if in && f.Kind == store.KindPrompted {
		if err := s.ensureActiveRevision(r.Context(), b, f); errors.Is(err, errNoRevisions) {
			http.Error(w, id+" has no versions yet, so there is nothing to show", http.StatusConflict)
			return
		} else if err != nil {
			s.fail(w, "builder: activate latest", err)
			return
		}
	}
	if err := s.store.SetFrontendInRotation(r.Context(), id, in); err != nil {
		s.fail(w, "builder: rotation", err)
		return
	}
	s.contentChanged()
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// builderPreview mints a fresh index URL for a servable ref, for the admin
// preview iframe (index tokens live 60s, so each load asks again).
func (s *Server) builderPreview(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	if !frontend.Valid(ref) {
		http.Error(w, "invalid ref", http.StatusBadRequest)
		return
	}
	if rev := frontend.RevisionOf(ref); rev != "" {
		b := s.needBuilderStore(w)
		if b == nil {
			return
		}
		if _, err := b.Revision(r.Context(), rev); errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			s.fail(w, "builder: revision", err)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ref": ref, "url": s.signedIndexURL(ref)})
}

func (s *Server) builderBuiltin(w http.ResponseWriter, r *http.Request) {
	b := s.needBuilderStore(w)
	if b == nil {
		return
	}
	f, err := b.BuilderFrontend(r.Context(), "builtin/"+r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && !frontend.IsBuiltin(f.ID)) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "builder: frontend", err)
		return
	}
	s.render(w, "admin/builder_frontend.html", http.StatusOK, map[string]any{
		"FE": f, "Builtin": true, "PreviewRef": f.ID, "Self": r.URL.Path,
		"RotationOverridden": s.rotationOverride != nil,
	})
}

type revisionRow struct {
	store.Revision
	Active       bool
	Selected     bool
	ParentNumber int
}

// builderFrontend is a prompted front end's page: chat, preview of the
// selected revision, and the revision history.
func (s *Server) builderFrontend(w http.ResponseWriter, r *http.Request) {
	b := s.needBuilderStore(w)
	if b == nil {
		return
	}
	id := frontend.PromptedID(r.PathValue("slug"))
	f, err := b.BuilderFrontend(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && f.Kind != store.KindPrompted) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "builder: frontend", err)
		return
	}
	revs, err := b.Revisions(r.Context(), id)
	if err != nil {
		s.fail(w, "builder: revisions", err)
		return
	}
	runs, err := b.Runs(r.Context(), id, 5)
	if err != nil {
		s.fail(w, "builder: runs", err)
		return
	}

	// selected: ?rev=, else the active revision, else the newest
	sel := r.URL.Query().Get("rev")
	if !slices.ContainsFunc(revs, func(x store.Revision) bool { return x.ID == sel }) {
		sel = f.ActiveRevision
		if sel == "" && len(revs) > 0 {
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
	var failed []store.Run
	for _, run := range runs {
		if run.Status == store.RunFailed || run.Status == store.RunRunning {
			failed = append(failed, run)
		}
	}
	s.builder.mu.Lock()
	running := s.builder.running[id]
	s.builder.mu.Unlock()

	s.render(w, "admin/builder_frontend.html", http.StatusOK, map[string]any{
		"FE": f, "Slug": r.PathValue("slug"), "Revisions": rows, "Selected": selected,
		"Conversation": conv, "PreviewRef": previewRef, "Runs": failed, "Running": running,
		"Disabled": s.builderDisabledReason(), "Self": r.URL.Path, "Visitor": s.isVisitorFrontend(r, f.ID),
		"RotationOverridden": s.rotationOverride != nil,
	})
}

// builderActivate makes a revision the front end's active one (publish, or
// roll back to an older one).
func (s *Server) builderActivate(w http.ResponseWriter, r *http.Request) {
	b := s.needBuilderStore(w)
	if b == nil {
		return
	}
	slug := r.PathValue("slug")
	rev := r.FormValue("rev")
	err := b.SetActiveRevision(r.Context(), frontend.PromptedID(slug), rev)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "builder: activate", err)
		return
	}
	s.contentChanged()
	http.Redirect(w, r, "/admin/builder/fe/"+slug+"?rev="+rev, http.StatusSeeOther)
}

// builderSource shows a revision's file as plain text.
func (s *Server) builderSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !frontend.ValidRevisionID(id) {
		http.NotFound(w, r)
		return
	}
	files, err := s.builder.files.Read(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body, ok := files[r.PathValue("path")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Write(body)
}

// saveRevision validates files, writes them to storage and records the
// revision. The files are written first, so a stored revision is always
// servable.
func (s *Server) saveRevision(ctx context.Context, rev store.Revision, files revfiles.Files) (store.Revision, error) {
	if err := builder.Validate(files); err != nil {
		return store.Revision{}, err
	}
	rev.ID = frontend.NewRevisionID()
	rev.Files = builder.Manifest(files)
	if err := s.builder.files.Write(ctx, rev.ID, files); err != nil {
		return store.Revision{}, fmt.Errorf("write files: %w", err)
	}
	return s.builderStore().AddRevision(ctx, rev)
}

// builderImport stores files posted as JSON as a new revision, without the
// model: {"files": {"index.html": "..."}, "summary": "...", "parent": "<rev id>"}.
// Used for hand-made front ends and deterministic tests.
func (s *Server) builderImport(w http.ResponseWriter, r *http.Request) {
	b := s.needBuilderStore(w)
	if b == nil {
		return
	}
	id := frontend.PromptedID(r.PathValue("slug"))
	if f, err := b.BuilderFrontend(r.Context(), id); err != nil || f.Kind != store.KindPrompted {
		http.NotFound(w, r)
		return
	}
	var in struct {
		Files   map[string]string `json:"files"`
		Summary string            `json:"summary"`
		Parent  string            `json:"parent"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, builder.MaxTotal*2)).Decode(&in); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	files := revfiles.Files{}
	for n, c := range in.Files {
		files[n] = []byte(c)
	}
	conv, _ := json.Marshal([]builder.Turn{{Role: "user", Text: "(Imported files.)", At: s.now()},
		{Role: "assistant", Text: in.Summary, At: s.now(), Actions: []string{"import"}}})
	if in.Parent != "" {
		if p, err := b.Revision(r.Context(), in.Parent); err == nil && p.FrontendID == id {
			hist := builder.ParseConversation(p.Conversation)
			var turns []builder.Turn
			json.Unmarshal(conv, &turns)
			conv, _ = json.Marshal(append(hist, turns...))
		}
	}
	rev, err := s.saveRevision(r.Context(), store.Revision{FrontendID: id, ParentID: in.Parent, Author: "ben",
		Summary: in.Summary, Conversation: conv}, files)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "unknown parent revision", http.StatusBadRequest)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": rev.ID, "number": rev.Number, "ref": frontend.RevRef(rev.ID)})
}

// siteContent is the current /api/site.json body, handed to the model for
// reference (whatever shape the content contract gives it).
func (s *Server) siteContent(ctx context.Context) json.RawMessage {
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/site.json", nil)
	rec := httptest.NewRecorder()
	s.siteJSON(rec, req)
	if rec.Code != http.StatusOK || !json.Valid(rec.Body.Bytes()) {
		return nil
	}
	return json.RawMessage(strings.TrimSpace(rec.Body.String()))
}

// builderChat runs one of Ben's chat turns (see runChat).
func (s *Server) builderChat(w http.ResponseWriter, r *http.Request) {
	b := s.needBuilderStore(w)
	if b == nil {
		return
	}
	if reason := s.builderDisabledReason(); reason != "" {
		http.Error(w, reason, http.StatusServiceUnavailable)
		return
	}
	f, err := b.BuilderFrontend(r.Context(), frontend.PromptedID(r.PathValue("slug")))
	if errors.Is(err, store.ErrNotFound) || (err == nil && f.Kind != store.KindPrompted) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "builder: frontend", err)
		return
	}
	s.runChat(w, r, f, chatCaller{author: "ben"})
}

// chatCaller says who is chatting: Ben (admin, no limits, raw errors) or a
// visitor (the public builder: limits, friendly messages, owner session).
type chatCaller struct {
	author  string // recorded on the revision: "ben" or "visitor"
	visitor bool
	session []byte // sha256(sid), visitors only
	ipHash  []byte // visitors only
}

// runChat runs one chat turn: a prompt applied to a parent revision of f by
// the model, streamed to the browser as server-sent events, ending in a new
// revision. Request: JSON {"prompt": "...", "parent": "<rev id or empty>",
// "images": ["<base64 PNG/JPEG/WebP/GIF>", ...]} (images optional).
// The caller has checked that the chat is enabled and that the caller may
// chat with f.
func (s *Server) runChat(w http.ResponseWriter, r *http.Request, f store.FrontendInfo, c chatCaller) {
	b := s.builderStore()
	id := f.ID
	// say says why a request can't run: friendly text for visitors
	say := func(status int, admin, visitor string) {
		if c.visitor {
			admin = visitor
		}
		http.Error(w, admin, status)
	}
	var in struct {
		Prompt string   `json:"prompt"`
		Parent string   `json:"parent"`
		Images []string `json:"images"`
	}
	// room for the attached images, base64-encoded
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10+maxAttachBen*(maxAttachBytes*4/3+4))
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Prompt) == "" {
		say(http.StatusBadRequest, "send JSON {\"prompt\": \"...\", \"parent\": \"<revision id>\"}", buildMessage(msgEmpty, s.limits))
		return
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if c.visitor && utf8.RuneCountInString(in.Prompt) > s.limits.MaxPrompt {
		say(http.StatusBadRequest, "", buildMessage(msgTooLong, s.limits))
		return
	}

	images, err := s.storeAttachments(r.Context(), in.Images, c.visitor)
	var ae errAttach
	if errors.As(err, &ae) {
		say(http.StatusBadRequest, ae.msg, ae.msg)
		return
	} else if err != nil {
		s.fail(w, "builder: attachments", err)
		return
	}
	req := builder.Request{FrontendID: id, Title: f.Title, Prompt: in.Prompt, Visitor: c.visitor, Images: images}
	var history []builder.Turn
	if in.Parent != "" {
		parent, err := b.Revision(r.Context(), in.Parent)
		if err != nil || parent.FrontendID != id {
			say(http.StatusBadRequest, "unknown parent revision", "That version doesn't exist any more. Reload the page and try again.")
			return
		}
		if req.Parent, err = s.builder.files.Read(r.Context(), parent.ID); err != nil {
			s.fail(w, "builder: read parent files", err)
			return
		}
		history = builder.ParseConversation(parent.Conversation)
		req.History = history
	}

	s.builder.mu.Lock()
	if s.builder.running[id] {
		s.builder.mu.Unlock()
		say(http.StatusConflict, "a chat run is already in progress for "+id, buildMessage(store.LimitConcurrent, s.limits))
		return
	}
	s.builder.running[id] = true
	s.builder.mu.Unlock()
	defer func() {
		s.builder.mu.Lock()
		delete(s.builder.running, id)
		s.builder.mu.Unlock()
	}()

	// The run outlives a closed tab: it finishes and saves its revision.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), RunTimeout)
	defer cancel()

	run := store.Run{FrontendID: id, ParentID: in.Parent, Prompt: in.Prompt}
	var runID int64
	if c.visitor {
		runID, err = s.visitorStore().StartVisitorRun(ctx, run, c.session, c.ipHash, s.runQuota())
		var qe *store.QuotaError
		if errors.As(err, &qe) {
			say(http.StatusTooManyRequests, "", buildMessage(qe.Limit, s.limits))
			return
		}
	} else {
		runID, err = b.StartRun(ctx, run)
	}
	if err != nil {
		s.fail(w, "builder: start run", err)
		return
	}
	s.builder.mu.Lock()
	if s.builder.runs == nil {
		s.builder.runs = map[int64]bool{}
	}
	s.builder.runs[runID] = true
	s.builder.mu.Unlock()
	defer func() {
		s.builder.mu.Lock()
		delete(s.builder.runs, runID)
		s.builder.mu.Unlock()
	}()
	req.Content = s.siteContent(ctx)

	ev := newEventStream(w)
	stopPing := ev.keepAlive(15 * time.Second)
	defer stopPing()
	ev.send(builder.Event{Type: "status", Text: "working"})

	started := s.now()
	res, err := s.builder.agent.Run(ctx, req, ev.send)
	var rev store.Revision
	if err == nil {
		turns := append(slices.Clone(history),
			builder.Turn{Role: "user", Text: in.Prompt, At: started, Images: attachmentPaths(images)},
			builder.Turn{Role: "assistant", Text: res.Summary, At: s.now(), Actions: res.Actions})
		conv, _ := json.Marshal(turns)
		rev, err = s.saveRevision(ctx, store.Revision{FrontendID: id, ParentID: in.Parent, Author: c.author,
			Summary: res.Summary, Conversation: conv}, res.Files)
	}
	if err != nil {
		s.log.Error("builder: run", "frontend", id, "visitor", c.visitor, "err", err)
		if ferr := b.FinishRun(ctx, runID, "", err.Error()); ferr != nil {
			s.log.Error("builder: finish run", "err", ferr)
		}
		text := err.Error()
		if c.visitor {
			// never show a visitor internals (API errors, spend limits, ...)
			text = buildMessage(msgUnavailable, s.limits)
			if errors.Is(err, builder.ErrRefused) {
				text = buildMessage(msgRefused, s.limits)
			}
		}
		ev.send(builder.Event{Type: "error", Text: text})
		return
	}
	if ferr := b.FinishRun(ctx, runID, rev.ID, ""); ferr != nil {
		s.log.Error("builder: finish run", "err", ferr)
	}
	ev.send(builder.Event{Type: "revision", Revision: rev.ID, Number: rev.Number, Text: res.Summary})
	ev.send(builder.Event{Type: "done"})
}

// eventStream writes server-sent events. Writes after the client has gone
// fail silently; the run carries on.
type eventStream struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

func newEventStream(w http.ResponseWriter) *eventStream {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	e := &eventStream{w: w, rc: http.NewResponseController(w)}
	e.rc.Flush()
	return e
}

func (e *eventStream) send(ev builder.Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprintf(e.w, "data: %s\n\n", b)
	e.rc.Flush()
}

// keepAlive sends an SSE comment periodically so idle proxies keep the
// connection open during long model turns.
func (e *eventStream) keepAlive(every time.Duration) (stop func()) {
	done := make(chan struct{})
	t := time.NewTicker(every)
	go func() {
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				e.mu.Lock()
				io.WriteString(e.w, ": ping\n\n")
				e.rc.Flush()
				e.mu.Unlock()
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

func urlQuery(s string) string {
	r := strings.NewReplacer("%", "%25", "&", "%26", "+", "%2B", " ", "+", "#", "%23", "?", "%3F", "=", "%3D", "/", "%2F")
	return r.Replace(s)
}

var errNoRevisions = errors.New("front end has no revisions")

// ensureActiveRevision makes the latest revision of a prompted front end
// active if none is, so adding it to the rotation always means "show it":
// the rotation skips front ends without an active revision.
func (s *Server) ensureActiveRevision(ctx context.Context, b store.Builder, f store.FrontendInfo) error {
	if f.ActiveRevision != "" {
		return nil
	}
	revs, err := b.Revisions(ctx, f.ID)
	if err != nil {
		return err
	}
	if len(revs) == 0 {
		return errNoRevisions
	}
	latest := revs[0]
	for _, r := range revs[1:] {
		if r.Number > latest.Number {
			latest = r
		}
	}
	return b.SetActiveRevision(ctx, f.ID, latest.ID)
}

// attachmentPaths lists the /media/ paths of attached images.
func attachmentPaths(images []builder.Attachment) []string {
	var out []string
	for _, a := range images {
		out = append(out, a.Path)
	}
	return out
}

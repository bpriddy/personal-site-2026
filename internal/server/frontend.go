package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/fetoken"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// pickCookie holds the visitor's per-visit front-end pick (a session cookie).
const pickCookie = "fe_pick"

type frontendResponse struct {
	Ref   string `json:"ref"`   // the front-end ID
	Serve string `json:"serve"` // the servable ref the token carries
	URL   string `json:"url"`
	// View live (v1.2): the visitor's own draft, shown only to them
	Draft    bool   `json:"draft,omitempty"`
	Exit     string `json:"exit,omitempty"`     // POST here to stop viewing it
	Revision string `json:"revision,omitempty"` // the draft's revision id
	Number   int    `json:"number,omitempty"`   // and its number
	Title    string `json:"title,omitempty"`    // the draft's title (visitor text)
	// Credit (v1.9): who made the front end, for the site bar ("" = none).
	// Only for rotation picks, whose credit Ben has reviewed; plain text.
	Credit string `json:"credit,omitempty"`
	// Choices is how many front ends the rotation holds (v1.4): the parent
	// shows its shuffle control only when there is something to shuffle to.
	Choices int `json:"choices"`
}

// apiFrontend tells the parent page which front end to load and hands it a
// freshly signed user-content URL for that front end's index. See
// docs/frontend-protocol.md, "Main site", "Front ends vs. revisions" and
// "View live".
func (s *Server) apiFrontend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	id, serve := frontend.DefaultRef, frontend.DefaultRef
	var resp frontendResponse
	choices, credit := 0, ""
	if r.URL.Query().Get("fallback") != "1" { // fallback leaves the visit's pick (and fe_live) alone
		rot := s.currentRotation(r.Context())
		choices = len(rot.ids)
		if draft, ok := s.liveDraft(w, r); ok {
			resp = draft
		} else {
			id = s.visitPick(w, r, rot.ids)
			serve = rot.serve[id]
			credit = rot.credit[id]
		}
	}
	if resp.Ref == "" {
		resp = frontendResponse{Ref: id, Serve: serve, URL: s.signedIndexURL(serve), Credit: credit}
	}
	resp.Choices = choices
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.log.Error("api/frontend", "err", err)
	}
}

// liveDraft serves View live: if the fe_live cookie names a front end the
// request's session owns and one of its revisions ("<front-end id>:<revision
// id>"; see liveSelection), it returns that revision as a draft. Any other
// fe_live cookie is cleared.
func (s *Server) liveDraft(w http.ResponseWriter, r *http.Request) (frontendResponse, bool) {
	f, rv, err := s.liveSelection(r)
	switch {
	case errors.Is(err, errNoLive):
		return frontendResponse{}, false
	case errors.Is(err, store.ErrNotFound):
		s.clearLive(w)
		return frontendResponse{}, false
	case err != nil:
		s.log.Error("api/frontend: live draft", "err", err)
		return frontendResponse{}, false // keep the cookie: maybe it works next time
	}
	serve := frontend.RevRef(rv.ID)
	return frontendResponse{Ref: f.ID, Serve: serve, URL: s.signedIndexURL(serve),
		Draft: true, Exit: buildExitPath, Revision: rv.ID, Number: rv.Number, Title: f.Title}, true
}

// signedIndexURL mints a fresh user-content URL for a servable ref's index.
func (s *Server) signedIndexURL(serve frontend.Ref) string {
	tok := fetoken.Sign(s.cfg.SigningKey, fetoken.Claims{Ref: serve, Issued: s.now()})
	return strings.TrimRight(s.cfg.UsercontentOrigin, "/") + "/t/" + tok + "/"
}

// visitPick returns the visit's front end: the fe_pick cookie if it names an
// ID in the current rotation, otherwise a new random pick, stored in the cookie.
func (s *Server) visitPick(w http.ResponseWriter, r *http.Request, rotation frontend.Rotation) frontend.Ref {
	cur := ""
	if c, err := r.Cookie(pickCookie); err == nil && frontend.ValidID(c.Value) && rotation.Contains(c.Value) {
		cur = c.Value
	}
	ref := cur
	if _, shuffle := r.URL.Query()["shuffle"]; ref == "" || shuffle {
		// ?shuffle: a fresh pick, different from the current one when possible
		others := rotation
		if cur != "" && len(rotation) > 1 {
			others = slices.DeleteFunc(slices.Clone(rotation), func(id frontend.Ref) bool { return id == cur })
		}
		ref = others.Pick(s.intn)
	}
	// A visit ends after pickTTL without a page view. A session cookie isn't
	// enough: Safari restores session cookies when it reopens, so the first
	// pick would stick forever. Refreshed on every load (sliding).
	http.SetCookie(w, &http.Cookie{
		Name:     pickCookie,
		Value:    ref,
		Path:     "/",
		MaxAge:   pickTTL,
		HttpOnly: true,
		Secure:   !s.cfg.Dev(),
		SameSite: http.SameSiteLaxMode,
	})
	return ref
}

// pickTTL is how long a visit's front-end pick lasts without a page view.
const pickTTL = 30 * 60

// rotation is the servable rotation: front-end IDs, and the ref each serves.
type rotation struct {
	ids    frontend.Rotation
	serve  map[frontend.Ref]frontend.Ref
	credit map[frontend.Ref]string // the front ends' credits, if any
}

// builderStore is the store's builder side (prompted front ends and their
// revisions), or nil if the store doesn't have one.
func (s *Server) builderStore() store.Builder {
	b, _ := s.store.(store.Builder)
	return b
}

// currentRotation is FRONTEND_ROTATION if set, else the front ends marked in
// rotation in the store: the default first, then by ID. A prompted front end
// is servable only once it has an active revision. If the store fails or
// nothing servable is in rotation, visitors get the default front end.
func (s *Server) currentRotation(ctx context.Context) rotation {
	def := rotation{ids: frontend.Rotation{frontend.DefaultRef}, serve: map[string]string{frontend.DefaultRef: frontend.DefaultRef}}
	out := rotation{serve: map[string]string{}, credit: map[string]string{}}

	// active revisions of prompted front ends (and, without an override, the
	// store's rotation itself)
	var infos []store.FrontendInfo
	if b := s.builderStore(); b != nil {
		var err error
		if infos, err = b.BuilderFrontends(ctx); err != nil {
			s.log.Error("rotation: store", "err", err)
			if s.rotationOverride == nil {
				return def
			} // an override's built-ins still work
		}
	} else if s.rotationOverride == nil {
		fes, err := s.store.Frontends(ctx)
		if err != nil {
			s.log.Error("rotation: store", "err", err)
			return def
		}
		for _, f := range fes {
			infos = append(infos, store.FrontendInfo{ID: f.Ref, Kind: store.KindBuiltin, InRotation: f.InRotation})
		}
	}
	active := map[string]string{}
	for _, f := range infos {
		if f.ActiveRevision != "" {
			active[f.ID] = f.ActiveRevision
		}
		if f.Credit != "" {
			out.credit[f.ID] = f.Credit
		}
	}
	add := func(id string) {
		switch {
		case frontend.IsBuiltin(id):
			out.serve[id] = id
		case frontend.IsPrompted(id) && frontend.ValidRevisionID(active[id]):
			out.serve[id] = frontend.RevRef(active[id])
		default:
			return // not servable (yet)
		}
		if !out.ids.Contains(id) {
			out.ids = append(out.ids, id)
		}
	}

	if s.rotationOverride != nil {
		for _, id := range s.rotationOverride {
			add(id)
		}
	} else {
		for _, f := range infos { // ordered by ID
			if f.InRotation {
				add(f.ID)
			}
		}
		slices.SortStableFunc(out.ids, func(a, b frontend.Ref) int {
			switch {
			case a == frontend.DefaultRef:
				return -1
			case b == frontend.DefaultRef:
				return 1
			}
			return 0
		})
	}
	if len(out.ids) == 0 {
		return def
	}
	return out
}

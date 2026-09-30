package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/fetoken"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
)

// pickCookie holds the visitor's per-visit front-end pick (a session cookie).
const pickCookie = "fe_pick"

type frontendResponse struct {
	Ref string `json:"ref"`
	URL string `json:"url"`
}

// apiFrontend tells the parent page which front end to load and hands it a
// freshly signed user-content URL for that front end's index. See
// docs/frontend-protocol.md, "Main site".
func (s *Server) apiFrontend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	var ref frontend.Ref
	if r.URL.Query().Get("fallback") == "1" {
		ref = frontend.DefaultRef // and leave the visit's pick alone
	} else {
		ref = s.visitPick(w, r, s.currentRotation(r.Context()))
	}

	tok := fetoken.Sign(s.cfg.SigningKey, fetoken.Claims{Ref: ref, Issued: s.now()})
	resp := frontendResponse{
		Ref: ref,
		URL: strings.TrimRight(s.cfg.UsercontentOrigin, "/") + "/t/" + tok + "/",
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.log.Error("api/frontend", "err", err)
	}
}

// visitPick returns the visit's front end: the fe_pick cookie if it names a ref
// in the current rotation, otherwise a new random pick, stored in the cookie.
func (s *Server) visitPick(w http.ResponseWriter, r *http.Request, rotation frontend.Rotation) frontend.Ref {
	if c, err := r.Cookie(pickCookie); err == nil && frontend.Valid(c.Value) && rotation.Contains(c.Value) {
		return c.Value
	}
	ref := rotation.Pick(s.intn)
	http.SetCookie(w, &http.Cookie{
		Name:     pickCookie,
		Value:    ref,
		Path:     "/",
		HttpOnly: true,
		Secure:   !s.cfg.Dev(),
		SameSite: http.SameSiteLaxMode,
		// no MaxAge/Expires: a session cookie, so the pick lasts one visit
	})
	return ref
}

// currentRotation is FRONTEND_ROTATION if set, else the front ends marked in
// rotation in the store: the default first, then by ref. If the store fails or
// nothing is in rotation, visitors get the default front end.
func (s *Server) currentRotation(ctx context.Context) frontend.Rotation {
	if s.rotationOverride != nil {
		return s.rotationOverride
	}
	fes, err := s.store.Frontends(ctx)
	if err != nil {
		s.log.Error("rotation: store", "err", err)
		return frontend.Rotation{frontend.DefaultRef}
	}
	var rot frontend.Rotation
	for _, f := range fes { // ordered by ref
		if f.InRotation && frontend.Valid(f.Ref) {
			rot = append(rot, f.Ref)
		}
	}
	slices.SortStableFunc(rot, func(a, b frontend.Ref) int {
		switch {
		case a == frontend.DefaultRef:
			return -1
		case b == frontend.DefaultRef:
			return 1
		}
		return 0
	})
	if len(rot) == 0 {
		return frontend.Rotation{frontend.DefaultRef}
	}
	return rot
}

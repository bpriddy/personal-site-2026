package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"time"
)

// Visitor identity for the public builder (docs/frontend-protocol.md, v1.2
// "Identity"): the sid cookie holds a random 128-bit id; the server only ever
// stores or compares sha256(sid). No sign-up: the browser is the identity.

const (
	sidCookie = "sid"
	sidMaxAge = 30 * 24 * time.Hour
)

var sidPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type sessionKey struct{}

// hashSID returns sha256 of a sid cookie value.
func hashSID(sid string) []byte {
	h := sha256.Sum256([]byte(sid))
	return h[:]
}

// readSession returns the hash of the request's sid cookie, or nil if it has
// none (or a malformed one). It never sets a cookie.
func readSession(r *http.Request) []byte {
	if v, ok := r.Context().Value(sessionKey{}).([]byte); ok {
		return v
	}
	c, err := r.Cookie(sidCookie)
	if err != nil || !sidPattern.MatchString(c.Value) {
		return nil
	}
	return hashSID(c.Value)
}

// withSession gives the request a visitor session: it keeps a valid sid
// cookie or issues a new one, and refreshes its 30-day lifetime. Only the
// public builder's routes use it, so plain page views never get a cookie.
func (s *Server) withSession(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sid := ""
		if c, err := r.Cookie(sidCookie); err == nil && sidPattern.MatchString(c.Value) {
			sid = c.Value
		} else {
			b := make([]byte, 16)
			rand.Read(b)
			sid = hex.EncodeToString(b)
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sidCookie,
			Value:    sid,
			Path:     "/",
			MaxAge:   int(sidMaxAge / time.Second),
			HttpOnly: true,
			Secure:   !s.cfg.Dev(),
			SameSite: http.SameSiteLaxMode,
		})
		h(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, hashSID(sid))))
	}
}

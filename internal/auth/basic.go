// Package auth guards the admin UI.
package auth

import (
	"crypto/subtle"
	"net/http"
)

// Basic wraps h in HTTP basic auth. A stopgap: replace with session login
// (or Google IAP in front of /admin on Cloud Run) before real use.
func Basic(user, password string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="admin", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

package observer

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// IngestHandler serves POST /api/observe (docs/frontend-protocol.md, "Observer
// ingestion"). Only same-origin requests from mainOrigin are accepted (the
// parent page forwards reports with navigator.sendBeacon); the body is JSON,
// sent as application/json or text/plain, at most MaxBody bytes. A well-formed
// report always gets 204 at once, including when it is rate-limited, a
// duplicate, or dropped: the work happens on the observer's workers.
func (o *Observer) IngestHandler(mainOrigin string) http.Handler {
	mainOrigin = strings.TrimRight(mainOrigin, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			http.Error(w, "cross-origin", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != mainOrigin {
			http.Error(w, "cross-origin", http.StatusForbidden)
			return
		}
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || (mt != "application/json" && mt != "text/plain") {
			http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		rep, err := ParseReport(body)
		if err != nil {
			http.Error(w, "invalid report", http.StatusBadRequest)
			return
		}
		o.Submit(ClientIP(r, o.cfg.XFFHops), rep)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
}

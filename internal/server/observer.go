package server

import "net/http"

// The site observer: ingests detections (content gaps, type breaks, front-end
// failures), heals them, and reports them in the admin (docs/observer.md;
// docs/frontend-protocol.md, "Observer").

// observerState holds the observer's dependencies (set by an Option).
type observerState struct{}

// observerRoutes registers public routes (POST /api/observe) on mux and admin
// routes under /admin/observer/.
func (s *Server) observerRoutes(mux, admin *http.ServeMux) {}

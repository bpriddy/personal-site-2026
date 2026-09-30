package server

import "net/http"

// The admin-only front-end builder: chat with Claude to create and reprompt
// front ends as immutable revisions (docs/observer.md, "Front-end revisions and
// reprompting"; docs/frontend-protocol.md, "Builder").

// builderState holds the builder's dependencies (set by an Option).
type builderState struct{}

// builderRoutes registers admin routes under /admin/builder/.
func (s *Server) builderRoutes(admin *http.ServeMux) {}

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/bpriddy/personal-site-2026/internal/store"
)

// Ben's review of visitor submissions (docs/frontend-protocol.md, v1.2
// "Review"): listed on /admin/builder/, approved or rejected here, counted on
// the Builder tab's badge (admin.js, /admin/builder/pending.json).

func (s *Server) reviewRoutes(admin *http.ServeMux) {
	admin.HandleFunc("GET /admin/builder/pending.json", s.adminPending)
	admin.HandleFunc("POST /admin/builder/submissions/{id}/{action}", s.adminReview)
}

func (s *Server) adminPending(w http.ResponseWriter, r *http.Request) {
	n := 0
	if v := s.visitorStore(); v != nil {
		var err error
		if n, err = v.PendingSubmissions(r.Context()); err != nil {
			s.fail(w, "builder: pending", err)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]int{"pending": n})
}

func (s *Server) adminReview(w http.ResponseWriter, r *http.Request) {
	v := s.visitorStore()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	action := r.PathValue("action")
	if v == nil || err != nil || (action != "approve" && action != "reject") {
		http.NotFound(w, r)
		return
	}
	_, err = v.ReviewSubmission(r.Context(), id, action == "approve")
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrNotPending):
		http.Redirect(w, r, "/admin/builder/?error="+urlQuery("That submission was already reviewed."), http.StatusSeeOther)
		return
	case err != nil:
		s.fail(w, "builder: review", err)
		return
	}
	s.contentChanged()
	http.Redirect(w, r, "/admin/builder/#submissions", http.StatusSeeOther)
}

// isVisitorFrontend reports whether a visitor made the front end.
func (s *Server) isVisitorFrontend(r *http.Request, id string) bool {
	v := s.visitorStore()
	if v == nil {
		return false
	}
	ids, err := v.VisitorFrontendIDs(r.Context())
	return err == nil && ids[id]
}

// submissionRow is a submission as the admin lists it.
type submissionRow struct {
	store.Submission
	Slug string
}

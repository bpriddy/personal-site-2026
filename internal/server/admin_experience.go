package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// The experience list in the admin (v1.8): a dashboard table and a small
// form per role. Minimal on purpose: role, company, dates, one line.

func (s *Server) experienceStore(w http.ResponseWriter) store.ExperienceStore {
	es := store.ExperienceOf(s.store)
	if es == nil {
		http.Error(w, "this store has no experience list", http.StatusNotImplemented)
	}
	return es
}

// validateExperience lists what's wrong with e (nothing for a valid role).
func validateExperience(e content.Experience) []string {
	var out []string
	if !content.ValidSlug(e.Slug) {
		out = append(out, fmt.Sprintf("slug %q: lowercase letters, digits and dashes", e.Slug))
	}
	if strings.TrimSpace(e.Role) == "" || strings.TrimSpace(e.Company) == "" {
		out = append(out, fmt.Sprintf("%s: role and company are required", e.Slug))
	}
	for _, m := range []string{e.Start, e.End} {
		if !content.ValidMonth(m) {
			out = append(out, fmt.Sprintf("%s: dates are YYYY-MM or YYYY (got %q)", e.Slug, m))
		}
	}
	if e.Current && e.End != "" {
		out = append(out, fmt.Sprintf("%s: the current role has no end date", e.Slug))
	}
	if len(e.Note) > 160 {
		out = append(out, fmt.Sprintf("%s: the note is one short line (at most 160 characters)", e.Slug))
	}
	return out
}

// adminExperienceNew starts a new role from ?slug=.
func (s *Server) adminExperienceNew(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.URL.Query().Get("slug"))
	if !content.ValidSlug(slug) {
		http.Error(w, "give a slug: lowercase letters, digits and dashes", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/experience/"+slug, http.StatusSeeOther)
}

func (s *Server) adminExperienceForm(w http.ResponseWriter, r *http.Request) {
	es := s.experienceStore(w)
	if es == nil {
		return
	}
	slug := r.PathValue("slug")
	if !content.ValidSlug(slug) {
		http.NotFound(w, r)
		return
	}
	e, err := es.ExperienceItem(r.Context(), slug)
	isNew := errors.Is(err, store.ErrNotFound)
	if isNew {
		e = content.Experience{Slug: slug, Order: 100}
	} else if err != nil {
		s.fail(w, "experience", err)
		return
	}
	s.render(w, "admin/experience_form.html", http.StatusOK, map[string]any{"Item": e, "New": isNew, "Error": ""})
}

func (s *Server) adminExperienceSave(w http.ResponseWriter, r *http.Request) {
	es := s.experienceStore(w)
	if es == nil {
		return
	}
	slug := r.PathValue("slug")
	if !content.ValidSlug(slug) {
		http.NotFound(w, r)
		return
	}
	_, err := es.ExperienceItem(r.Context(), slug)
	isNew := errors.Is(err, store.ErrNotFound)
	if err != nil && !isNew {
		s.fail(w, "experience", err)
		return
	}
	e := content.Experience{
		Slug:      slug,
		Role:      strings.TrimSpace(r.FormValue("role")),
		Company:   strings.TrimSpace(r.FormValue("company")),
		Start:     strings.TrimSpace(r.FormValue("start")),
		End:       strings.TrimSpace(r.FormValue("end")),
		Current:   r.FormValue("current") == "on",
		Note:      strings.TrimSpace(r.FormValue("note")),
		Published: r.FormValue("published") == "on",
	}
	e.Order, _ = strconv.Atoi(strings.TrimSpace(r.FormValue("order")))
	if problems := validateExperience(e); len(problems) > 0 {
		s.render(w, "admin/experience_form.html", http.StatusBadRequest, map[string]any{
			"Item": e, "New": isNew, "Error": strings.Join(problems, "; "),
		})
		return
	}
	if err := es.SaveExperience(r.Context(), e); err != nil {
		s.fail(w, "save experience", err)
		return
	}
	s.contentChanged()
	http.Redirect(w, r, "/admin/#experience", http.StatusSeeOther)
}

// adminExperiencePublish is the dashboard's publish toggle: published=1|0.
func (s *Server) adminExperiencePublish(w http.ResponseWriter, r *http.Request) {
	es := s.experienceStore(w)
	if es == nil {
		return
	}
	e, err := es.ExperienceItem(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "experience", err)
		return
	}
	e.Published = r.FormValue("published") == "1"
	if err := es.SaveExperience(r.Context(), e); err != nil {
		s.fail(w, "save experience", err)
		return
	}
	s.contentChanged()
	http.Redirect(w, r, "/admin/#experience", http.StatusSeeOther)
}

func (s *Server) adminExperienceDelete(w http.ResponseWriter, r *http.Request) {
	es := s.experienceStore(w)
	if es == nil {
		return
	}
	if err := es.DeleteExperience(r.Context(), r.PathValue("slug")); err != nil {
		s.fail(w, "delete experience", err)
		return
	}
	s.contentChanged()
	http.Redirect(w, r, "/admin/#experience", http.StatusSeeOther)
}

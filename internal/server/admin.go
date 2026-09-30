package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

func (s *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	pages, err := s.store.Pages(r.Context())
	if err != nil {
		s.fail(w, "pages", err)
		return
	}
	exps, err := s.store.Experiments(r.Context())
	if err != nil {
		s.fail(w, "experiments", err)
		return
	}
	fes, err := s.store.Frontends(r.Context())
	if err != nil {
		s.fail(w, "frontends", err)
		return
	}
	s.render(w, "admin/dashboard.html", http.StatusOK, map[string]any{
		"Pages": pages, "Experiments": exps, "Frontends": fes,
		"RotationOverridden": s.rotationOverride != nil,
	})
}

// pages are addressed by ?slug= because the home page's slug is empty
func (s *Server) adminPageForm(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	page, err := s.store.Page(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		page = content.Page{Slug: slug}
	} else if err != nil {
		s.fail(w, "page", err)
		return
	}
	s.render(w, "admin/page_form.html", http.StatusOK, map[string]any{"Page": page})
}

func (s *Server) adminPageSave(w http.ResponseWriter, r *http.Request) {
	p := content.Page{
		Slug:      r.FormValue("slug"),
		Title:     r.FormValue("title"),
		Body:      r.FormValue("body"),
		Published: r.FormValue("published") == "on",
	}
	if err := s.store.SavePage(r.Context(), p); err != nil {
		s.fail(w, "save page", err)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (s *Server) adminExperimentForm(w http.ResponseWriter, r *http.Request) {
	exp, err := s.store.Experiment(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "experiment", err)
		return
	}
	s.render(w, "admin/experiment_form.html", http.StatusOK, map[string]any{"Experiment": exp})
}

// experiments are created in the repo (they need a build), so admin only edits
// the listing metadata of existing ones
func (s *Server) adminExperimentSave(w http.ResponseWriter, r *http.Request) {
	exp, err := s.store.Experiment(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "experiment", err)
		return
	}
	exp.Title = r.FormValue("title")
	exp.Summary = r.FormValue("summary")
	exp.Published = r.FormValue("published") == "on"
	exp.Order, _ = strconv.Atoi(r.FormValue("order"))
	if err := s.store.SaveExperiment(r.Context(), exp); err != nil {
		s.fail(w, "save experiment", err)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

// adminFrontendRotation adds a registered front end to the rotation or removes
// it. The ref is a form field because refs contain "/".
func (s *Server) adminFrontendRotation(w http.ResponseWriter, r *http.Request) {
	ref := r.FormValue("ref")
	in := r.FormValue("in_rotation") == "1"
	err := s.store.SetFrontendInRotation(r.Context(), ref, in)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "set rotation", err)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

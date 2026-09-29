package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	page, err := s.store.Page(r.Context(), "")
	if err != nil {
		s.fail(w, "home page", err)
		return
	}
	exps, err := s.publishedExperiments(r)
	if err != nil {
		s.fail(w, "experiments", err)
		return
	}
	s.renderPublic(w, "home.html", map[string]any{"Page": page, "Experiments": exps})
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	page, err := s.store.Page(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && !page.Published) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "page", err)
		return
	}
	s.renderPublic(w, "page.html", map[string]any{"Page": page})
}

func (s *Server) experiments(w http.ResponseWriter, r *http.Request) {
	exps, err := s.publishedExperiments(r)
	if err != nil {
		s.fail(w, "experiments", err)
		return
	}
	s.renderPublic(w, "experiments.html", map[string]any{"Experiments": exps})
}

// experimentFiles serves experiments/<slug>/dist/. Only slugs registered in the
// CMS are served, so the directory lookup is bounded to known names.
func (s *Server) experimentFiles(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	exp, err := s.store.Experiment(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !exp.Published && !s.cfg.Dev()) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "experiment", err)
		return
	}
	dist := filepath.Join(s.cfg.ExperimentsDir, slug, "dist")
	if _, err := os.Stat(dist); err != nil {
		http.Error(w, "experiment not built: run `make experiments`", http.StatusNotFound)
		return
	}
	http.StripPrefix("/experiments/"+slug, http.FileServer(http.Dir(dist))).ServeHTTP(w, r)
}

// siteJSON is the wasm site's view of the CMS: the same published content the
// transcript renders, as data.
func (s *Server) siteJSON(w http.ResponseWriter, r *http.Request) {
	type page struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	type experiment struct {
		Slug    string `json:"slug"`
		Title   string `json:"title"`
		Summary string `json:"summary"`
	}
	out := struct {
		Pages       []page       `json:"pages"`
		Experiments []experiment `json:"experiments"`
	}{Pages: []page{}, Experiments: []experiment{}}

	pages, err := s.store.Pages(r.Context())
	if err != nil {
		s.fail(w, "pages", err)
		return
	}
	for _, p := range pages {
		if p.Published {
			out.Pages = append(out.Pages, page{p.Slug, p.Title, p.Body})
		}
	}
	exps, err := s.publishedExperiments(r)
	if err != nil {
		s.fail(w, "experiments", err)
		return
	}
	for _, e := range exps {
		out.Experiments = append(out.Experiments, experiment{e.Slug, e.Title, e.Summary})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		s.log.Error("site.json", "err", err)
	}
}

func (s *Server) publishedExperiments(r *http.Request) ([]content.Experiment, error) {
	all, err := s.store.Experiments(r.Context())
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, e := range all {
		if e.Published {
			out = append(out, e)
		}
	}
	return out, nil
}

package server

import (
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
	s.render(w, "home.html", http.StatusOK, map[string]any{"Page": page, "Experiments": exps})
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
	s.render(w, "page.html", http.StatusOK, map[string]any{"Page": page})
}

func (s *Server) experiments(w http.ResponseWriter, r *http.Request) {
	exps, err := s.publishedExperiments(r)
	if err != nil {
		s.fail(w, "experiments", err)
		return
	}
	s.render(w, "experiments.html", http.StatusOK, map[string]any{"Experiments": exps})
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

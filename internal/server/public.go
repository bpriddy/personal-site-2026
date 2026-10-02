package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/contract"
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
	projects, err := s.publishedProjects(r.Context())
	if err != nil {
		s.fail(w, "projects", err)
		return
	}
	data := map[string]any{"Page": page, "Experiments": exps, "WorkCount": len(projects)}
	if len(projects) > selectedWork {
		projects = projects[:selectedWork]
	}
	data["Work"] = projects
	s.renderPublic(w, r, "home.html", http.StatusOK, data)
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	page, err := s.store.Page(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && !page.Published) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "page", err)
		return
	}
	s.renderPublic(w, r, "page.html", http.StatusOK, map[string]any{"Page": page})
}

func (s *Server) experiments(w http.ResponseWriter, r *http.Request) {
	exps, err := s.publishedExperiments(r)
	if err != nil {
		s.fail(w, "experiments", err)
		return
	}
	s.renderPublic(w, r, "experiments.html", http.StatusOK, map[string]any{"Experiments": exps})
}

// notFound renders the 404 transcript in the public shell, so a front end's
// navigation to an unknown slug swaps in a real "not found" page.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.renderPublic(w, r, "notfound.html", http.StatusNotFound, map[string]any{})
}

// siteJSON serves the content contract (docs/content-contract.json): the same
// published content the transcript renders, normalized for front ends, with
// the observer's generated values merged beneath the human ones.
func (s *Server) siteJSON(w http.ResponseWriter, r *http.Request) {
	site, err := contract.Build(r.Context(), s.store, s.contentGenerated())
	if err != nil {
		s.fail(w, "site.json", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if err := json.NewEncoder(w).Encode(site); err != nil {
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

// navItem is one entry of the transcript's pages nav (the meta row).
type navItem struct {
	Href, Label, Index string
	Current            bool
}

// publicNav lists the published pages (home first) and, when any are
// published, the work and the experiments, numbered 01, 02, ... like the default front
// end's nav. Errors just leave the nav out: it never breaks a page.
func (s *Server) publicNav(r *http.Request) []navItem {
	var out []navItem
	route := strings.Trim(r.URL.Path, "/")
	add := func(href, label, slug string) {
		current := route == slug || (slug != "" && strings.HasPrefix(route, slug+"/"))
		out = append(out, navItem{Href: href, Label: label, Index: fmt.Sprintf("%02d", len(out)+1), Current: current})
	}
	if pages, err := s.store.Pages(r.Context()); err == nil {
		for _, p := range pages {
			if !p.Published {
				continue
			}
			label := strings.TrimSpace(p.Title)
			if p.Slug == "" {
				label = "Index"
			} else if label == "" {
				label = p.Slug
			}
			add("/"+p.Slug, label, p.Slug)
		}
	}
	if projects, err := s.publishedProjects(r.Context()); err == nil && len(projects) > 0 {
		add("/work/", "Work", "work")
	}
	if exps, err := s.publishedExperiments(r); err == nil && len(exps) > 0 {
		add("/experiments/", "Experiments", "experiments")
	}
	return out
}

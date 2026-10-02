package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/contract"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// youtubeEmbedOrigin is YouTube's privacy-enhanced player, the only
// third-party frame the transcript loads (a project's film).
const youtubeEmbedOrigin = "https://www.youtube-nocookie.com"

// selectedWork is how many projects the home page features.
const selectedWork = 6

// projectView is a published project as the transcript renders it.
type projectView struct {
	content.Project
	Index  string               // "01"
	Hero   *contract.MediaItem  // the first image, if any
	Thumb  *contract.MediaItem  // the index row's still or loop: the first loop, else the hero
	Loops  []contract.MediaItem // every loop
	Images []contract.MediaItem // the other images
	Meta   string               // "Samsung · 2020"
	Embed  string               // the YouTube player URL, or ""
	Watch  string               // the YouTube watch page, or ""
	Href   string               // "/work/<slug>"
	Link   string               // the live work, if it is an http(s) URL
}

func newProjectView(i int, p content.Project) projectView {
	v := projectView{Project: p, Index: twoDigits(i + 1), Href: "/work/" + p.Slug}
	for _, m := range contract.Media(p.Media) {
		switch m.Kind {
		case content.MediaImage:
			if v.Hero == nil {
				v.Hero = &m
			} else {
				v.Images = append(v.Images, m)
			}
		case content.MediaLoop:
			v.Loops = append(v.Loops, m)
		}
	}
	switch {
	case len(v.Loops) > 0:
		v.Thumb = &v.Loops[0]
	case v.Hero != nil:
		v.Thumb = v.Hero
	}
	var meta []string
	for _, s := range []string{p.Client, p.Year} {
		if s = strings.TrimSpace(s); s != "" {
			meta = append(meta, s)
		}
	}
	v.Meta = strings.Join(meta, " · ")
	if content.ValidYouTube(p.YouTube) {
		v.Embed = youtubeEmbedOrigin + "/embed/" + p.YouTube + "?rel=0"
		v.Watch = content.YouTubeURL(p.YouTube)
	}
	if l := strings.TrimSpace(p.Link); l != "" && content.ValidLink(l) {
		v.Link = l
	}
	if strings.TrimSpace(v.Title) == "" {
		v.Title = p.Slug
	}
	return v
}

func twoDigits(n int) string { return fmt.Sprintf("%02d", n) }

// publishedProjects lists the published projects in featured order; none if
// the store has no Projects.
func (s *Server) publishedProjects(ctx context.Context) ([]projectView, error) {
	ps := store.ProjectsOf(s.store)
	if ps == nil {
		return nil, nil
	}
	all, err := ps.Projects(ctx)
	if err != nil {
		return nil, err
	}
	var out []projectView
	for _, p := range all {
		if p.Published {
			out = append(out, newProjectView(len(out), p))
		}
	}
	return out, nil
}

// work is /work: the index of published projects.
func (s *Server) work(w http.ResponseWriter, r *http.Request) {
	projects, err := s.publishedProjects(r.Context())
	if err != nil {
		s.fail(w, "projects", err)
		return
	}
	s.renderPublic(w, r, "work.html", http.StatusOK, map[string]any{"Projects": projects})
}

// project is /work/<slug>. Unpublished and unknown projects are the 404 page.
func (s *Server) project(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	projects, err := s.publishedProjects(r.Context())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "projects", err)
		return
	}
	for i, p := range projects {
		if p.Slug != slug {
			continue
		}
		next := projects[(i+1)%len(projects)]
		data := map[string]any{"Project": p, "Count": len(projects)}
		if len(projects) > 1 {
			data["Next"] = next
		}
		s.renderPublic(w, r, "project.html", http.StatusOK, data)
		return
	}
	s.notFound(w, r)
}

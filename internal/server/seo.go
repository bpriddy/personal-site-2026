package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/bpriddy/personal-site-2026/internal/store"
)

// Search and sharing basics: a real robots.txt, a sitemap, descriptions taken
// from the content (excerpt), canonical and Open Graph tags (shell.html), and
// a schema.org Person on the home page built only from published content.

// excerpt is the start of text, cut at a word boundary within max runes, with
// an ellipsis when cut. Paragraph breaks become spaces.
func excerpt(text string, max int) string {
	t := strings.Join(strings.Fields(text), " ")
	r := []rune(t)
	if len(r) <= max {
		return t
	}
	cut := max
	for cut > max/2 && !unicode.IsSpace(r[cut]) {
		cut--
	}
	return strings.TrimRightFunc(string(r[:cut]), func(c rune) bool { return unicode.IsSpace(c) || unicode.IsPunct(c) }) + "…"
}

// robots allows everything public and points at the sitemap.
func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	fmt.Fprintf(w, "User-agent: *\nAllow: /\nDisallow: /admin/\nDisallow: /build/\nDisallow: /api/\n\nSitemap: %s/sitemap.xml\n",
		strings.TrimRight(s.cfg.MainOrigin, "/"))
}

// sitemap lists the published routes: home, pages, work, each project, experiments.
func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(s.cfg.MainOrigin, "/")
	type url struct {
		loc string
		mod time.Time
	}
	var urls []url
	if pages, err := s.store.Pages(r.Context()); err == nil {
		for _, p := range pages {
			if p.Published {
				urls = append(urls, url{base + "/" + p.Slug, p.UpdatedAt})
			}
		}
	}
	if ps := store.ProjectsOf(s.store); ps != nil {
		if all, err := ps.Projects(r.Context()); err == nil {
			var latest time.Time
			var items []url
			for _, p := range all {
				if p.Published {
					items = append(items, url{base + "/work/" + p.Slug, p.UpdatedAt})
					if p.UpdatedAt.After(latest) {
						latest = p.UpdatedAt
					}
				}
			}
			if len(items) > 0 {
				urls = append(urls, url{base + "/work/", latest})
				urls = append(urls, items...)
			}
		}
	}
	if exps, err := s.publishedExperiments(r); err == nil && len(exps) > 0 {
		urls = append(urls, url{base + "/experiments", time.Time{}})
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, u := range urls {
		sb.WriteString("  <url><loc>")
		template.HTMLEscape(&sb, []byte(u.loc))
		sb.WriteString("</loc>")
		if !u.mod.IsZero() {
			sb.WriteString("<lastmod>" + u.mod.UTC().Format("2006-01-02") + "</lastmod>")
		}
		sb.WriteString("</url>\n")
	}
	sb.WriteString("</urlset>\n")
	w.Write([]byte(sb.String()))
}

// personLD is the home page's schema.org Person, from published content only:
// the name (the home page's title), the site, a description (the bio's
// excerpt), and the current role and company from the experience list.
func personLD(name, url, bio string, current *experienceView) template.JS {
	p := map[string]any{"@context": "https://schema.org", "@type": "Person", "name": name, "url": url}
	if d := excerpt(bio, 300); d != "" {
		p["description"] = d
	}
	if current != nil {
		p["jobTitle"] = current.Role
		p["worksFor"] = map[string]any{"@type": "Organization", "name": current.Company}
	}
	b, _ := json.Marshal(p) // escapes <, > and & for the script element
	return template.JS(b)
}

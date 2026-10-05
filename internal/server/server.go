// Package server wires routes: the public site (HTML transcript + front-end
// host), the /api endpoints front ends use, and the admin CMS. Front-end files
// themselves are served only by the user-content service (cmd/usercontent).
package server

import (
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/auth"
	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/contract"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/media"
	"github.com/bpriddy/personal-site-2026/internal/store"
	"github.com/bpriddy/personal-site-2026/web"
)

type Server struct {
	cfg   config.Config
	store store.Store
	log   *slog.Logger
	tmpl  map[string]*template.Template
	mux   *http.ServeMux

	rotationOverride frontend.Rotation // FRONTEND_ROTATION, if set; else the store decides
	intn             func(int) int     // randomness for picking from the rotation
	now              func() time.Time  // token issue time
	publicCSP        string

	// Extension points, each owned by its own file (see docs/frontend-protocol.md,
	// "Builder" and "Observer"). Configured by Options passed to New.
	builder  builderState  // builder.go
	observer observerState // observer.go

	// the public builder (build.go)
	limits     BuildLimits   // build_limits.go; WithBuildLimits
	newLimiter windowLimiter // POST /build/new per client IP

	media media.Source // /media/...; WithMedia, default MEDIA_DIR
}

// WithMedia sets where /media/... comes from (default: the MEDIA_DIR directory).
func WithMedia(src media.Source) Option {
	return func(s *Server) error {
		s.media = src
		return nil
	}
}

// An Option configures optional subsystems (builder, observer) at New.
type Option func(*Server) error

// New builds the server. Besides cfg it reads FRONTEND_ROTATION (see
// frontend.RotationEnv): comma-separated refs overriding the default rotation;
// an invalid ref is a startup error.
func New(cfg config.Config, st store.Store, log *slog.Logger, opts ...Option) (*Server, error) {
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	rot, _, err := frontend.RotationOverride()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg: cfg, store: st, log: log, tmpl: tmpl, mux: http.NewServeMux(),
		rotationOverride: rot, intn: rand.IntN, now: time.Now,
		publicCSP: publicCSP(cfg.UsercontentOrigin),
		limits:    DefaultBuildLimits,
	}
	for _, o := range opts {
		if err := o(s); err != nil {
			return nil, err
		}
	}
	s.routes()
	return s, nil
}

// ServeHTTP routes the request. Unmatched GET/HEAD requests get the 404
// transcript page, so client-side navigation to an unknown slug shows it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// www.<domain> → the canonical origin: front ends trust exactly one parent
	// origin (frame-ancestors, postMessage), so the site must live at one host.
	if strings.HasPrefix(r.Host, "www.") && r.URL.Path != "/health" {
		http.Redirect(w, r, strings.TrimRight(s.cfg.MainOrigin, "/")+r.URL.RequestURI(), http.StatusMovedPermanently)
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if _, pattern := s.mux.Handler(r); pattern == "" {
			s.notFound(w, r)
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	static, _ := fs.Sub(web.FS, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	// the house fonts: fixed names, cached for a year (web.Fonts)
	fonts := http.StripPrefix("/static/fonts/", http.FileServerFS(web.Fonts))
	s.mux.Handle("GET /static/fonts/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fonts.ServeHTTP(w, r)
	}))
	// project and experiment media, for the transcript (docs/frontend-protocol.md, v1.4)
	if s.media == nil {
		s.media = media.Dir{Root: s.cfg.MediaDir}
	}
	s.mux.Handle("GET /media/", media.New(s.media, s.log))
	s.mux.HandleFunc("GET /api/site.json", s.siteJSON)
	s.mux.HandleFunc("GET /api/frontend", s.apiFrontend)
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	s.mux.HandleFunc("GET /{$}", s.home)
	// both spellings: front ends navigate by slug ("experiments" → /experiments)
	s.mux.HandleFunc("GET /robots.txt", s.robots)
	s.mux.HandleFunc("GET /sitemap.xml", s.sitemap)
	s.mux.HandleFunc("GET /experiments", s.experiments)
	s.mux.HandleFunc("GET /experiments/{$}", s.experiments)
	s.mux.HandleFunc("GET /work", s.work)
	s.mux.HandleFunc("GET /work/{$}", s.work)
	s.mux.HandleFunc("GET /work/{slug}", s.project)
	s.mux.HandleFunc("GET /{slug}", s.page)

	admin := http.NewServeMux()
	admin.HandleFunc("GET /admin/{$}", s.adminDashboard)
	admin.HandleFunc("GET /admin/pages/edit", s.adminPageForm)
	admin.HandleFunc("POST /admin/pages/edit", s.adminPageSave)
	admin.HandleFunc("GET /admin/experiments/{slug}", s.adminExperimentForm)
	admin.HandleFunc("POST /admin/experiments/{slug}", s.adminExperimentSave)
	admin.HandleFunc("POST /admin/frontends", s.adminFrontendRotation)
	admin.HandleFunc("GET /admin/projects/{$}", s.adminProjectNew)
	admin.HandleFunc("GET /admin/projects/{slug}", s.adminProjectForm)
	admin.HandleFunc("POST /admin/projects/{slug}", s.adminProjectSave)
	admin.HandleFunc("POST /admin/projects/{slug}/publish", s.adminProjectPublish)
	admin.HandleFunc("POST /admin/import/projects", s.adminImportProjects)
	admin.HandleFunc("GET /admin/experience/{$}", s.adminExperienceNew)
	admin.HandleFunc("GET /admin/experience/{slug}", s.adminExperienceForm)
	admin.HandleFunc("POST /admin/experience/{slug}", s.adminExperienceSave)
	admin.HandleFunc("POST /admin/experience/{slug}/publish", s.adminExperiencePublish)
	admin.HandleFunc("POST /admin/experience/{slug}/delete", s.adminExperienceDelete)
	// basic auth credentials ride along on cross-site requests, so reject those
	guarded := http.NewCrossOriginProtection().Handler(admin)
	s.builderRoutes(admin)
	s.reviewRoutes(admin)
	s.buildRoutes(s.mux)
	s.observerRoutes(s.mux, admin)
	s.mux.Handle("/admin/", auth.Basic(s.cfg.AdminUser, s.cfg.AdminPassword, guarded))
}

// layouts maps each template directory to the layout its pages extend: the
// public shell (transcript + front-end host script) or the admin layout.
var layouts = map[string]string{
	"public": "templates/public/shell.html",
	"admin":  "templates/admin/base.html",
}

// parseTemplates builds one template set per page: its layout + that page.
// Pages are keyed by path under templates/, e.g. "public/home.html".
func parseTemplates() (map[string]*template.Template, error) {
	out := map[string]*template.Template{}
	for dir, layout := range layouts {
		pages, err := fs.Glob(web.FS, "templates/"+dir+"/*.html")
		if err != nil {
			return nil, err
		}
		for _, p := range pages {
			if p == layout {
				continue
			}
			t, err := template.New("").Funcs(templateFuncs).ParseFS(web.FS, layout, p)
			if err != nil {
				return nil, err
			}
			out[strings.TrimPrefix(p, "templates/")] = t
		}
	}
	return out, nil
}

func (s *Server) render(w http.ResponseWriter, name string, status int, data any) {
	t, ok := s.tmpl[name]
	if !ok {
		s.fail(w, "missing template "+name, nil)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		s.log.Error("render", "template", name, "err", err)
	}
}

// renderPublic renders a page in the public shell, under the public CSP.
func (s *Server) renderPublic(w http.ResponseWriter, r *http.Request, name string, status int, data map[string]any) {
	w.Header().Set("Content-Security-Policy", s.publicCSP)
	data["Nav"] = s.publicNav(r)
	data["SiteURL"] = strings.TrimRight(s.cfg.MainOrigin, "/")
	data["Canonical"] = data["SiteURL"].(string) + r.URL.Path
	data["Year"] = s.now().Year()
	s.render(w, "public/"+name, status, data)
}

// publicCSP locks public pages to their own scripts and styles; the only frame
// they may load is the user-content origin, and they only talk to this origin.
func publicCSP(usercontentOrigin string) string {
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self' data:",
		"media-src 'self'", // project loops (/media/)
		"connect-src 'self'",
		// the front end; YouTube's privacy-enhanced player, for project films
		"frame-src " + strings.TrimRight(usercontentOrigin, "/") + " " + youtubeEmbedOrigin,
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

func (s *Server) fail(w http.ResponseWriter, msg string, err error) {
	s.log.Error(msg, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

var templateFuncs = template.FuncMap{
	// paragraphs splits plain-text CMS bodies on blank lines, so the transcript
	// keeps the same paragraphs the front ends draw.
	"paragraphs": paragraphs,
	// nameLines sets a name on two lines, first word and the rest ("Ben" /
	// "Priddy"), the way the default front end does.
	"nameLines": func(title string) []string {
		f := strings.Fields(title)
		if len(f) < 2 {
			return f
		}
		return []string{f[0], strings.Join(f[1:], " ")}
	},
	// prose tags a body's paragraphs for the type scale (docs/design-pov.md,
	// 5.1 "Bio"): the first real paragraph is the lede; a short line before it
	// ("Coming soon.") is an aside, set in italic; the rest is body text.
	"prose": func(body string) []proseBlock {
		var out []proseBlock
		lede := false
		for i, p := range paragraphs(body) {
			switch {
			case i == 0 && len([]rune(p)) <= 40:
				out = append(out, proseBlock{p, "aside"})
			case !lede:
				lede = true
				out = append(out, proseBlock{p, "lede"})
			default:
				out = append(out, proseBlock{p, "body"})
			}
		}
		return out
	},
	// excerpt: a description from content (search and sharing)
	"excerpt": excerpt,
	// two pads a number to two digits, like the site's indices (01, 02)
	"two": func(n int) string { return fmt.Sprintf("%02d", n) },
	"inc": func(n int) int { return n + 1 },
	"mul": func(a, b int) int { return a * b },
	// heroMax: a still is never shown past 1.25x its width (the old ones are small)
	"heroMax": func(w int) int { return w * 5 / 4 },
	// dict builds a map for passing several values to a template
	"dict": func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			if k, ok := kv[i].(string); ok {
				m[k] = kv[i+1]
			}
		}
		return m
	},
	// extLink is u if it is an http(s) URL, else ""
	"extLink": func(u string) string {
		if u = strings.TrimSpace(u); content.ValidLink(u) {
			return u
		}
		return ""
	},
	// shape sorts media for layout without inline styles (the public CSP has
	// no 'unsafe-inline'): "strip" (3:1 and wider), "wide", or "box"
	"shape": func(m contract.MediaItem) string {
		switch {
		case m.Width <= 0 || m.Height <= 0:
			return "wide"
		case m.Width >= 3*m.Height:
			return "strip"
		case 2*m.Width >= 3*m.Height:
			return "wide"
		}
		return "box"
	},
	// join sets a list as the admin's comma list
	"join": func(l []string) string { return strings.Join(l, ", ") },
	// thumbOf is a media list's first still: an image, or a loop's poster
	"thumbOf": func(l []content.Media) string {
		for _, m := range contract.Media(l) {
			if m.Kind == content.MediaImage {
				return m.Src
			}
			if m.Poster != "" {
				return m.Poster
			}
		}
		return ""
	},
	// firstLoop is a media list's first loop, if any
	"firstLoop": func(l []content.Media) *contract.MediaItem {
		for _, m := range contract.Media(l) {
			if m.Kind == content.MediaLoop {
				return &m
			}
		}
		return nil
	},
}

// proseBlock is one paragraph and its role in the type scale.
type proseBlock struct{ Text, Kind string }

func paragraphs(body string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

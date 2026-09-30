// Package server wires routes: the public site (HTML transcript + front-end
// host), the /api endpoints front ends use, and the admin CMS. Front-end files
// themselves are served only by the user-content service (cmd/usercontent).
package server

import (
	"html/template"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/auth"
	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
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
	s.mux.HandleFunc("GET /api/site.json", s.siteJSON)
	s.mux.HandleFunc("GET /api/frontend", s.apiFrontend)
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	s.mux.HandleFunc("GET /{$}", s.home)
	// both spellings: front ends navigate by slug ("experiments" → /experiments)
	s.mux.HandleFunc("GET /experiments", s.experiments)
	s.mux.HandleFunc("GET /experiments/{$}", s.experiments)
	s.mux.HandleFunc("GET /{slug}", s.page)

	admin := http.NewServeMux()
	admin.HandleFunc("GET /admin/{$}", s.adminDashboard)
	admin.HandleFunc("GET /admin/pages/edit", s.adminPageForm)
	admin.HandleFunc("POST /admin/pages/edit", s.adminPageSave)
	admin.HandleFunc("GET /admin/experiments/{slug}", s.adminExperimentForm)
	admin.HandleFunc("POST /admin/experiments/{slug}", s.adminExperimentSave)
	admin.HandleFunc("POST /admin/frontends", s.adminFrontendRotation)
	// basic auth credentials ride along on cross-site requests, so reject those
	guarded := http.NewCrossOriginProtection().Handler(admin)
	s.builderRoutes(admin)
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
func (s *Server) renderPublic(w http.ResponseWriter, name string, status int, data map[string]any) {
	w.Header().Set("Content-Security-Policy", s.publicCSP)
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
		"connect-src 'self'",
		"frame-src " + strings.TrimRight(usercontentOrigin, "/"),
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
	// keeps the same paragraphs the canvas front ends draw.
	"paragraphs": func(body string) []string {
		var out []string
		for _, p := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	},
}

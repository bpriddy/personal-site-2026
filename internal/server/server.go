// Package server wires routes: public site, admin CMS, and experiments.
package server

import (
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/auth"
	"github.com/bpriddy/personal-site-2026/internal/config"
	"github.com/bpriddy/personal-site-2026/internal/store"
	"github.com/bpriddy/personal-site-2026/web"
)

type Server struct {
	cfg   config.Config
	store store.Store
	log   *slog.Logger
	tmpl  map[string]*template.Template
	mux   *http.ServeMux
}

func New(cfg config.Config, st store.Store, log *slog.Logger) (*Server, error) {
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, store: st, log: log, tmpl: tmpl, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	static, _ := fs.Sub(web.FS, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	s.mux.HandleFunc("GET /{$}", s.home)
	s.mux.HandleFunc("GET /experiments/{$}", s.experiments)
	s.mux.HandleFunc("GET /experiments/{slug}/{path...}", s.experimentFiles)
	s.mux.HandleFunc("GET /{slug}", s.page)

	admin := http.NewServeMux()
	admin.HandleFunc("GET /admin/{$}", s.adminDashboard)
	admin.HandleFunc("GET /admin/pages/edit", s.adminPageForm)
	admin.HandleFunc("POST /admin/pages/edit", s.adminPageSave)
	admin.HandleFunc("GET /admin/experiments/{slug}", s.adminExperimentForm)
	admin.HandleFunc("POST /admin/experiments/{slug}", s.adminExperimentSave)
	// basic auth credentials ride along on cross-site requests, so reject those
	guarded := http.NewCrossOriginProtection().Handler(admin)
	s.mux.Handle("/admin/", auth.Basic(s.cfg.AdminUser, s.cfg.AdminPassword, guarded))
}

// parseTemplates builds one template set per page: base.html + that page.
func parseTemplates() (map[string]*template.Template, error) {
	out := map[string]*template.Template{}
	pages, err := fs.Glob(web.FS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	admin, err := fs.Glob(web.FS, "templates/admin/*.html")
	if err != nil {
		return nil, err
	}
	for _, p := range append(pages, admin...) {
		if strings.HasSuffix(p, "/base.html") {
			continue
		}
		t, err := template.ParseFS(web.FS, "templates/base.html", p)
		if err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(p, "templates/")] = t
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

func (s *Server) fail(w http.ResponseWriter, msg string, err error) {
	s.log.Error(msg, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

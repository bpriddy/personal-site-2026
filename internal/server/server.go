// Package server wires routes: public site, admin CMS, and experiments.
package server

import (
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	site  siteAssets
}

// siteAssets describes the built wasm site bundle (site/dist) the shell loads.
type siteAssets struct {
	Built   bool
	Version string // cache-buster for the unhashed site.js / site_bg.wasm
}

func New(cfg config.Config, st store.Store, log *slog.Logger) (*Server, error) {
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, store: st, log: log, tmpl: tmpl, mux: http.NewServeMux()}
	s.site = loadSiteAssets(cfg.SiteDir)
	if !s.site.Built {
		log.Warn("wasm site not built; serving the HTML transcript only", "dir", cfg.SiteDir)
	}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	static, _ := fs.Sub(web.FS, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	s.mux.Handle("GET /site/", http.StripPrefix("/site/", http.FileServer(http.Dir(s.cfg.SiteDir))))
	s.mux.HandleFunc("GET /api/site.json", s.siteJSON)
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

// layouts maps each template directory to the layout its pages extend: the
// public shell (canvas + transcript) or the admin layout.
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
			t, err := template.ParseFS(web.FS, layout, p)
			if err != nil {
				return nil, err
			}
			out[strings.TrimPrefix(p, "templates/")] = t
		}
	}
	return out, nil
}

func loadSiteAssets(dir string) siteAssets {
	fi, err := os.Stat(filepath.Join(dir, "site_bg.wasm"))
	if err != nil {
		return siteAssets{}
	}
	return siteAssets{Built: true, Version: strconv.FormatInt(fi.ModTime().Unix(), 36)}
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

// renderPublic renders a page in the public shell, which also needs the site bundle.
func (s *Server) renderPublic(w http.ResponseWriter, name string, data map[string]any) {
	data["Site"] = s.site
	s.render(w, "public/"+name, http.StatusOK, data)
}

func (s *Server) fail(w http.ResponseWriter, msg string, err error) {
	s.log.Error(msg, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

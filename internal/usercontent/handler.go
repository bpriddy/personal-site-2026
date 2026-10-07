// Package usercontent is the user-content service: it serves front-end files
// at /t/<token>/<path> to the main site's sandboxed iframe, plus the host API
// script and the house fonts. See docs/frontend-protocol.md ("User-content
// service").
package usercontent

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bpriddy/personal-site-2026/internal/fetoken"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/web"
	webuc "github.com/bpriddy/personal-site-2026/web/usercontent"
)

const (
	IndexMaxAge = 60 * time.Second // index.html: token age limit
	AssetMaxAge = 30 * time.Minute // everything else
	ClockSkew   = 30 * time.Second // how far in the future a token may be issued

	cacheIndex    = "no-store"
	cacheAsset    = "private, max-age=1800"
	cacheSiteHost = "public, max-age=300"
	cacheFont     = "public, max-age=31536000, immutable"
)

// Options configures a Handler.
type Options struct {
	SigningKey []byte // FRONTEND_SIGNING_KEY
	MainOrigin string // MAIN_ORIGIN, used in frame-ancestors and site-host.js
	// SelfOrigin (USERCONTENT_ORIGIN) is this service's own origin. Optional;
	// when set, the CSP names it next to 'self'. A front end runs in a sandbox
	// (an opaque origin), and WebKit doesn't match 'self' for its fetches, so
	// without the explicit origin Safari refuses e.g. a front end's own .wasm.
	SelfOrigin string
	Source     FileSource       // front-end files
	Now        func() time.Time // clock; nil means time.Now
	Log        *slog.Logger     // nil means discard
	Fonts      fs.FS            // served at /fonts/<file>; nil means web.Fonts
	Media      http.Handler     // serves /media/<name> (internal/media); nil means 404
}

// Handler implements the user-content routes.
type Handler struct {
	key      []byte
	src      FileSource
	now      func() time.Time
	log      *slog.Logger
	csp      string
	siteHost []byte
	fonts    fs.FS
	media    http.Handler
}

func New(o Options) (*Handler, error) {
	if len(o.SigningKey) == 0 {
		return nil, errors.New("usercontent: signing key is required")
	}
	if o.Source == nil {
		return nil, errors.New("usercontent: file source is required")
	}
	if err := checkOrigin(o.MainOrigin); err != nil {
		return nil, err
	}
	if o.SelfOrigin != "" {
		if err := checkOrigin(o.SelfOrigin); err != nil {
			return nil, err
		}
	}
	h := &Handler{
		key:      o.SigningKey,
		src:      o.Source,
		now:      o.Now,
		log:      o.Log,
		csp:      csp(o.MainOrigin, o.SelfOrigin),
		siteHost: []byte(strings.ReplaceAll(webuc.SiteHostJS, "__MAIN_ORIGIN__", o.MainOrigin)),
		fonts:    o.Fonts,
		media:    o.Media,
	}
	if h.fonts == nil {
		h.fonts = web.Fonts
	}
	if h.now == nil {
		h.now = time.Now
	}
	if h.log == nil {
		h.log = slog.New(slog.DiscardHandler)
	}
	return h, nil
}

// checkOrigin requires a bare http(s) origin, since it is pasted into a CSP
// header and into JavaScript.
func checkOrigin(o string) error {
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil ||
		u.Opaque != "" || strings.ContainsAny(o, " \t\r\n;,'\"\\") {
		return errors.New("usercontent: main origin must be a bare http(s) origin, got " + strconv.Quote(o))
	}
	return nil
}

func csp(mainOrigin, selfOrigin string) string {
	self := "'self'"
	if selfOrigin != "" {
		self += " " + selfOrigin
	}
	return strings.Join([]string{
		"sandbox allow-scripts",
		"default-src " + self,
		"script-src " + self + " 'unsafe-inline' 'wasm-unsafe-eval'",
		"style-src " + self + " 'unsafe-inline'",
		"img-src " + self + " data: blob:",
		"font-src " + self + " data:",
		"media-src " + self + " blob:",
		"connect-src " + self,
		"worker-src " + self + " blob:",
		"frame-src 'none'",
		"form-action 'none'",
		"base-uri 'none'",
		"frame-ancestors " + mainOrigin,
	}, "; ")
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/t/"):
		if !allowMethod(w, r) {
			return
		}
		h.serveToken(w, r, strings.TrimPrefix(p, "/t/"))
	case p == "/site-host.js":
		if !allowMethod(w, r) {
			return
		}
		hd := w.Header()
		hd.Set("Content-Type", "text/javascript; charset=utf-8")
		hd.Set("Access-Control-Allow-Origin", "*")
		hd.Set("Cross-Origin-Resource-Policy", "cross-origin")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Cache-Control", cacheSiteHost)
		hd.Set("Content-Length", strconv.Itoa(len(h.siteHost)))
		w.Write(h.siteHost)
	case strings.HasPrefix(p, "/fonts/"):
		if !allowMethod(w, r) {
			return
		}
		h.serveFont(w, r, strings.TrimPrefix(p, "/fonts/"))
	case strings.HasPrefix(p, "/media/") && h.media != nil:
		// project and experiment media, for front ends (img-src/media-src 'self')
		h.media.ServeHTTP(w, r)
	case p == "/health":
		if !allowMethod(w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "ok")
	default:
		http.NotFound(w, r)
	}
}

func allowMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

// serveToken handles /t/<token>/<rest>. rest is the decoded remainder of the
// URL path after "/t/".
func (h *Handler) serveToken(w http.ResponseWriter, r *http.Request, rest string) {
	hd := w.Header()
	hd.Set("Content-Security-Policy", h.csp)
	hd.Set("Access-Control-Allow-Origin", "*")
	hd.Set("Cross-Origin-Resource-Policy", "cross-origin")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", cacheIndex) // errors aren't cached either

	tok, name, ok := strings.Cut(rest, "/")
	if !ok {
		// /t/<token> with no trailing slash: relative asset URLs would resolve
		// against /t/, so don't serve it
		h.fail(w, r, http.StatusNotFound, "no trailing slash")
		return
	}
	claims, err := fetoken.Verify(h.key, tok)
	if err != nil {
		h.fail(w, r, http.StatusForbidden, err.Error())
		return
	}
	if !frontend.Valid(claims.Ref) {
		h.fail(w, r, http.StatusForbidden, "invalid ref")
		return
	}

	index := name == "" || name == "index.html"
	if index {
		name = "index.html"
	} else if !cleanName(name) {
		h.fail(w, r, http.StatusForbidden, "bad path")
		return
	}

	age := h.now().Sub(claims.Issued)
	if age < -ClockSkew {
		h.fail(w, r, http.StatusForbidden, "token from the future")
		return
	}
	if index {
		if age > IndexMaxAge {
			h.fail(w, r, http.StatusForbidden, "index token expired")
			return
		}
		if r.Header.Get("Sec-Fetch-Dest") != "iframe" {
			h.fail(w, r, http.StatusForbidden, "index not requested by an iframe")
			return
		}
	} else if age > AssetMaxAge {
		h.fail(w, r, http.StatusForbidden, "asset token expired")
		return
	}

	// built-in bundles ship a precompressed <name>.gz next to their text and
	// wasm files (scripts/build-frontends.sh); prefer it when the client takes
	// gzip. Built-ins only: revisions in the bucket have no .gz to look for.
	var obj *Object
	gz := false
	if strings.HasPrefix(claims.Ref, "builtin/") && compressible(name) {
		hd.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			if obj, err = h.src.Open(r.Context(), claims.Ref, name+".gz"); err == nil {
				gz = true
			}
		}
	}
	if !gz {
		obj, err = h.src.Open(r.Context(), claims.Ref, name)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			h.fail(w, r, http.StatusNotFound, "no such file")
			return
		}
		h.log.Error("usercontent: open", "ref", claims.Ref, "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer obj.Body.Close()

	if !index {
		hd.Set("Cache-Control", cacheAsset)
	}
	hd.Set("Content-Type", contentType(name))
	if gz {
		hd.Set("Content-Encoding", "gzip")
	}
	if obj.Size >= 0 {
		hd.Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	}
	if !obj.ModTime.IsZero() {
		hd.Set("Last-Modified", obj.ModTime.UTC().Format(http.TimeFormat))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		if _, err := io.Copy(w, obj.Body); err != nil {
			h.log.Debug("usercontent: copy", "ref", claims.Ref, "name", name, "err", err)
		}
	}
}

// compressible: the built-in files worth sending gzipped
func compressible(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".js", ".mjs", ".css", ".wasm", ".json", ".wgsl", ".svg", ".txt":
		return true
	}
	return false
}

// acceptsGzip: the request's Accept-Encoding lists gzip (without q=0)
func acceptsGzip(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept-Encoding") {
		for part := range strings.SplitSeq(v, ",") {
			enc, params, _ := strings.Cut(strings.TrimSpace(part), ";")
			if !strings.EqualFold(strings.TrimSpace(enc), "gzip") && strings.TrimSpace(enc) != "*" {
				continue
			}
			if q, ok := strings.CutPrefix(strings.ReplaceAll(params, " ", ""), "q="); ok && (q == "0" || q == "0.0" || q == "0.00" || q == "0.000") {
				return false
			}
			return true
		}
	}
	return false
}

// serveFont serves one house font (or its license) from /fonts/<name>, the
// same files the main site has at /static/fonts/. Front ends load them with
// @font-face url("/fonts/<name>"): same origin as the front end, so the
// sandbox CSP's font-src 'self' allows it. The sandboxed document's origin is
// opaque, so fonts are CORS requests with Origin: null, hence the wildcard.
// Fixed names, cached for a year.
func (h *Handler) serveFont(w http.ResponseWriter, r *http.Request, name string) {
	hd := w.Header()
	hd.Set("Access-Control-Allow-Origin", "*")
	hd.Set("Cross-Origin-Resource-Policy", "cross-origin")
	hd.Set("X-Content-Type-Options", "nosniff")
	ext := strings.ToLower(path.Ext(name))
	if strings.Contains(name, "/") || !cleanName(name) || (ext != ".woff2" && ext != ".txt") {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(h.fonts, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	hd.Set("Content-Type", contentType(name))
	hd.Set("Cache-Control", cacheFont)
	hd.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(b)
	}
}

// fail writes a plain error. The reason is logged, never sent.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, code int, reason string) {
	h.log.Debug("usercontent: refused", "code", code, "reason", reason, "path_len", len(r.URL.Path))
	http.Error(w, http.StatusText(code), code)
}

// cleanName reports whether name is a safe relative file path: non-empty
// segments, none starting with "." (which covers "." and ".."), no backslashes
// or NULs, and not a directory (no trailing slash).
func cleanName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	for seg := range strings.SplitSeq(name, "/") {
		if seg == "" || seg[0] == '.' {
			return false
		}
	}
	return path.Clean(name) == name
}

var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".map":   "application/json",
	".wasm":  "application/wasm",
	".wgsl":  "text/plain; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".avif":  "image/avif",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".mp3":   "audio/mpeg",
	".ogg":   "audio/ogg",
	".wav":   "audio/wav",
	".mp4":   "video/mp4",
	".webm":  "video/webm",
	".glb":   "model/gltf-binary",
	".gltf":  "model/gltf+json",
	".bin":   "application/octet-stream",
}

// contentType maps a file name to its media type. The table wins over the
// system MIME database so results don't depend on the host; unknown types are
// served as opaque bytes (nosniff stops browsers guessing).
func contentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := contentTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

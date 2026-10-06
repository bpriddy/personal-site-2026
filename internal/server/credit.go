package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/frontend"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// cleanCredit makes a credit safe to show as one line of text: control and
// format characters dropped, whitespace collapsed, at most store.MaxCredit
// runes. "" means no credit.
func cleanCredit(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > store.MaxCredit {
		s = strings.TrimSpace(string([]rune(s)[:store.MaxCredit]))
	}
	return s
}

// builderCredit sets a front end's credit from the admin form (credit=...).
func (s *Server) builderCredit(w http.ResponseWriter, r *http.Request) {
	b := s.needBuilderStore(w)
	if b == nil {
		return
	}
	slug := r.PathValue("slug")
	err := b.SetCredit(r.Context(), frontend.PromptedID(slug), cleanCredit(r.FormValue("credit")))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "builder: credit", err)
		return
	}
	s.contentChanged()
	dest := "/admin/builder/fe/" + slug
	if rev := r.FormValue("rev"); frontend.ValidRevisionID(rev) {
		dest += "?rev=" + rev
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// siteCopy is the site-wide copy with defaults filled in (v1.10).
func (s *Server) siteCopy(ctx context.Context) map[string]string {
	var stored map[string]string
	if cs := store.CopyOf(s.store); cs != nil {
		var err error
		if stored, err = cs.SiteCopy(ctx); err != nil {
			s.log.Error("site copy", "err", err)
		}
	}
	return content.ResolveSiteCopy(stored)
}

// A copyField is one line of a front end's own wording for the admin form.
type copyField struct {
	Key, Label, Default, Value string
}

// frontendCopyFields reads the keys a revision's copy.json declares, as
// {"key": "default"} or {"key": {"label": "...", "default": "..."}}, sorted
// by key, with the front end's current edits.
func (s *Server) frontendCopyFields(ctx context.Context, revID string, edits map[string]string) []copyField {
	if revID == "" {
		return nil
	}
	files, err := s.builder.files.Read(ctx, revID)
	if err != nil {
		return nil
	}
	raw, ok := files["copy.json"]
	if !ok {
		return nil
	}
	var decl map[string]json.RawMessage
	if json.Unmarshal(raw, &decl) != nil {
		return nil
	}
	var out []copyField
	for k, v := range decl {
		if !content.CopyKey.MatchString(k) {
			continue
		}
		f := copyField{Key: k, Label: k, Value: edits[k]}
		var def string
		var obj struct{ Label, Default string }
		if json.Unmarshal(v, &def) == nil {
			f.Default = def
		} else if json.Unmarshal(v, &obj) == nil {
			f.Default = obj.Default
			if strings.TrimSpace(obj.Label) != "" {
				f.Label = obj.Label
			}
		}
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b copyField) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// builderCopy saves Ben's edits to a front end's own wording: one form field
// per key its active revision's copy.json declares ("copy.<key>"). Empty, or
// the default, means "use the default".
func (s *Server) builderCopy(w http.ResponseWriter, r *http.Request) {
	b := s.needBuilderStore(w)
	if b == nil {
		return
	}
	slug := r.PathValue("slug")
	f, err := b.BuilderFrontend(r.Context(), frontend.PromptedID(slug))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "builder: copy", err)
		return
	}
	edits := map[string]string{}
	for _, fld := range s.frontendCopyFields(r.Context(), f.ActiveRevision, f.Copy) {
		v := strings.TrimSpace(r.FormValue("copy." + fld.Key))
		if v != "" && v != fld.Default && len(v) <= content.MaxCopyLen {
			edits[fld.Key] = v
		}
	}
	if err := b.SetFrontendCopy(r.Context(), f.ID, edits); err != nil {
		s.fail(w, "builder: copy", err)
		return
	}
	s.contentChanged()
	http.Redirect(w, r, "/admin/builder/fe/"+slug+"#copy", http.StatusSeeOther)
}

// adminSiteCopy saves the site-wide copy from the dashboard form
// ("copy.<key>" for each content.SiteCopy line; empty = the default).
func (s *Server) adminSiteCopy(w http.ResponseWriter, r *http.Request) {
	cs := store.CopyOf(s.store)
	if cs == nil {
		http.Error(w, "this store can't keep copy", http.StatusServiceUnavailable)
		return
	}
	lines := map[string]string{}
	for _, l := range content.SiteCopy {
		v := strings.Join(strings.Fields(r.FormValue("copy."+l.Key)), " ")
		if v == l.Default || len(v) > content.MaxCopyLen {
			v = ""
		}
		lines[l.Key] = v
	}
	if err := cs.SetSiteCopy(r.Context(), lines); err != nil {
		s.fail(w, "admin: site copy", err)
		return
	}
	s.contentChanged()
	http.Redirect(w, r, "/admin/#site-copy", http.StatusSeeOther)
}

// A siteCopyField is one site-wide line for the dashboard form.
type siteCopyField struct {
	content.CopyLine
	Value string // the stored value; "" = the default
}

func (s *Server) siteCopyFields(ctx context.Context) []siteCopyField {
	var stored map[string]string
	if cs := store.CopyOf(s.store); cs != nil {
		stored, _ = cs.SiteCopy(ctx)
	}
	out := make([]siteCopyField, 0, len(content.SiteCopy))
	for _, l := range content.SiteCopy {
		out = append(out, siteCopyField{CopyLine: l, Value: stored[l.Key]})
	}
	return out
}

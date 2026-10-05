package server

import (
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

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

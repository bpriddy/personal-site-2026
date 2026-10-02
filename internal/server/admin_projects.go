package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// projectsStore is the store's Projects; nil (and a 501 from the admin) if
// it has none.
func (s *Server) projectsStore(w http.ResponseWriter) store.Projects {
	ps := store.ProjectsOf(s.store)
	if ps == nil {
		http.Error(w, "this store has no projects", http.StatusNotImplemented)
	}
	return ps
}

// adminProjectNew is the dashboard's "New project" form: ?slug= → its form.
func (s *Server) adminProjectNew(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.URL.Query().Get("slug"))
	if !content.ValidSlug(slug) {
		http.Error(w, "a project slug is lowercase letters, digits and dashes", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/projects/"+slug, http.StatusSeeOther)
}

// adminProjectForm edits a project; an unknown (valid) slug starts a new one.
func (s *Server) adminProjectForm(w http.ResponseWriter, r *http.Request) {
	ps := s.projectsStore(w)
	if ps == nil {
		return
	}
	slug := r.PathValue("slug")
	if !content.ValidSlug(slug) {
		http.NotFound(w, r)
		return
	}
	p, err := ps.Project(r.Context(), slug)
	isNew := errors.Is(err, store.ErrNotFound)
	if isNew {
		p = content.Project{Slug: slug, Order: 100}
	} else if err != nil {
		s.fail(w, "project", err)
		return
	}
	s.render(w, "admin/project_form.html", http.StatusOK, map[string]any{"Project": p, "New": isNew, "Error": ""})
}

// adminProjectSave saves the edit form: every text field, tags/roles/palette
// as comma lists, order and published. Media isn't edited here (it comes from
// the import), so it is kept as it is.
func (s *Server) adminProjectSave(w http.ResponseWriter, r *http.Request) {
	ps := s.projectsStore(w)
	if ps == nil {
		return
	}
	slug := r.PathValue("slug")
	if !content.ValidSlug(slug) {
		http.NotFound(w, r)
		return
	}
	p, err := ps.Project(r.Context(), slug)
	isNew := errors.Is(err, store.ErrNotFound)
	if isNew {
		p = content.Project{Slug: slug}
	} else if err != nil {
		s.fail(w, "project", err)
		return
	}
	p.Title = strings.TrimSpace(r.FormValue("title"))
	p.Client = strings.TrimSpace(r.FormValue("client"))
	p.Agency = strings.TrimSpace(r.FormValue("agency"))
	p.Year = strings.TrimSpace(r.FormValue("year"))
	p.Summary = strings.TrimSpace(r.FormValue("summary"))
	p.Contribution = strings.TrimSpace(r.FormValue("contribution"))
	p.Body = strings.TrimSpace(r.FormValue("body"))
	p.Link = strings.TrimSpace(r.FormValue("link"))
	p.YouTube = strings.TrimSpace(r.FormValue("youtube"))
	p.Tags = commaList(r.FormValue("tags"))
	p.Roles = commaList(r.FormValue("roles"))
	p.Palette = commaList(r.FormValue("palette"))
	p.Order, _ = strconv.Atoi(strings.TrimSpace(r.FormValue("order")))
	p.Published = r.FormValue("published") == "on"
	if problems := validateProject(p); len(problems) > 0 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		s.render(w, "admin/project_form.html", http.StatusBadRequest, map[string]any{
			"Project": p, "New": isNew, "Error": strings.Join(problems, "; "),
		})
		return
	}
	if err := ps.SaveProject(r.Context(), p); err != nil {
		s.fail(w, "save project", err)
		return
	}
	s.contentChanged()
	http.Redirect(w, r, "/admin/#projects", http.StatusSeeOther)
}

// adminProjectPublish is the dashboard's publish toggle: published=1|0.
func (s *Server) adminProjectPublish(w http.ResponseWriter, r *http.Request) {
	ps := s.projectsStore(w)
	if ps == nil {
		return
	}
	p, err := ps.Project(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "project", err)
		return
	}
	p.Published = r.FormValue("published") == "1"
	if err := ps.SaveProject(r.Context(), p); err != nil {
		s.fail(w, "save project", err)
		return
	}
	s.contentChanged()
	http.Redirect(w, r, "/admin/#projects", http.StatusSeeOther)
}

// commaList splits "a, b,,c" into ["a", "b", "c"].
func commaList(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// validateProject lists what's wrong with p (nothing for a valid project).
func validateProject(p content.Project) []string {
	var out []string
	if !content.ValidSlug(p.Slug) {
		out = append(out, fmt.Sprintf("slug %q: lowercase letters, digits and dashes", p.Slug))
	}
	if !content.ValidLink(p.Link) {
		out = append(out, fmt.Sprintf("%s: link must be an http(s) URL", p.Slug))
	}
	if p.YouTube != "" && !content.ValidYouTube(p.YouTube) {
		out = append(out, fmt.Sprintf("%s: youtube must be a video id (11 characters), not a URL", p.Slug))
	}
	for _, c := range p.Palette {
		if !content.ValidHex(c) {
			out = append(out, fmt.Sprintf("%s: palette colour %q must be #rrggbb", p.Slug, c))
		}
	}
	for i, m := range p.Media {
		if !m.Valid() {
			out = append(out, fmt.Sprintf("%s: media %d must be an image or loop under /media/ with a known file type", p.Slug, i+1))
		}
	}
	return out
}

// ── import (POST /admin/import/projects) ───────────────────────────────────

// maxImportBytes bounds the import body.
const maxImportBytes = 4 << 20

// importFile is the import's JSON shape (the old site's normalized export).
// Unknown fields are ignored.
type importFile struct {
	Projects []struct {
		Slug         string          `json:"slug"`
		Title        string          `json:"title"`
		Client       string          `json:"client"`
		Agency       string          `json:"agency"`
		Year         string          `json:"year"`
		Tags         []string        `json:"tags"`
		Roles        []string        `json:"roles"`
		Summary      string          `json:"summary"`
		Contribution string          `json:"contribution"`
		Body         string          `json:"body"`
		Link         string          `json:"link"`
		Palette      []string        `json:"palette"`
		YouTube      string          `json:"youtube"`
		Media        []content.Media `json:"media"`
		Order        int             `json:"order"`
		Published    bool            `json:"published"`
	} `json:"projects"`
	Experiments []struct {
		Slug      string          `json:"slug"`
		Title     string          `json:"title"`
		Summary   string          `json:"summary"`
		Link      string          `json:"link"`
		Media     []content.Media `json:"media"`
		Order     *int            `json:"order"`
		Published *bool           `json:"published"`
	} `json:"experiments"`
}

// importCounts is what an import did to one collection.
type importCounts struct {
	Created   []string `json:"created"`
	Updated   []string `json:"updated"`
	Unchanged []string `json:"unchanged"`
}

// adminImportProjects upserts projects, and experiments' link and media,
// from an import file (JSON body). Idempotent: an item already equal to its
// import is left alone, so importing the same file again changes nothing.
// It never touches pages. All or nothing: any invalid item rejects the whole
// file (400, with every problem listed) before anything is written.
//
//   - A project is replaced by its import (every field; slug is the key).
//   - An existing experiment gets the import's link and media only; its
//     title, summary, order and publication stay as edited in the admin.
//   - A new experiment is created from the import (published unless the
//     import says "published": false; ordered after the existing ones).
func (s *Server) adminImportProjects(w http.ResponseWriter, r *http.Request) {
	ps := s.projectsStore(w)
	if ps == nil {
		return
	}
	if mt := r.Header.Get("Content-Type"); !strings.HasPrefix(mt, "application/json") {
		http.Error(w, "send the import as application/json", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBytes))
	if err != nil {
		http.Error(w, "import too large", http.StatusRequestEntityTooLarge)
		return
	}
	var in importFile
	if err := json.Unmarshal(body, &in); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	// validate everything first
	var problems []string
	var projects []content.Project
	seen := map[string]bool{}
	for _, ip := range in.Projects {
		p := content.Project{
			Slug: ip.Slug, Title: strings.TrimSpace(ip.Title), Client: strings.TrimSpace(ip.Client),
			Agency: strings.TrimSpace(ip.Agency), Year: strings.TrimSpace(ip.Year),
			Tags: commaListOf(ip.Tags), Roles: commaListOf(ip.Roles),
			Summary: strings.TrimSpace(ip.Summary), Contribution: strings.TrimSpace(ip.Contribution),
			Body: strings.TrimSpace(ip.Body), Link: strings.TrimSpace(ip.Link),
			Palette: commaListOf(ip.Palette), YouTube: strings.TrimSpace(ip.YouTube),
			Media: slices.Clone(ip.Media), Order: ip.Order, Published: ip.Published,
		}
		if p.Media == nil {
			p.Media = []content.Media{}
		}
		if seen[p.Slug] {
			problems = append(problems, fmt.Sprintf("project %q appears twice", p.Slug))
		}
		seen[p.Slug] = true
		problems = append(problems, validateProject(p)...)
		projects = append(projects, p)
	}
	type expImport struct {
		content.Experiment
		order, published bool // set by the import
	}
	var exps []expImport
	seenExp := map[string]bool{}
	for _, ie := range in.Experiments {
		e := expImport{Experiment: content.Experiment{
			Slug: ie.Slug, Title: strings.TrimSpace(ie.Title), Summary: strings.TrimSpace(ie.Summary),
			Link: strings.TrimSpace(ie.Link), Media: slices.Clone(ie.Media), Published: true,
		}}
		if e.Media == nil {
			e.Media = []content.Media{}
		}
		if ie.Order != nil {
			e.Order, e.order = *ie.Order, true
		}
		if ie.Published != nil {
			e.Published, e.published = *ie.Published, true
		}
		if !content.ValidSlug(e.Slug) {
			problems = append(problems, fmt.Sprintf("experiment slug %q: lowercase letters, digits and dashes", e.Slug))
		}
		if seenExp[e.Slug] {
			problems = append(problems, fmt.Sprintf("experiment %q appears twice", e.Slug))
		}
		seenExp[e.Slug] = true
		if !content.ValidLink(e.Link) {
			problems = append(problems, fmt.Sprintf("experiment %s: link must be an http(s) URL", e.Slug))
		}
		for i, m := range e.Media {
			if !m.Valid() {
				problems = append(problems, fmt.Sprintf("experiment %s: media %d must be an image or loop under /media/ with a known file type", e.Slug, i+1))
			}
		}
		exps = append(exps, e)
	}
	if len(problems) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "nothing imported", "problems": problems})
		return
	}

	// then write what differs
	var res struct {
		Projects    importCounts `json:"projects"`
		Experiments importCounts `json:"experiments"`
	}
	res.Projects = importCounts{Created: []string{}, Updated: []string{}, Unchanged: []string{}}
	res.Experiments = importCounts{Created: []string{}, Updated: []string{}, Unchanged: []string{}}
	for _, p := range projects {
		cur, err := ps.Project(ctx, p.Slug)
		switch {
		case errors.Is(err, store.ErrNotFound):
			res.Projects.Created = append(res.Projects.Created, p.Slug)
		case err != nil:
			s.fail(w, "import: project", err)
			return
		default:
			p.UpdatedAt = cur.UpdatedAt
			if reflect.DeepEqual(cur, p) {
				res.Projects.Unchanged = append(res.Projects.Unchanged, p.Slug)
				continue
			}
			res.Projects.Updated = append(res.Projects.Updated, p.Slug)
		}
		if err := ps.SaveProject(ctx, p); err != nil {
			s.fail(w, "import: save project", err)
			return
		}
	}
	existing, err := s.store.Experiments(ctx)
	if err != nil {
		s.fail(w, "import: experiments", err)
		return
	}
	nextOrder := 0
	for _, e := range existing {
		nextOrder = max(nextOrder, e.Order+1)
	}
	for _, e := range exps {
		cur, err := s.store.Experiment(ctx, e.Slug)
		switch {
		case errors.Is(err, store.ErrNotFound):
			if !e.order {
				e.Order = nextOrder
				nextOrder++
			}
			if err := s.store.SaveExperiment(ctx, e.Experiment); err != nil {
				s.fail(w, "import: save experiment", err)
				return
			}
			res.Experiments.Created = append(res.Experiments.Created, e.Slug)
			continue
		case err != nil:
			s.fail(w, "import: experiment", err)
			return
		}
		if cur.Link == e.Link && reflect.DeepEqual(cur.Media, e.Media) {
			res.Experiments.Unchanged = append(res.Experiments.Unchanged, e.Slug)
			continue
		}
		cur.Link, cur.Media = e.Link, e.Media
		if err := s.store.SaveExperiment(ctx, cur); err != nil {
			s.fail(w, "import: save experiment", err)
			return
		}
		res.Experiments.Updated = append(res.Experiments.Updated, e.Slug)
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, res)
}

// commaListOf trims a list and drops blank entries; never nil.
func commaListOf(l []string) []string {
	out := []string{}
	for _, s := range l {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

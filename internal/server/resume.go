package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

// /resume (protocol v1.14): one fixed page, the same whichever front end a
// visitor got: Ben's career as a connected system (roles, projects, what
// they were about, his skills) over the plain resume, and /resume.pdf from
// the same data. Roles and projects come from the CMS; the rest is
// content.Resume (admin: /admin/resume), defaulting to his CV.

// resumeView is everything the page and the PDF show.
type resumeView struct {
	Name, Location, Email, LinkedIn string
	Profile                         []string // paragraphs
	SolvingTitle                    string
	Solving                         []string
	Groups                          []resumeGroup
	SkillsDo, SkillsReview          []string
	Graph                           resumeGraph
}

// resumeGroup is one employer: consecutive roles there, with their bullets.
type resumeGroup struct {
	Company string
	Roles   []resumeRole
	Bullets []string
}

type resumeRole struct {
	Slug, Title, Dates string
}

// resumeGraph is the system the page draws: nodes and the links between them.
type resumeGraph struct {
	Nodes []graphNode `json:"nodes"`
	Links [][2]string `json:"links"`
}

type graphNode struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"` // me | role | project | theme | skill
	Label   string   `json:"label"`
	Sub     string   `json:"sub,omitempty"`
	Text    string   `json:"text,omitempty"`
	Bullets []string `json:"bullets,omitempty"`
	Href    string   `json:"href,omitempty"`
}

// resumeContent is the stored resume, or the CV defaults.
func (s *Server) resumeContent(ctx context.Context) content.Resume {
	if rs := store.ResumeOf(s.store); rs != nil {
		if r, ok, err := rs.Resume(ctx); err == nil && ok {
			return r
		} else if err != nil {
			s.log.Error("resume", "err", err)
		}
	}
	return content.DefaultResume()
}

func (s *Server) resumeView(ctx context.Context) (resumeView, error) {
	r := s.resumeContent(ctx)
	roles, err := s.publishedExperience(ctx)
	if err != nil {
		return resumeView{}, err
	}
	projects, err := s.publishedProjects(ctx)
	if err != nil {
		return resumeView{}, err
	}
	name := "Ben Priddy"
	var bio []string
	if home, err := s.store.Page(ctx, ""); err == nil {
		if home.Title != "" {
			name = home.Title
		}
		bio = paragraphs(home.Body)
	}
	v := resumeView{Name: name, Location: r.Location, Email: r.Email, LinkedIn: r.LinkedIn,
		Profile: tidy(paragraphs(r.Profile)), SolvingTitle: strings.TrimSpace(r.SolvingTitle), Solving: tidy(paragraphs(r.SolvingBody)),
		SkillsDo: content.Lines(r.SkillsDo), SkillsReview: content.Lines(r.SkillsReview)}
	// "Currently solving" defaults to the site's own words: the bio after its first paragraph
	if len(v.Solving) == 0 && len(bio) > 1 {
		v.Solving = tidy(bio[1:])
	}
	for _, e := range roles {
		dates := e.StartLabel + " to "
		if e.Current {
			dates += "Present"
		} else {
			dates += e.EndLabel
		}
		role := resumeRole{Slug: e.Slug, Title: e.Role, Dates: dates}
		if n := len(v.Groups); n > 0 && v.Groups[n-1].Company == e.Company {
			v.Groups[n-1].Roles = append(v.Groups[n-1].Roles, role)
		} else {
			v.Groups = append(v.Groups, resumeGroup{Company: e.Company, Roles: []resumeRole{role}})
		}
		g := &v.Groups[len(v.Groups)-1]
		g.Bullets = append(g.Bullets, content.Lines(r.Bullets[e.Slug])...)
	}
	v.Graph = buildResumeGraph(v, roles, projects)
	return v, nil
}

// tidy collapses runs of spaces inside each paragraph.
func tidy(ps []string) []string {
	for i, p := range ps {
		ps[i] = strings.Join(strings.Fields(p), " ")
	}
	return ps
}

// startYear is the first year in "2020", "2019-2022", "2026-03"; 0 if none.
func startYear(s string) int {
	if len(s) < 4 {
		return 0
	}
	y, err := strconv.Atoi(s[:4])
	if err != nil {
		return 0
	}
	return y
}

// buildResumeGraph links Ben to his roles and skills, each role to the
// projects of its years (the earliest-starting role whose years cover the
// project's start), and each project to its tags.
func buildResumeGraph(v resumeView, roles []experienceView, projects []projectView) resumeGraph {
	g := resumeGraph{Links: [][2]string{}}
	add := func(n graphNode) { g.Nodes = append(g.Nodes, n) }
	link := func(a, b string) { g.Links = append(g.Links, [2]string{a, b}) }
	me := graphNode{ID: "me", Kind: "me", Label: v.Name, Text: strings.Join(v.Profile, "\n\n")}
	if len(roles) > 0 {
		me.Sub = roles[0].Role + " · " + roles[0].Company
	}
	add(me)
	bullets := map[string][]string{}
	for _, gr := range v.Groups {
		// a group's bullets belong to its first (newest) role
		bullets[gr.Roles[0].Slug] = gr.Bullets
	}
	type span struct {
		id       string
		from, to int
	}
	var spans []span
	for _, e := range roles {
		id := "role:" + e.Slug
		dates := e.StartLabel + " – "
		to := startYear(e.End)
		if e.Current {
			dates += "Present"
			to = 9999
		} else {
			dates += e.EndLabel
		}
		add(graphNode{ID: id, Kind: "role", Label: e.Role, Sub: e.Company + " · " + dates, Text: e.Note, Bullets: bullets[e.Slug]})
		link("me", id)
		spans = append(spans, span{id, startYear(e.Start), to})
	}
	themes := map[string]string{} // lower-case tag → node id
	for _, p := range projects {
		id := "project:" + p.Slug
		add(graphNode{ID: id, Kind: "project", Label: p.Title, Sub: p.Meta, Text: p.Summary, Href: p.Href})
		y, best := startYear(p.Year), ""
		bestFrom := 0
		for _, sp := range spans {
			if y >= sp.from && y <= sp.to && (best == "" || sp.from < bestFrom) {
				best, bestFrom = sp.id, sp.from
			}
		}
		if best != "" {
			link(best, id)
		}
		for _, t := range p.Tags {
			key := strings.ToLower(strings.TrimSpace(t))
			if key == "" {
				continue
			}
			tid, ok := themes[key]
			if !ok {
				tid = "theme:" + key
				themes[key] = tid
				add(graphNode{ID: tid, Kind: "theme", Label: strings.TrimSpace(t)})
			}
			link(id, tid)
		}
	}
	// his skills: two nodes, each opening its list
	for _, sk := range []struct {
		id, label string
		items     []string
	}{{"skill:do", "What I can do", v.SkillsDo}, {"skill:review", "What I can review", v.SkillsReview}} {
		if len(sk.items) == 0 {
			continue
		}
		add(graphNode{ID: sk.id, Kind: "skill", Label: sk.label, Sub: "Technical skills", Bullets: sk.items})
		link("me", sk.id)
	}
	return g
}

// resume is /resume.
func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	v, err := s.resumeView(r.Context())
	if err != nil {
		s.fail(w, "resume", err)
		return
	}
	graph, _ := json.Marshal(v.Graph)
	w.Header().Set("Content-Security-Policy", s.publicCSP)
	s.render(w, "fixed/resume.html", http.StatusOK, map[string]any{
		"R": v, "GraphJSON": template.JS(graph), // json.Marshal escapes <, > and & "SiteURL": strings.TrimRight(s.cfg.MainOrigin, "/"),
		"Canonical": strings.TrimRight(s.cfg.MainOrigin, "/") + "/resume",
	})
}

// adminResumeForm is GET /admin/resume.
func (s *Server) adminResumeForm(w http.ResponseWriter, r *http.Request) {
	s.renderResumeForm(w, r, s.resumeContent(r.Context()), "")
}

func (s *Server) renderResumeForm(w http.ResponseWriter, r *http.Request, res content.Resume, problem string) {
	roles, err := s.publishedExperience(r.Context())
	if err != nil {
		s.fail(w, "admin: resume", err)
		return
	}
	status := http.StatusOK
	if problem != "" {
		status = http.StatusBadRequest
	}
	s.render(w, "admin/resume_form.html", status, map[string]any{"R": res, "Roles": roles, "Error": problem})
}

// adminResume is POST /admin/resume.
func (s *Server) adminResume(w http.ResponseWriter, r *http.Request) {
	rs := store.ResumeOf(s.store)
	if rs == nil {
		http.Error(w, "this store can't keep the resume", http.StatusServiceUnavailable)
		return
	}
	norm := func(k string) string { return strings.TrimSpace(strings.ReplaceAll(r.FormValue(k), "\r\n", "\n")) }
	res := content.Resume{Profile: norm("profile"), SolvingTitle: norm("solvingTitle"), SolvingBody: norm("solvingBody"),
		SkillsDo: norm("skillsDo"), SkillsReview: norm("skillsReview"), Email: norm("email"), LinkedIn: norm("linkedin"),
		Location: norm("location"), Bullets: map[string]string{}}
	if err := r.ParseForm(); err == nil {
		for k := range r.PostForm {
			if slug, ok := strings.CutPrefix(k, "bullets."); ok && slug != "" {
				if b := norm(k); b != "" {
					res.Bullets[slug] = b
				}
			}
		}
	}
	if err := res.Validate(); err != nil {
		s.renderResumeForm(w, r, res, err.Error())
		return
	}
	if err := rs.SetResume(r.Context(), res); err != nil {
		s.fail(w, "admin: resume", err)
		return
	}
	http.Redirect(w, r, "/admin/resume", http.StatusSeeOther)
}

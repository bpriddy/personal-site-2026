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
	Photo, PhotoSet                 string // the centre node's picture; a srcset when there are sizes
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
	Year    int      `json:"year,omitempty"`  // a role's or project's start year
	Group   string   `json:"group,omitempty"` // a skill's constellation: "do" | "review"
	Hub     bool     `json:"hub,omitempty"`   // a constellation's own node
	Img     string   `json:"img,omitempty"`   // me: the photo
	ImgSet  string   `json:"imgset,omitempty"`
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
		SkillsDo: content.Lines(r.SkillsDo), SkillsReview: content.Lines(r.SkillsReview), Photo: r.PhotoOrDefault()}
	if v.Photo == content.DefaultPhoto {
		v.PhotoSet = "/static/resume/ben-480.webp 480w, /static/resume/ben-960.webp 960w"
	}
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
// projects of its years (of the roles whose years cover the project's start,
// one at the project's agency or client if there is one, else the
// earliest-starting: years are coarse, and a project that started in 2023
// at Anomaly isn't Active Theory's, whose years also cover 2023), each
// project to its tags, and his skills as two constellations (skillLinks).
func buildResumeGraph(v resumeView, roles []experienceView, projects []projectView) resumeGraph {
	g := resumeGraph{Links: [][2]string{}}
	add := func(n graphNode) { g.Nodes = append(g.Nodes, n) }
	link := func(a, b string) { g.Links = append(g.Links, [2]string{a, b}) }
	me := graphNode{ID: "me", Kind: "me", Label: v.Name, Text: strings.Join(v.Profile, "\n\n"), Img: v.Photo, ImgSet: v.PhotoSet}
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
		id, company string
		from, to    int
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
		add(graphNode{ID: id, Kind: "role", Label: e.Role, Sub: e.Company + " · " + dates, Text: e.Note, Bullets: bullets[e.Slug], Year: startYear(e.Start)})
		link("me", id)
		spans = append(spans, span{id, strings.ToLower(strings.TrimSpace(e.Company)), startYear(e.Start), to})
	}
	themes := map[string]string{} // lower-case tag → node id
	for _, p := range projects {
		id := "project:" + p.Slug
		add(graphNode{ID: id, Kind: "project", Label: p.Title, Sub: p.Meta, Text: p.Summary, Href: p.Href, Year: startYear(p.Year)})
		y, best := startYear(p.Year), ""
		bestFrom, bestAt := 0, false
		for _, sp := range spans {
			if y < sp.from || y > sp.to {
				continue
			}
			at := sp.company != "" && (strings.EqualFold(strings.TrimSpace(p.Agency), sp.company) || strings.EqualFold(strings.TrimSpace(p.Client), sp.company))
			if best == "" || (at && !bestAt) || (at == bestAt && sp.from < bestFrom) {
				best, bestFrom, bestAt = sp.id, sp.from, at
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
	// his skills: two constellations, "What I can do" and "What I can
	// review", each a hub with a node per skill, linked by skillLinks
	current := ""
	if len(roles) > 0 {
		current = "role:" + roles[0].Slug
	}
	for _, sk := range []struct {
		group, label string
		items        []string
	}{{"do", "What I can do", v.SkillsDo}, {"review", "What I can review", v.SkillsReview}} {
		if len(sk.items) == 0 {
			continue
		}
		hub := "skill:" + sk.group
		add(graphNode{ID: hub, Kind: "skill", Label: sk.label, Sub: "Technical skills", Bullets: sk.items, Group: sk.group, Hub: true})
		link("me", hub)
		seen := map[string]bool{}
		for _, item := range sk.items {
			id := hub + "/" + skillSlug(item)
			if seen[id] {
				continue
			}
			seen[id] = true
			add(graphNode{ID: id, Kind: "skill", Label: item, Sub: sk.label, Group: sk.group})
			link(hub, id)
			for _, to := range skillLinks(sk.group, item, current, themes) {
				link(id, to)
			}
		}
	}
	return g
}

// skillSlug is a skill's id part: lower case, runs of anything but letters
// and digits as one "-" ("Three.js" → "three-js").
func skillSlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// reviewThemes says what a "What I can review" skill was used for: a keyword
// in the skill (lower case) → the project themes (tags, lower case) whose
// work used it. Conservative on purpose: a link claims "this skill is in that
// kind of work", so only where Ben's projects make it true:
//   - the web stack (JavaScript, TypeScript, React, Node) → Web; JavaScript
//     also → AR (Play the City is web AR), Node also → Tooling;
//   - Three.js and GLSL (real-time 3D in the browser) → Web, AR and
//     Experiential; GLSL also → Computer Vision (camera effects as shaders);
//   - Python → Applied AI, AI, Tooling, Computer Vision;
//   - databases and cloud infrastructure → Tooling (the internal tools).
//
// Linking to themes rather than single projects keeps it honest: the CMS
// doesn't record which project used which language. A theme no project has
// is skipped; a skill that matches nothing links only to its constellation.
var reviewThemes = []struct {
	keyword string
	themes  []string
}{
	{"javascript", []string{"web", "ar"}},
	{"typescript", []string{"web"}},
	{"react", []string{"web"}},
	{"node", []string{"web", "tooling"}},
	{"three.js", []string{"web", "ar", "experiential"}},
	{"glsl", []string{"web", "ar", "experiential", "computer vision"}},
	{"python", []string{"applied ai", "ai", "tooling", "computer vision"}},
	{"database", []string{"tooling"}},
	{"cloud", []string{"tooling"}},
}

// doThemes: every "What I can do" skill (agentic engineering, the evolving
// coding stack, skills, hardening through loops) is today's work, so each
// links to Ben's current role and to these themes.
var doThemes = []string{"applied ai", "tooling"}

// skillLinks is the ids a skill links to besides its constellation's hub
// (reviewThemes, doThemes). current is the newest role's id ("" if none);
// themes maps lower-case tags to theme ids.
func skillLinks(group, item, current string, themes map[string]string) []string {
	var out []string
	seen := map[string]bool{}
	addTheme := func(tag string) {
		if id, ok := themes[tag]; ok && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if group == "do" {
		if current != "" {
			out = append(out, current)
		}
		for _, t := range doThemes {
			addTheme(t)
		}
		return out
	}
	low := strings.ToLower(item)
	for _, rt := range reviewThemes {
		if strings.Contains(low, rt.keyword) {
			for _, t := range rt.themes {
				addTheme(t)
			}
		}
	}
	return out
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
		"R": v, "GraphJSON": template.JS(graph), // json.Marshal escapes <, > and &
		"SiteURL":   strings.TrimRight(s.cfg.MainOrigin, "/"),
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
		Location: norm("location"), Photo: norm("photo"), Bullets: map[string]string{}}
	// a full address on this site is its path
	if origin := strings.TrimRight(s.cfg.MainOrigin, "/"); origin != "" {
		res.Photo = strings.TrimPrefix(res.Photo, origin)
	}
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

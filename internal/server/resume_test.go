package server

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

func seedResume(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()
	es := store.ExperienceOf(s.store)
	for _, e := range []content.Experience{
		{Slug: "anomaly-global-head-ai", Role: "Global Head of AI Technology", Company: "Anomaly", Start: "2026-03", Current: true, Order: 1, Published: true},
		{Slug: "anomaly-technical-director", Role: "Technical Director, Emerging Technologies", Company: "Anomaly", Start: "2023-06", End: "2026-03", Order: 2, Published: true},
		{Slug: "stink-studios-technical-director", Role: "Technical Director", Company: "Stink Studios", Start: "2019", End: "2022-04", Order: 3, Published: true},
		{Slug: "tool-creative-director-technology", Role: "Creative Director of Technology", Company: "Tool of North America", Start: "2014-09", End: "2019-06", Order: 4, Published: true},
	} {
		if err := es.SaveExperience(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	ps := store.ProjectsOf(s.store)
	for _, p := range []content.Project{
		{Slug: "ai-education", Title: "AI Education", Year: "2023-2026", Tags: []string{"Education", "Applied AI"}, Published: true},
		{Slug: "nytimes", Title: "ad.nytimes.com", Year: "2022", Tags: []string{"Web"}, Published: true},
		{Slug: "play-the-city", Title: "Play the City", Year: "2018", Tags: []string{"AR", "web"}, Published: true},
		// 2019 is in Tool's years and Stink's: the agency decides
		{Slug: "stink-reel", Title: "Stink Reel", Year: "2019", Agency: "Stink Studios", Tags: []string{"Web"}, Published: true},
	} {
		if err := ps.SaveProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	s.store.SavePage(ctx, content.Page{Slug: "", Title: "Ben Priddy", Published: true,
		Body: "I love the creative ways technology shows up in culture.\n\nRight now I'm focused on the first and last mile of enterprise AI.  The real work sits outside the model."})
}

func TestResumePage(t *testing.T) {
	s := newTestServer(t)
	seedResume(t, s)
	rec := get(s, "/resume")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := html.UnescapeString(rec.Body.String())
	for _, want := range []string{
		"<h1 class=\"rz-name\">Ben Priddy</h1>",
		"I love the creative ways technology shows up in culture, and I've built my career", // the profile: CV, corrected
		"helped shape the company",
		"Right now I'm focused on the first and last mile of enterprise AI. The real work", // currently solving: the site's words, tidied
		"Designed and led applied AI education across the network",                         // Anomaly's bullets
		"Mar 2026 to Present", "2019 to Apr 2022",
		"mailto:colorandsound@gmail.com", "linkedin.com/in/ben-priddy",
		`href="/resume.pdf"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, not := range []string{"fifteen years", "helped shaped", "323-387", "How I learned to stop waiting"} {
		if strings.Contains(body, not) {
			t.Errorf("should not contain %q", not)
		}
	}
	// Anomaly's two titles are one group
	if n := strings.Count(body, `<p class="rz-company">Anomaly</p>`); n != 1 {
		t.Errorf("Anomaly groups: %d", n)
	}
	// the graph: raw JSON, projects under the role of their years
	m := regexp.MustCompile(`(?s)<script type="application/json" id="rz-graph">(.*?)</script>`).FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatal("no graph JSON")
	}
	var g resumeGraph
	if err := json.Unmarshal([]byte(m[1]), &g); err != nil {
		t.Fatalf("graph JSON: %v\n%s", err, m[1][:min(200, len(m[1]))])
	}
	has := func(a, b string) bool {
		for _, l := range g.Links {
			if l[0] == a && l[1] == b {
				return true
			}
		}
		return false
	}
	for _, l := range [][2]string{
		{"role:anomaly-technical-director", "project:ai-education"},
		{"role:stink-studios-technical-director", "project:nytimes"},
		{"role:tool-creative-director-technology", "project:play-the-city"},
		{"project:play-the-city", "theme:web"}, // tags merge case-insensitively
		{"project:nytimes", "theme:web"},
		{"me", "role:anomaly-global-head-ai"},
		{"role:stink-studios-technical-director", "project:stink-reel"}, // its agency, not the earliest role
		// the skills: two constellations, a node per skill
		{"me", "skill:do"}, {"me", "skill:review"},
		{"skill:do", "skill:do/agentic-engineering"},
		{"skill:review", "skill:review/three-js"},
		// linked to the work they belong to (skillLinks)
		{"skill:do/agentic-engineering", "role:anomaly-global-head-ai"},
		{"skill:do/agentic-engineering", "theme:applied ai"},
		{"skill:review/three-js", "theme:web"},
		{"skill:review/three-js", "theme:ar"},
		{"skill:review/javascript", "theme:web"},
		{"skill:review/python", "theme:applied ai"},
	} {
		if !has(l[0], l[1]) {
			t.Errorf("missing link %v", l)
		}
	}
	for _, l := range [][2]string{
		{"role:tool-creative-director-technology", "project:stink-reel"},
		{"skill:review/typescript", "theme:ar"}, // conservative: TypeScript is Web only
		{"skill:review/python", "theme:web"},    // nor is Python web work
		{"skill:do/agentic-engineering", "theme:web"},
	} {
		if has(l[0], l[1]) {
			t.Errorf("unexpected link %v", l)
		}
	}
	nodes := map[string]graphNode{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	if n := nodes["me"]; n.Img != content.DefaultPhoto || !strings.Contains(n.ImgSet, "ben-960.webp 960w") {
		t.Errorf("me: photo %q %q", n.Img, n.ImgSet)
	}
	skills := 0
	for _, n := range g.Nodes {
		if n.Kind == "skill" && !n.Hub {
			skills++
			if n.Group != "do" && n.Group != "review" {
				t.Errorf("skill %s: group %q", n.ID, n.Group)
			}
		}
	}
	def := content.DefaultResume()
	if want := len(content.Lines(def.SkillsDo)) + len(content.Lines(def.SkillsReview)); skills != want {
		t.Errorf("skill nodes: %d, want %d", skills, want)
	}
	if h := nodes["skill:review"]; !h.Hub || h.Label != "What I can review" {
		t.Errorf("review hub: %+v", h)
	}
	if r := nodes["role:stink-studios-technical-director"]; r.Year != 2019 {
		t.Errorf("role year %d", r.Year)
	}
	// every link joins two nodes that exist
	for _, l := range g.Links {
		if _, ok := nodes[l[0]]; !ok {
			t.Errorf("link from missing %s", l[0])
		}
		if _, ok := nodes[l[1]]; !ok {
			t.Errorf("link to missing %s", l[1])
		}
	}
	// the photo is served
	if rec := get(s, content.DefaultPhoto); rec.Code != 200 {
		t.Errorf("photo: %d", rec.Code)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP %q", csp)
	}
}

func TestResumePDF(t *testing.T) {
	s := newTestServer(t)
	seedResume(t, s)
	rec := get(s, "/resume.pdf")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Fatalf("pdf: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String()[:min(20, rec.Body.Len())])
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "Ben_Priddy_Resume.pdf") {
		t.Errorf("disposition %q", rec.Header().Get("Content-Disposition"))
	}
	// the photo by the name
	if !strings.Contains(rec.Body.String(), "/Subtype /Image") {
		t.Error("the PDF has no photo")
	}
	// a photo the PDF can't use (WebP, or missing) costs the photo, not the PDF
	if p := s.resumePhoto(context.Background(), "/static/resume/ben-480.webp"); p != nil {
		t.Error("WebP photo for the PDF")
	}
	if p := s.resumePhoto(context.Background(), "/media/assets/uploads/missing.jpg"); p != nil {
		t.Error("missing photo for the PDF")
	}
	v, _ := s.resumeView(context.Background())
	if b, err := renderResumePDF(v, &pdfPhoto{data: []byte("not a jpeg"), typ: "JPG"}); err != nil || !strings.HasPrefix(string(b), "%PDF-") {
		t.Errorf("bad photo: %v", err)
	}
}

func TestAdminResume(t *testing.T) {
	s := newTestServer(t)
	seedResume(t, s)
	post := func(vals url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/admin/resume", strings.NewReader(vals.Encode()))
		req.SetBasicAuth("admin", "pw")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(url.Values{"profile": {"x"}, "linkedin": {"linkedin.com/in/x"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad LinkedIn: %d", rec.Code)
	}
	rec := post(url.Values{"profile": {"A new profile."}, "solvingTitle": {"A headline"}, "solvingBody": {"Own words."},
		"bullets.stink-studios-technical-director": {"- One\r\n- Two"}, "skillsReview": {"Go\nWGSL"},
		"email": {"ben@example.com"}, "linkedin": {"https://www.linkedin.com/in/x"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	body := html.UnescapeString(get(s, "/resume").Body.String())
	for _, want := range []string{"A new profile.", "A headline", "Own words.", "<li>One</li><li>Two</li>", "Go · WGSL", "mailto:ben@example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("after saving, missing %q", want)
		}
	}
	if strings.Contains(body, "Right now I'm focused") {
		t.Error("own Currently solving text should replace the bio's")
	}

	// the profile photo: a path on this site (a full address here is cut to its path)
	for _, bad := range []string{"https://example.com/me.jpg", "//example.com/me.jpg", "/media/../etc/passwd.jpg", "/static/resume/ben.svg", "javascript:alert(1)"} {
		if rec := post(url.Values{"photo": {bad}}); rec.Code != http.StatusBadRequest {
			t.Errorf("photo %q: %d", bad, rec.Code)
		}
	}
	if rec := post(url.Values{"photo": {"http://localhost:8080/media/assets/uploads/abc123.jpg"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("save photo: %d %s", rec.Code, rec.Body)
	}
	if r, _, _ := store.ResumeOf(s.store).Resume(context.Background()); r.Photo != "/media/assets/uploads/abc123.jpg" {
		t.Errorf("stored photo %q", r.Photo)
	}
	page := get(s, "/resume").Body.String()
	if !strings.Contains(page, `"img":"/media/assets/uploads/abc123.jpg"`) || strings.Contains(page, `"imgset"`) {
		t.Error("the centre node should show the new photo, without the default's sizes")
	}
	req := httptest.NewRequest("GET", "/admin/resume", nil)
	req.SetBasicAuth("admin", "pw")
	form := httptest.NewRecorder()
	s.ServeHTTP(form, req)
	if !strings.Contains(form.Body.String(), `name="photo" value="/media/assets/uploads/abc123.jpg"`) {
		t.Errorf("admin form: no photo field with the saved photo (%d)", form.Code)
	}
	// cleared, it's the default crop again
	if rec := post(url.Values{"photo": {""}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("clear photo: %d", rec.Code)
	}
	if !strings.Contains(get(s, "/resume").Body.String(), `"img":"/static/resume/ben-480.jpg"`) {
		t.Error("a cleared photo should fall back to the default")
	}
}

func TestValidPhotoPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/static/resume/ben-480.jpg":                         true,
		"/static/resume/ben-960.webp":                        true,
		"/media/assets/uploads/d20994ceb1e7aa05e54c7def.jpg": true,
		"/media/projects/x/hero.png":                         true,
		"":                                                   false,
		"ben.jpg":                                            false,
		"/static/../secret.jpg":                              false,
		"/static/resume/.hidden.jpg":                         false,
		"/static/resume/ben.svg":                             false,
		"/static/resume/ben.jpg?x=1":                         false,
		"https://benpriddy.com/static/resume/ben-480.jpg":    false,
		"//evil.example/x.jpg":                               false,
		"/other/x.jpg":                                       false,
		"/media/x.mp4":                                       false,
	} {
		if got := content.ValidPhotoPath(p); got != want {
			t.Errorf("ValidPhotoPath(%q) = %v, want %v", p, got, want)
		}
	}
}

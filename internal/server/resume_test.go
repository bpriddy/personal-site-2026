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
	} {
		if !has(l[0], l[1]) {
			t.Errorf("missing link %v", l)
		}
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
}

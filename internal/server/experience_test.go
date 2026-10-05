package server

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/content"
	"github.com/bpriddy/personal-site-2026/internal/store"
)

func TestMonthLabel(t *testing.T) {
	for in, want := range map[string]string{"2026-03": "Mar 2026", "2019": "2019", "2014-09": "Sep 2014", "": "", "2020-13": "", "March": ""} {
		if got := monthLabel(in); got != want {
			t.Errorf("monthLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHomeShowsPublishedExperience(t *testing.T) {
	s := newTestServer(t)
	es := store.ExperienceOf(s.store)
	ctx := context.Background()
	es.SaveExperience(ctx, content.Experience{Slug: "a", Role: "Global Head of AI Technology", Company: "Anomaly", Start: "2026-03", Current: true, Order: 1, Published: true})
	es.SaveExperience(ctx, content.Experience{Slug: "b", Role: "Technical Director", Company: "Stink Studios", Start: "2020-02", End: "2022-04", Order: 2, Published: true})
	es.SaveExperience(ctx, content.Experience{Slug: "c", Role: "Secret draft", Company: "Hidden", Order: 3})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	body, _ := io.ReadAll(rec.Body)
	page := string(body)
	for _, want := range []string{"( Experience )", "Global Head of AI Technology", `<time datetime="2026-03">Mar 2026</time> to present`,
		`<time datetime="2020-02">Feb 2020</time> to <time datetime="2022-04">Apr 2022</time>`, "Stink Studios"} {
		if !strings.Contains(page, want) {
			t.Errorf("home is missing %q", want)
		}
	}
	if strings.Contains(page, "Secret draft") {
		t.Error("an unpublished role is on the home page")
	}
	if strings.Index(page, "Global Head") > strings.Index(page, "Stink Studios") {
		t.Error("roles out of order")
	}
}

func TestImportExperience(t *testing.T) {
	s := newTestServer(t)
	body := `{"projects": [], "experience": [
		{"slug": "anomaly-global-head-ai", "role": "Global Head of AI Technology", "company": "Anomaly", "start": "2026-03", "current": true, "order": 1, "published": false},
		{"slug": "tool", "role": "Creative Director of Technology", "company": "Tool of North America", "start": "2014-09", "end": "2019-06", "order": 5}
	]}`
	res := importProjects(t, s, body)
	if len(res["experience"]["created"]) != 2 {
		t.Fatalf("first import: %v", res["experience"])
	}
	if res = importProjects(t, s, body); len(res["experience"]["unchanged"]) != 2 {
		t.Fatalf("re-import should change nothing: %v", res["experience"])
	}
	e, err := store.ExperienceOf(s.store).ExperienceItem(context.Background(), "anomaly-global-head-ai")
	if err != nil || !e.Current || e.Start != "2026-03" || e.Published {
		t.Fatalf("stored = %+v, %v", e, err)
	}
	// bad dates and a current role with an end are refused, and nothing is written
	bad := `{"experience": [{"slug": "x", "role": "R", "company": "C", "start": "March 2026"},
		{"slug": "y", "role": "R", "company": "C", "start": "2020", "end": "2021", "current": true}]}`
	rec := workAdmin(s, "POST", "/admin/import/projects", "application/json", bad, nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "YYYY-MM") || !strings.Contains(rec.Body.String(), "no end date") {
		t.Fatalf("bad import: %d %s", rec.Code, rec.Body)
	}
	if _, err := store.ExperienceOf(s.store).ExperienceItem(context.Background(), "x"); err == nil {
		t.Fatal("a rejected import wrote something")
	}
}

func TestAdminExperienceForm(t *testing.T) {
	s := newTestServer(t)
	form := "role=Technical+Director&company=Stink+Studios&start=2020-02&end=2022-04&note=&order=4&published=on"
	rec := workAdmin(s, "POST", "/admin/experience/stink", "application/x-www-form-urlencoded", form, nil)
	if rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if rec = workAdmin(s, "GET", "/admin/", "", "", nil); !strings.Contains(rec.Body.String(), "Stink Studios") {
		t.Fatal("dashboard doesn't list the role")
	}
	if rec = workAdmin(s, "GET", "/admin/experience/stink", "", "", nil); !strings.Contains(rec.Body.String(), `value="2020-02"`) {
		t.Fatalf("form: %s", rec.Body)
	}
	if rec = workAdmin(s, "POST", "/admin/experience/stink", "application/x-www-form-urlencoded", "role=&company=X&start=2020", nil); rec.Code != 400 {
		t.Fatalf("missing role accepted: %d", rec.Code)
	}
	workAdmin(s, "POST", "/admin/experience/stink/delete", "application/x-www-form-urlencoded", "", nil)
	if _, err := store.ExperienceOf(s.store).ExperienceItem(context.Background(), "stink"); err == nil {
		t.Fatal("delete didn't")
	}
}

func TestExcerpt(t *testing.T) {
	long := "I've been writing code, managing small teams, doing creative tech and consulting for 20 years. Currently I head AI technology globally."
	if got := excerpt(long, 60); got != "I've been writing code, managing small teams, doing creative…" {
		t.Errorf("excerpt = %q", got)
	}
	if got := excerpt("Short.\n\nTwo paras.", 160); got != "Short. Two paras." {
		t.Errorf("excerpt = %q", got)
	}
}

func TestSearchBasics(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	s.store.SavePage(ctx, content.Page{Slug: "", Title: "Ben Priddy", Body: "I've been writing code for 20 years. Currently I head AI technology globally.", Published: true})
	store.ExperienceOf(s.store).SaveExperience(ctx, content.Experience{Slug: "a", Role: "Global Head of AI Technology", Company: "Anomaly", Start: "2026-03", Current: true, Published: true})
	store.ProjectsOf(s.store).SaveProject(ctx, content.Project{Slug: "qledecode", Title: "QLEDecode", Summary: "A two month competition for superfans.", Published: true,
		Media: []content.Media{{Kind: "image", Src: "/media/projects/qledecode/hero.jpg", Width: 735, Height: 413}}})
	get := func(path string) (int, string, string) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		b, _ := io.ReadAll(rec.Body)
		return rec.Code, rec.Header().Get("Content-Type"), string(b)
	}
	code, ct, body := get("/robots.txt")
	if code != 200 || !strings.HasPrefix(ct, "text/plain") || !strings.Contains(body, "Sitemap: http://localhost:8080/sitemap.xml") || !strings.Contains(body, "Disallow: /admin/") {
		t.Errorf("robots.txt: %d %s\n%s", code, ct, body)
	}
	code, ct, body = get("/sitemap.xml")
	for _, want := range []string{"<loc>http://localhost:8080/</loc>", "<loc>http://localhost:8080/work/</loc>", "<loc>http://localhost:8080/work/qledecode</loc>"} {
		if code != 200 || !strings.Contains(ct, "xml") || !strings.Contains(body, want) {
			t.Errorf("sitemap missing %s:\n%s", want, body)
		}
	}
	_, _, home := get("/")
	for _, want := range []string{
		"<title>Ben Priddy · Global Head of AI Technology, Anomaly</title>",
		`<meta name="description" content="I&#39;ve been writing code for 20 years. Currently I head AI technology globally.">`,
		`<link rel="canonical" href="http://localhost:8080/">`,
		`"@type":"Person"`, `"jobTitle":"Global Head of AI Technology"`, `"worksFor":{"@type":"Organization","name":"Anomaly"}`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("home head missing %s", want)
		}
	}
	_, _, proj := get("/work/qledecode")
	for _, want := range []string{`content="A two month competition for superfans."`, `<meta property="og:image" content="http://localhost:8080/media/projects/qledecode/hero.jpg">`} {
		if !strings.Contains(proj, want) {
			t.Errorf("project head missing %s", want)
		}
	}
}

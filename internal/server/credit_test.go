package server

import (
	"net/url"
	"strings"
	"testing"
)

func TestCleanCredit(t *testing.T) {
	for in, want := range map[string]string{
		"  Jo   Smith\n":        "Jo Smith",
		"Jo‮Smith​":             "JoSmith", // format characters (bidi overrides, zero widths) dropped
		"a\tb\x00c":             "a bc",
		strings.Repeat("é", 90): strings.Repeat("é", 80),
		"":                      "",
	} {
		if got := cleanCredit(in); got != want {
			t.Errorf("cleanCredit(%q) = %q, want %q", in, got, want)
		}
	}
}

// A visitor's credit goes public only with Ben's approval; Ben can edit it.
func TestBuilderCredit(t *testing.T) {
	e := newBuilderServer(t, true)
	a := e.visitor(1, "192.0.2.1")
	slug := a.create(t, "A calm green page")
	e.script()
	r1 := a.chat(t, slug, "A calm green page", "")
	stranger := e.visitor(9, "192.0.2.9")
	pick := func() frontendResponse {
		return decodeFrontend(t, stranger.do("GET", "/api/frontend", nil, "Cookie", pickCookie+"=fe/"+slug))
	}
	approve := func() {
		t.Helper()
		subs, _ := e.st.Submissions(t.Context(), 10)
		if rec := adminDo(e, "POST", "/admin/builder/submissions/"+itoa64(subs[0].ID)+"/approve"); rec.Code != 303 {
			t.Fatalf("approve: %d", rec.Code)
		}
	}

	if rec := a.post("/build/api/fe/"+slug+"/submit", map[string]string{"rev": r1.Revision, "credit": "  Jo‮ Smith\n"}); rec.Code != 200 {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body)
	}
	if st := a.state(t); st.Frontends[0].Credit != "Jo Smith" {
		t.Errorf("the visitor's credit = %q", st.Frontends[0].Credit)
	}
	if page := adminDo(e, "GET", "/admin/builder/").Body.String(); !strings.Contains(page, "Credit: “Jo Smith”") {
		t.Error("the submissions table doesn't show the requested credit")
	}
	approve()
	if p := pick(); p.Ref != "fe/"+slug || p.Credit != "Jo Smith" {
		t.Fatalf("approved pick = %+v", p)
	}

	// a new credit with the next submission waits for the next approval
	e.script()
	r2 := a.chat(t, slug, "greener", r1.Revision)
	a.post("/build/api/fe/"+slug+"/submit", map[string]string{"rev": r2.Revision, "credit": "Someone else"})
	if p := pick(); p.Credit != "Jo Smith" {
		t.Errorf("unreviewed credit went public: %+v", p)
	}
	approve()
	if p := pick(); p.Credit != "Someone else" {
		t.Errorf("after approval: %+v", p)
	}

	// Ben edits it
	if rec := e.form("/admin/builder/fe/"+slug+"/credit", url.Values{"credit": {"  Ben's pick "}}); rec.Code != 303 {
		t.Fatalf("credit form: %d", rec.Code)
	}
	if p := pick(); p.Credit != "Ben's pick" {
		t.Errorf("after Ben's edit: %+v", p)
	}
	if rec := e.form("/admin/builder/fe/nope/credit", url.Values{"credit": {"x"}}); rec.Code != 404 {
		t.Errorf("unknown front end: %d", rec.Code)
	}
}

package content

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Resume is what /resume and its PDF say beyond the roles and projects the
// CMS already holds (protocol v1.14). Text fields are plain text; paragraphs
// are separated by blank lines, bullets and skills by newlines.
type Resume struct {
	Profile      string            `json:"profile"`      // paragraphs
	SolvingTitle string            `json:"solvingTitle"` // "Currently solving": an optional headline
	SolvingBody  string            `json:"solvingBody"`  // its paragraph(s); "" = the site bio's (all but its first)
	Bullets      map[string]string `json:"bullets"`      // experience slug → bullets, one per line
	SkillsDo     string            `json:"skillsDo"`     // "What I can do": one per line
	SkillsReview string            `json:"skillsReview"` // "What I can review": one per line
	Email        string            `json:"email"`
	LinkedIn     string            `json:"linkedin"` // a full https:// URL
	Location     string            `json:"location"`
	// Photo is the picture in the system's centre node (and by the name in
	// the PDF): a path on this site, "/static/..." or "/media/...". ""
	// (resumes saved before it existed, or a cleared field) is DefaultPhoto.
	Photo string `json:"photo,omitempty"`
}

// DefaultPhoto is Ben's portrait, cropped square around his face from his
// 1536x2048 selfie (uploads/d20994ceb1e7aa05e54c7def.jpg): 480 and 960px,
// JPEG and WebP, in web/static/resume/.
const DefaultPhoto = "/static/resume/ben-480.jpg"

var (
	staticSeg = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	photoExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
)

// ValidPhotoPath reports whether p can be the resume photo: a same-origin
// image path, either a media file ("/media/assets/uploads/<id>.jpg") or one
// of the site's own static files ("/static/resume/ben-480.jpg"). Nothing
// external, no query, no "..": the public CSP only allows 'self' images.
func ValidPhotoPath(p string) bool {
	if len(p) > 200 || path.Clean(p) != p || !photoExts[strings.ToLower(path.Ext(p))] {
		return false
	}
	if MediaPath(p) {
		return true
	}
	rest, ok := strings.CutPrefix(p, "/static/")
	if !ok || rest == "" {
		return false
	}
	for _, seg := range strings.Split(rest, "/") {
		if !staticSeg.MatchString(seg) {
			return false
		}
	}
	return true
}

// PhotoOrDefault is r's photo, or DefaultPhoto.
func (r Resume) PhotoOrDefault() string {
	if r.Photo == "" {
		return DefaultPhoto
	}
	return r.Photo
}

// MaxResumeField caps any one text field.
const MaxResumeField = 4000

// Validate reports why r can't be saved, or nil.
func (r Resume) Validate() error {
	fields := map[string]string{"profile": r.Profile, "currently solving title": r.SolvingTitle,
		"currently solving": r.SolvingBody, "skills (do)": r.SkillsDo, "skills (review)": r.SkillsReview,
		"email": r.Email, "LinkedIn": r.LinkedIn, "location": r.Location}
	for slug, b := range r.Bullets {
		fields["bullets for "+slug] = b
	}
	for name, v := range fields {
		if utf8.RuneCountInString(v) > MaxResumeField {
			return fmt.Errorf("%s is longer than %d characters", name, MaxResumeField)
		}
	}
	if r.LinkedIn != "" && !strings.HasPrefix(r.LinkedIn, "https://") {
		return fmt.Errorf("LinkedIn must be a full https:// address")
	}
	if r.Photo != "" && !ValidPhotoPath(r.Photo) {
		return fmt.Errorf("the photo must be an image on this site: a path like /media/assets/uploads/….jpg or /static/….jpg")
	}
	if r.Email != "" && (!strings.Contains(r.Email, "@") || strings.ContainsAny(r.Email, " <>")) {
		return fmt.Errorf("email doesn't look like an address")
	}
	return nil
}

// Lines splits a field into its non-empty, trimmed lines (bullets, skills).
func Lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-•*"))
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// DefaultResume is the resume until Ben edits it in the admin: his CV
// (Google Doc "Ben_Priddy_CV", 2026-08-17) with his corrections of
// 2026-10-13: the profile opens with the "love" framing of his bio instead of
// years of experience, and "helped shaped" reads "helped shape". Contact is
// email and LinkedIn only (no phone on a public page).
func DefaultResume() Resume {
	return Resume{
		Profile: "I love the creative ways technology shows up in culture, and I've built my career around making them: writing code, leading teams, directing technology and working across disciplines to create inspiring work with Google, Samsung and The Grammys, awarded at Cannes, the Webbys, the One Show and more.\n\n" +
			"Now heading AI Technology globally for Anomaly, I helped shape the company's AI vision and led the adoption that took internal use from novelty to a core facet of the business, through R&D, rapid realization and building of internal tools now in active use, a global internal education series, and direct project work with our brand partners.",
		// "Currently solving" is the site's version (Ben, 2026-10-13): the home
		// bio after its first paragraph, read live, so it never drifts
		SolvingTitle: "",
		SolvingBody:  "",
		Bullets: map[string]string{
			// the CV lists Anomaly's two titles together, with these bullets
			"anomaly-global-head-ai": "Lead AI technology globally, partnering with the Chief AI Officer to set AI strategy and vision, and owning the infrastructure, standards, and working practice.\n" +
				"Build the practice from the ground up alongside the people in it, on the premise that coding is becoming a facet of every role the way building a deck already is.\n" +
				"Designed and led applied AI education across the network: multiple AI Weeks for global offices as a rollout strategy, and individual workshops covering generative coding, AI video, and automated research.\n" +
				"Evaluate the AI tooling landscape and build the connective infrastructure that makes it usable under real production deadlines and client approvals.",
			"active-theory-technical-director": "Led a team of developers, raising engineering practice and standards across the studio's project work.\n" +
				"Contributed to the evolution of the internal libraries and tooling that the company has come to be known for.",
			"stink-studios-technical-director": "Built the vision for the studio's technology offering, shaping what the company sold and how it positioned technical capability to clients.\n" +
				"Led technology on award-winning project work for global brands, from technical concept through delivery.\n" +
				"Partnered with creative and new business on scoping and feasibility, translating ambitious concepts into buildable plans before they were committed.",
			"tool-creative-director-technology": "Production-side creative director on interactive campaigns for international brands including New Balance, The Grammys, Google, and Airbnb.\n" +
				"Voraciously researched and prototyped at the cutting edge of creative technology, discovering use cases and behaviors that became central components of future campaigns.",
		},
		SkillsDo:     "Agentic engineering\nLeveraging the evolving coding stack\nIntegrating skills\nRefining and hardening through loops and graph structure",
		SkillsReview: "Python\nJavaScript\nTypeScript\nNode.js\nReact frameworks\nThree.js\nGLSL\nModern database architectures\nModern cloud infrastructure",
		Email:        "colorandsound@gmail.com",
		LinkedIn:     "https://www.linkedin.com/in/ben-priddy-9673a016",
		Location:     "New York, NY",
		Photo:        DefaultPhoto,
	}
}

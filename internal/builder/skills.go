package builder

import (
	"embed"
	"fmt"
	"slices"
	"strings"

	"github.com/bpriddy/personal-site-2026/internal/connect"
)

// Skills are short guides with working code for one technique, which the
// model reads on demand (read_skill) instead of carrying them in every
// prompt: skills/<name>.md, whose first line is "description: ...".
//
//go:embed skills/*.md
var skillFS embed.FS

type skill struct{ name, desc, body string }

var skills = func() []skill {
	ents, err := skillFS.ReadDir("skills")
	if err != nil {
		panic(err)
	}
	var out []skill
	for _, e := range ents {
		b, err := skillFS.ReadFile("skills/" + e.Name())
		if err != nil {
			panic(err)
		}
		first, body, _ := strings.Cut(string(b), "\n")
		desc, ok := strings.CutPrefix(first, "description: ")
		if !ok {
			panic("skill " + e.Name() + ": first line must be \"description: ...\"")
		}
		out = append(out, skill{strings.TrimSuffix(e.Name(), ".md"), strings.TrimSpace(desc), strings.TrimSpace(body)})
	}
	slices.SortFunc(out, func(a, b skill) int { return strings.Compare(a.name, b.name) })
	return out
}()

// SkillNames lists the skills.
func SkillNames() []string {
	var out []string
	for _, s := range skills {
		out = append(out, s.name)
	}
	return out
}

// Skill returns a skill's guide.
func Skill(name string) (string, bool) {
	for _, s := range skills {
		if s.name == name {
			return s.body, true
		}
	}
	return "", false
}

// ToolsBrief is the system block that lists the skills and the connections
// this deployment has. It changes only with the configuration, so it is
// cached with the rest of the system prompt.
func ToolsBrief(conns []connect.Connection) string {
	var sb strings.Builder
	sb.WriteString("# Skills and connections\n\n")
	sb.WriteString("Skills are short guides with working code. Read one with read_skill before you use its technique or its tools:\n")
	for _, s := range skills {
		fmt.Fprintf(&sb, "- %s: %s\n", s.name, s.desc)
	}
	if len(conns) == 0 {
		sb.WriteString("\nNo connections to outside services are configured on this site: work with code and the house fonts only.\n")
		return sb.String()
	}
	sb.WriteString("\nConnections: tools that bring in assets from outside services (copied into the site's own media, so they load inside the sandbox):\n")
	for _, c := range conns {
		var names []string
		for _, t := range c.Tools() {
			names = append(names, t.Name)
		}
		fmt.Fprintf(&sb, "- %s: %s\n", c.Name(), strings.Join(names, ", "))
	}
	sb.WriteString(`
Use a connection when it makes the idea much better, not for its own sake: a request about real things (a ship, a car, a plant, an instrument, a room) is usually far better with a real 3D model than with primitive shapes; a material (wood, stone, metal) with a real texture; a typographic voice the house fonts can't give with a Google Font. One strong asset beats many. Keep pages fast: prefer lighter models and 1k textures.
Licenses: every import tells you its license. When it gives a credit line, show it as visible text in your files wherever the asset appears (a small credits line is fine); finish refuses to save without it.
If a connection fails or finds nothing good, do without it and build the idea in code.
`)
	return sb.String()
}

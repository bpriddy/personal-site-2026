package builder

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bpriddy/personal-site-2026/internal/connect"
)

// fakeConn is a connection with one search tool (returns a thumbnail) and
// one import tool (returns an asset that must be credited).
type fakeConn struct{ calls []string }

func (f *fakeConn) Name() string { return "fake" }
func (f *fakeConn) Tools() []connect.Tool {
	q := map[string]any{"q": map[string]any{"type": "string"}}
	return []connect.Tool{{Name: "fake_search", Description: "search", Props: q}, {Name: "fake_import", Description: "import", Props: q}}
}
func (f *fakeConn) Call(_ context.Context, tool string, _ json.RawMessage) (connect.Output, error) {
	f.calls = append(f.calls, tool)
	if tool == "fake_search" {
		return connect.Output{Text: "1 result", Images: []string{"https://thumbs.example/1.jpg"}}, nil
	}
	return connect.Output{Text: "imported", Credit: &connect.Credit{Asset: "/media/assets/models/sketchfab/abc.glb",
		Line: "“Ship” by Ada Artist, CC BY 4.0", Must: "Ada Artist"}}, nil
}

func TestConnectionsToolsImagesAndCredits(t *testing.T) {
	uses := strings.Replace(demoIndex, "</body>", `<script>site.loadModel("/media/assets/models/sketchfab/abc.glb")</script></body>`, 1)
	credited := strings.Replace(uses, "</body>", `<p class="credit">Model: “Ship” by Ada Artist, CC BY 4.0</p></body>`, 1)
	m := &ScriptedModel{Responses: []string{
		ToolUse("1", "read_skill", map[string]any{"name": "3d-models"}),
		ToolUse("2", "fake_search", map[string]any{"q": "ship"}),
		ToolUse("3", "fake_import", map[string]any{"q": "abc"}),
		ToolUse("4", "write_file", map[string]any{"path": "index.html", "content": uses}),
		ToolUse("5", "finish", map[string]any{"summary": "a ship"}), // refused: no credit
		ToolUse("6", "write_file", map[string]any{"path": "index.html", "content": credited}),
		ToolUse("7", "finish", map[string]any{"summary": "a ship"}),
	}}
	fc := &fakeConn{}
	b := New(m, Config{Model: "x", Connections: []connect.Connection{fc}})
	res, err := b.Run(context.Background(), Request{FrontendID: "fe/s", Prompt: "a ship"}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Files["index.html"]), "Ada Artist") {
		t.Fatal("saved without the credit")
	}
	if strings.Join(fc.calls, ",") != "fake_search,fake_import" {
		t.Fatalf("connection calls = %v", fc.calls)
	}
	// the tools are declared, with read_skill, and the brief lists the connection
	req := m.Requests[0]
	tools, _ := json.Marshal(req.Tools)
	for _, want := range []string{`"fake_search"`, `"fake_import"`, `"read_skill"`, `"3d-models"`} {
		if !strings.Contains(string(tools), want) {
			t.Errorf("tools missing %s", want)
		}
	}
	if !strings.Contains(req.System[2].Text, "fake: fake_search, fake_import") {
		t.Errorf("tools brief:\n%s", req.System[2].Text)
	}
	// the skill came back as the tool result
	skillTurn, _ := json.Marshal(m.Requests[1].Messages[len(m.Requests[1].Messages)-1])
	if !strings.Contains(string(skillTurn), "site.loadModel") {
		t.Errorf("read_skill result: %.300s", skillTurn)
	}
	// the search result carries the thumbnail as an image block
	searchTurn, _ := json.Marshal(m.Requests[2].Messages[len(m.Requests[2].Messages)-1])
	if !strings.Contains(string(searchTurn), `"type":"image"`) || !strings.Contains(string(searchTurn), "https://thumbs.example/1.jpg") {
		t.Errorf("search result: %s", searchTurn)
	}
	// the first finish was refused with the credit line
	refused, _ := json.Marshal(m.Requests[5].Messages[len(m.Requests[5].Messages)-1])
	if !strings.Contains(string(refused), "Not saved") || !strings.Contains(string(refused), "Ada Artist") {
		t.Errorf("finish without credit: %s", refused)
	}
}

func TestSkillsLoad(t *testing.T) {
	names := SkillNames()
	for _, want := range []string{"3d-models", "textures", "typography", "webgpu-starter"} {
		if body, ok := Skill(want); !ok || len(body) < 200 {
			t.Errorf("skill %s missing or empty (have %v)", want, names)
		}
	}
	brief := ToolsBrief(nil)
	if !strings.Contains(brief, "3d-models:") || !strings.Contains(brief, "No connections") {
		t.Errorf("brief without connections:\n%s", brief)
	}
}

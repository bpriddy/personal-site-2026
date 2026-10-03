package builder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/bpriddy/personal-site-2026/internal/revfiles"
)

const goodIndex = `<!doctype html><html><head><script src="/site-host.js"></script></head><body><main id=m></main><script>
(async () => { await site.loaded; const p = site.page(site.route);
document.getElementById("m").textContent = site.field(p, "title", {expect: "text"});
site.onRoute(r => site.navigate(r)); site.ready(); })();
</script></body></html>`

func TestCheckPath(t *testing.T) {
	for _, ok := range []string{"index.html", "js/app.js", "shaders/bg.wgsl", "a-b_c.1.css", "data.json"} {
		if err := CheckPath(ok); err != nil {
			t.Errorf("CheckPath(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/index.html", "../x.js", "a/../b.js", "./a.js", "a//b.js", ".env", "a/.git/x.js",
		"a b.js", "x.exe", "noext", "a\\b.js", "é.js", strings.Repeat("a", 200) + ".js", "dir/"} {
		if err := CheckPath(bad); err == nil {
			t.Errorf("CheckPath(%q) accepted", bad)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(revfiles.Files{"index.html": []byte(goodIndex)}); err != nil {
		t.Fatal(err)
	}
	for name, files := range map[string]revfiles.Files{
		"empty":        {},
		"no index":     {"app.js": []byte("site.ready()")},
		"no host":      {"index.html": []byte("<script>site.ready()</script>")},
		"no ready":     {"index.html": []byte(HostScriptTag)},
		"bad path":     {"index.html": []byte(goodIndex), "../x.js": nil},
		"too big":      {"index.html": []byte(goodIndex + strings.Repeat("x", MaxFileBytes))},
		"binary":       {"index.html": []byte(goodIndex + "\xff\xfe")},
		"bad ext file": {"index.html": []byte(goodIndex), "run.sh": []byte("x")},
	} {
		if err := Validate(files); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	many := revfiles.Files{"index.html": []byte(goodIndex)}
	for i := range MaxFiles {
		many[string(rune('a'+i%26))+strings.Repeat("x", i/26)+".js"] = nil
	}
	if err := Validate(many); err == nil {
		t.Error("too many files accepted")
	}
}

func TestRunNewFrontEnd(t *testing.T) {
	m := &ScriptedModel{Responses: []string{
		ToolUse("1", "write_file", map[string]any{"path": "index.html", "content": strings.Replace(goodIndex, "site.ready()", "site.readyX()", 1)}),
		ToolUse("2", "finish", map[string]any{"summary": "too early"}), // invalid: no site.ready(
		ToolUse("3", "str_replace", map[string]any{"path": "index.html", "old_str": "site.readyX()", "new_str": "site.ready()"}),
		ToolUse("4", "write_file", map[string]any{"path": "../escape.js", "content": "x"}), // refused
		ToolUse("5", "finish", map[string]any{"summary": "A dark page."}),
	}}
	b := New(m, Config{Model: "claude-opus-5-5", Fallbacks: true})
	var events []Event
	res, err := b.Run(context.Background(), Request{
		FrontendID: "fe/dark", Title: "Dark", Prompt: "make it dark",
		Content: json.RawMessage(`{"pages":[{"slug":"","title":"Ben Priddy","body":""}]}`),
	}, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "A dark page." || string(res.Files["index.html"]) != goodIndex || len(res.Files) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if strings.Join(res.Actions, ",") != "write index.html,edit index.html" {
		t.Errorf("actions = %v", res.Actions)
	}
	if len(m.Requests) != 5 {
		t.Fatalf("%d model turns", len(m.Requests))
	}

	// request shape: model, effort, adaptive thinking, auto tool choice,
	// fallbacks, cached system prompt, the prompt and content in the message
	req := m.Requests[0]
	if req.Model != "claude-opus-5-5" || req.OutputConfig.Effort != anthropic.BetaOutputConfigEffortHigh || req.Thinking.OfAdaptive == nil {
		t.Errorf("params: model %q effort %q thinking %+v", req.Model, req.OutputConfig.Effort, req.Thinking)
	}
	if req.Thinking.OfEnabled != nil || req.Thinking.OfDisabled != nil {
		t.Error("thinking must be adaptive only")
	}
	body, _ := json.Marshal(req)
	for _, want := range []string{`"fallbacks":"default"`, `"tool_choice"`} {
		has := strings.Contains(string(body), want)
		if want == `"tool_choice"` {
			has = !has // never forced (auto is the default)
		}
		if !has {
			t.Errorf("request body: %s check failed", want)
		}
	}
	if len(req.Betas) != 1 || req.Betas[0] != anthropic.AnthropicBetaServerSideFallback2026_07_01 {
		t.Errorf("betas = %v", req.Betas)
	}
	// every run: the system prompt, the quality bar, then the skills and
	// connections brief (cache breakpoint on it, the end of the static prefix)
	if len(req.System) != 3 || req.System[0].Text != SystemPrompt || req.System[1].Text != QualityBrief || req.System[2].Text != ToolsBrief(nil) {
		t.Error("system prompt + quality bar + tools brief")
	}
	if req.System[2].CacheControl.Type == "" {
		t.Error("cache breakpoint should sit on the tools brief (end of the static prefix)")
	}
	first := string(mustJSON(t, req.Messages[0]))
	for _, want := range []string{"make it dark", "new front end", "Ben Priddy"} {
		if !strings.Contains(first, want) {
			t.Errorf("first message missing %q", want)
		}
	}
	// the failed finish and refused write came back to the model as errors
	last := string(mustJSON(t, m.Requests[4].Messages))
	if !strings.Contains(last, "Can't write ../escape.js") || !strings.Contains(last, "site.ready()") {
		t.Errorf("tool errors not reported to the model: %s", last)
	}
	var sawTool bool
	for _, e := range events {
		sawTool = sawTool || (e.Type == "tool" && e.Path == "index.html")
	}
	if !sawTool {
		t.Errorf("events = %+v", events)
	}
}

func TestRunReprompt(t *testing.T) {
	parent := revfiles.Files{"index.html": []byte(goodIndex)}
	m := &ScriptedModel{Responses: []string{
		ToolUse("1", "str_replace", map[string]any{"path": "index.html", "old_str": "<main id=m>", "new_str": "<main id=m class=big>"}),
		ToolUse("2", "finish", map[string]any{"summary": "Bigger."}),
	}}
	history := []Turn{
		{Role: "user", Text: "make it dark"},
		{Role: "assistant", Text: "A dark page.", Actions: []string{"write index.html"}},
	}
	res, err := New(m, Config{Model: "x"}).Run(context.Background(),
		Request{FrontendID: "fe/dark", Parent: parent, History: history, Prompt: "bigger text"}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Files["index.html"]), "class=big") {
		t.Fatal("edit not applied")
	}
	if strings.Contains(string(parent["index.html"]), "class=big") {
		t.Fatal("parent files modified")
	}
	msgs := m.Requests[0].Messages
	if len(msgs) != 3 || msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[2].Role != "user" {
		t.Fatalf("history roles: %d messages", len(msgs))
	}
	if s := string(mustJSON(t, msgs[2])); !strings.Contains(s, `file path=\"index.html\"`) || !strings.Contains(s, "bigger text") {
		t.Errorf("current files not in the new message: %s", s)
	}
	if m.Requests[0].Fallbacks.OfDefault != "" || len(m.Requests[0].Betas) != 0 {
		t.Error("fallbacks set without Config.Fallbacks")
	}
}

func TestRunNudgesAndAcceptsWithoutFinish(t *testing.T) {
	m := &ScriptedModel{Responses: []string{
		ToolUse("1", "write_file", map[string]any{"path": "index.html", "content": goodIndex}),
		EndTurn("Done!", "end_turn"),
		EndTurn("All set.", "end_turn"),
	}}
	res, err := New(m, Config{Model: "x"}).Run(context.Background(), Request{Prompt: "p"}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "All set." || len(m.Requests) != 3 {
		t.Fatalf("summary %q after %d turns", res.Summary, len(m.Requests))
	}
}

func TestRunRefusalAndLimits(t *testing.T) {
	refusal := `{"id":"m","type":"message","role":"assistant","model":"x","stop_reason":"refusal","content":[],
		"stop_details":{"type":"refusal","category":"cyber","explanation":"nope"}}`
	_, err := New(&ScriptedModel{Responses: []string{refusal}}, Config{Model: "x"}).Run(context.Background(), Request{Prompt: "p"}, func(Event) {})
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "cyber") {
		t.Fatalf("refusal err = %v", err)
	}
	var loop []string
	for range 3 {
		loop = append(loop, ToolUse("l", "list_files", map[string]any{}))
	}
	_, err = New(&ScriptedModel{Responses: loop}, Config{Model: "x", MaxTurns: 3}).Run(context.Background(), Request{Prompt: "p"}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "3 model turns") {
		t.Fatalf("turn limit err = %v", err)
	}
}

func TestHistoryMessagesAlternate(t *testing.T) {
	msgs := historyMessages([]Turn{
		{Role: "assistant", Text: "orphan"}, // dropped: history starts with a user turn
		{Role: "user", Text: "a"},
		{Role: "user", Text: "b"}, // a failed run left two user turns in a row
		{Role: "assistant", Text: "c"},
		{Role: "user", Text: "d"},
	})
	var roles []string
	for _, m := range msgs {
		roles = append(roles, string(m.Role))
	}
	if strings.Join(roles, ",") != "user,assistant,user,assistant" {
		t.Fatalf("roles = %v", roles)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

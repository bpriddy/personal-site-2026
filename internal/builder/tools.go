package builder

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/bpriddy/personal-site-2026/internal/revfiles"
)

func tool(name, desc string, props map[string]any, required ...string) anthropic.BetaToolUnionParam {
	if props == nil {
		props = map[string]any{}
	}
	return anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
		Name:        name,
		Description: anthropic.String(desc),
		InputSchema: anthropic.BetaToolInputSchemaParam{
			Properties:  props,
			Required:    required,
			ExtraFields: map[string]any{"additionalProperties": false},
		},
		Strict: anthropic.Bool(true),
	}}
}

var str = map[string]any{"type": "string"}

// toolDefs are the file tools. Declared in full on every request, so the
// tools prefix (and prompt cache) stays stable.
func toolDefs() []anthropic.BetaToolUnionParam {
	pathProp := map[string]any{"type": "string", "description": "Relative file path, e.g. index.html or shaders/bg.wgsl"}
	return []anthropic.BetaToolUnionParam{
		tool("list_files", "List the working copy's files with their sizes.", nil),
		tool("read_file", "Read a file from the working copy.", map[string]any{"path": pathProp}, "path"),
		tool("write_file", "Create or overwrite a file in the working copy with the complete content.",
			map[string]any{"path": pathProp, "content": str}, "path", "content"),
		tool("str_replace", "Replace text in a file. old_str must occur exactly once in the file; include enough surrounding context to make it unique.",
			map[string]any{"path": pathProp, "old_str": str, "new_str": str}, "path", "old_str", "new_str"),
		tool("delete_file", "Delete a file from the working copy.", map[string]any{"path": pathProp}, "path"),
		tool("finish", "Save the working copy as a new revision and show it in Ben's preview. Call it once the files are complete and checked. summary: two or three plain sentences for Ben on what you built or changed.",
			map[string]any{"summary": str}, "summary"),
	}
}

// workspace is the working copy a run edits.
type workspace struct {
	files   revfiles.Files
	actions []string
	warned  bool // finish has pointed out soft problems once
}

func (ws *workspace) note(action string) {
	for _, a := range ws.actions {
		if a == action {
			return
		}
	}
	ws.actions = append(ws.actions, action)
}

// call runs one tool. It returns the tool result, whether it's an error, and,
// for a successful finish, the summary.
func (ws *workspace) call(name string, input json.RawMessage, emit func(Event), truncated bool) (out string, isErr bool, finished string) {
	var in struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
		OldStr  *string `json:"old_str"`
		NewStr  *string `json:"new_str"`
		Summary string  `json:"summary"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		if truncated {
			return "Your response was cut off before this tool call's input was complete, so it was not run. Use smaller steps.", true, ""
		}
		return "Invalid tool input: " + err.Error(), true, ""
	}
	fail := func(format string, a ...any) (string, bool, string) {
		msg := fmt.Sprintf(format, a...)
		emit(Event{Type: "warning", Tool: name, Path: in.Path, Text: msg})
		return msg, true, ""
	}
	switch name {
	case "list_files":
		if len(ws.files) == 0 {
			return "(no files yet)", false, ""
		}
		var sb strings.Builder
		for _, n := range sortedNames(ws.files) {
			fmt.Fprintf(&sb, "%s\t%d bytes\n", n, len(ws.files[n]))
		}
		return sb.String(), false, ""

	case "read_file":
		b, ok := ws.files[in.Path]
		if !ok {
			return fail("No such file: %s", in.Path)
		}
		return string(b), false, ""

	case "write_file":
		if err := CheckPath(in.Path); err != nil {
			return fail("Can't write %s: %v", in.Path, err)
		}
		if in.Content == nil {
			return fail("write_file needs content")
		}
		if len(*in.Content) > MaxFileBytes {
			return fail("%s is %d bytes; at most %d per file", in.Path, len(*in.Content), MaxFileBytes)
		}
		if _, ok := ws.files[in.Path]; !ok && len(ws.files) >= MaxFiles {
			return fail("Too many files (at most %d)", MaxFiles)
		}
		ws.files[in.Path] = []byte(*in.Content)
		ws.note("write " + in.Path)
		emit(Event{Type: "tool", Tool: name, Path: in.Path, Text: fmt.Sprintf("%d bytes", len(*in.Content))})
		return fmt.Sprintf("Wrote %s (%d bytes).", in.Path, len(*in.Content)), false, ""

	case "str_replace":
		b, ok := ws.files[in.Path]
		if !ok {
			return fail("No such file: %s", in.Path)
		}
		if in.OldStr == nil || in.NewStr == nil || *in.OldStr == "" {
			return fail("str_replace needs a non-empty old_str and a new_str")
		}
		switch n := strings.Count(string(b), *in.OldStr); n {
		case 0:
			return fail("old_str not found in %s; read the file and try again with the exact text", in.Path)
		case 1:
		default:
			return fail("old_str occurs %d times in %s; include more context so it is unique", n, in.Path)
		}
		next := strings.Replace(string(b), *in.OldStr, *in.NewStr, 1)
		if len(next) > MaxFileBytes {
			return fail("%s would be %d bytes; at most %d per file", in.Path, len(next), MaxFileBytes)
		}
		ws.files[in.Path] = []byte(next)
		ws.note("edit " + in.Path)
		emit(Event{Type: "tool", Tool: name, Path: in.Path})
		return "Replaced 1 occurrence in " + in.Path + ".", false, ""

	case "delete_file":
		if _, ok := ws.files[in.Path]; !ok {
			return fail("No such file: %s", in.Path)
		}
		delete(ws.files, in.Path)
		ws.note("delete " + in.Path)
		emit(Event{Type: "tool", Tool: name, Path: in.Path})
		return "Deleted " + in.Path + ".", false, ""

	case "finish":
		if err := Validate(ws.files); err != nil {
			return fail("Not saved. Fix these problems, then call finish again: %v", err)
		}
		if w := Warnings(ws.files); len(w) > 0 && !ws.warned {
			ws.warned = true
			emit(Event{Type: "warning", Tool: name, Text: strings.Join(w, "; ")})
			return "Not saved yet. Please check: " + strings.Join(w, "; ") +
				". Fix what applies; if something is intentional, call finish again and it will be saved.", false, ""
		}
		summary := strings.TrimSpace(in.Summary)
		if summary == "" {
			summary = "Updated the front end."
		}
		emit(Event{Type: "tool", Tool: name})
		return "Saved.", false, summary
	}
	return fail("Unknown tool %q", name)
}

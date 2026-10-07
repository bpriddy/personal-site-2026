// Package builder is the admin front-end builder's agent: it runs a Claude
// tool-use loop with file tools over a working copy of a revision's files and
// returns the new files, ready to be stored as a new immutable revision.
// The HTTP side (chat, preview, publishing) is internal/server/builder.go.
package builder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/bpriddy/personal-site-2026/internal/connect"
	"github.com/bpriddy/personal-site-2026/internal/llm"
	"github.com/bpriddy/personal-site-2026/internal/revfiles"
)

// Turn is one entry of a front end's prompt history, persisted with each
// revision (store.Revision.Conversation) so reprompting continues it.
type Turn struct {
	Role     string    `json:"role"` // "user" (Ben) or "assistant"
	Text     string    `json:"text"`
	At       time.Time `json:"at"`
	Revision string    `json:"revision,omitempty"` // assistant: the revision it produced
	Actions  []string  `json:"actions,omitempty"`  // assistant: file operations, e.g. "write index.html"
	By       string    `json:"by,omitempty"`       // user: "observer" when the site observer wrote it
	Images   []string  `json:"images,omitempty"`   // user: /media/ paths of the images attached to the prompt
}

// ParseConversation decodes a stored conversation; bad or empty JSON is an
// empty history.
func ParseConversation(raw []byte) []Turn {
	var out []Turn
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// Event is progress streamed to the browser while a run works.
type Event struct {
	Type string `json:"type"` // "status", "text", "thinking", "tool", "warning", "error", "revision", "done"
	Text string `json:"text,omitempty"`
	Tool string `json:"tool,omitempty"`
	Path string `json:"path,omitempty"`
	// "revision" events
	Revision string `json:"revision,omitempty"`
	Number   int    `json:"number,omitempty"`
}

// Model runs one model turn: it streams the response (reporting text and
// thinking through emit as it arrives) and returns the complete message.
// ClaudeModel is the real one; tests use a fake.
type Model interface {
	Turn(ctx context.Context, params anthropic.BetaMessageNewParams, emit func(Event)) (*anthropic.BetaMessage, error)
}

// Request is one chat turn from Ben.
type Request struct {
	FrontendID string          // "fe/<slug>"
	Title      string          // the front end's title
	Parent     revfiles.Files  // the files being changed; empty for a new front end
	History    []Turn          // the parent revision's conversation
	Prompt     string          // Ben's message
	Images     []Attachment    // images attached to the prompt (already in the media store)
	Content    json.RawMessage // the site's current content (/api/site.json), for reference
	// Visitor: the prompt comes from an anonymous visitor (the public
	// builder), not Ben. The model is told so (VisitorNote); the prompt itself
	// still goes only into user messages.
	Visitor bool
	// Observer: the prompt was written by the site observer (a content-drift
	// rebuild), on Ben's behalf. Only the label of the request in the first
	// message changes; the system prompt stays the same (cached).
	Observer bool
	// OnCost, if set, is told what the run spends as it goes: each model
	// turn's tokens and price (llm.Usage.Cost), and money connections spend
	// (generated media; USD only).
	OnCost func(llm.Cost)
}

// An Attachment is an image sent with a prompt: the model sees it, and can
// use it by its /media/ path (in the page, or as a start frame for video).
type Attachment struct {
	Path      string // "/media/assets/uploads/<hash>.png"
	MediaType string // "image/png", "image/jpeg", "image/webp" or "image/gif"
	Data      []byte
	Width     int
	Height    int
}

// Result is a finished run.
type Result struct {
	Files   revfiles.Files
	Summary string
	Actions []string
}

// Config tunes a Builder.
type Config struct {
	// Connections are outside services the model may call (internal/connect).
	Connections []connect.Connection
	Model       string // e.g. llm.Model
	Effort      anthropic.BetaOutputConfigEffort
	MaxTokens   int64 // per model turn
	MaxTurns    int   // model turns per run
	Fallbacks   bool  // server-side refusal fallbacks ("default" mode)
}

// Builder runs builder turns against a Model.
type Builder struct {
	model Model
	cfg   Config
}

func New(m Model, cfg Config) *Builder {
	if cfg.Effort == "" {
		cfg.Effort = anthropic.BetaOutputConfigEffortHigh
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 64000
	}
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 30
	}
	return &Builder{model: m, cfg: cfg}
}

// ErrRefused means the model (and any fallback) declined the request.
var ErrRefused = errors.New("the model declined this request")

// Run applies req.Prompt to req.Parent and returns the new files. It stops
// when the model calls finish with valid files, or fails.
func (b *Builder) Run(ctx context.Context, req Request, emit func(Event)) (*Result, error) {
	ctx = connect.WithBudget(ctx) // import limits are per run
	if req.OnCost != nil {
		ctx = connect.WithCostSink(ctx, func(usd float64) { req.OnCost(llm.Cost{USD: usd}) })
	}
	// some connections (paid video) are only for Ben's own runs
	conns := connect.ForRun(b.cfg.Connections, req.Visitor, req.Observer)
	ws := &workspace{ctx: ctx, files: clone(req.Parent), conns: map[string]connect.Connection{}}
	for _, c := range conns {
		for _, t := range c.Tools() {
			ws.conns[t.Name] = c
		}
	}
	msgs := historyMessages(req.History)
	first := []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(firstMessage(req))}
	for _, a := range req.Images {
		first = append(first, anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{
			Data: base64.StdEncoding.EncodeToString(a.Data), MediaType: anthropic.BetaBase64ImageSourceMediaType(a.MediaType)}))
	}
	msgs = append(msgs, anthropic.NewBetaUserMessage(first...))

	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(b.cfg.Model),
		MaxTokens: b.cfg.MaxTokens,
		// the quality bar rides on every run, after the system prompt; the
		// cache breakpoint sits on it so both static blocks are cached
		System: []anthropic.BetaTextBlockParam{
			{Text: SystemPrompt},
			{Text: QualityBrief},
			{Text: ToolsBrief(conns), CacheControl: anthropic.NewBetaCacheControlEphemeralParam()},
		},
		Tools:        toolDefs(conns),
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: b.cfg.Effort},
		Thinking: anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{
			Display: anthropic.BetaThinkingConfigAdaptiveDisplaySummarized,
		}},
	}
	if req.Visitor {
		// after the cached system prompt, so the cache still serves both kinds
		params.System = append(params.System, anthropic.BetaTextBlockParam{Text: VisitorNote})
	}
	if b.cfg.Fallbacks {
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}

	nudged := false
	for turn := 0; turn < b.cfg.MaxTurns; turn++ {
		params.Messages = msgs
		msg, err := b.model.Turn(ctx, params, emit)
		if err != nil {
			if se := llm.AsSpendLimit(err); se != nil {
				return nil, se // the account is out of money: no retry helps
			}
			return nil, fmt.Errorf("model: %w", err)
		}
		if req.OnCost != nil {
			req.OnCost(llm.UsageOf(msg, b.cfg.Model).Cost())
		}
		if msg.StopReason == anthropic.BetaStopReasonRefusal {
			why := string(msg.StopDetails.Category)
			if msg.StopDetails.Explanation != "" {
				why += ": " + msg.StopDetails.Explanation
			}
			if why != "" {
				return nil, fmt.Errorf("%w (%s)", ErrRefused, why)
			}
			return nil, ErrRefused
		}
		msgs = append(msgs, msg.ToParam())

		var results []anthropic.BetaContentBlockParamUnion
		finished := ""
		for _, block := range msg.Content {
			if block.Type != "tool_use" {
				continue
			}
			r := ws.call(block.Name, block.Input, emit, msg.StopReason == anthropic.BetaStopReasonMaxTokens)
			if r.finished != "" && finished == "" {
				finished = r.finished
			}
			results = append(results, toolResult(block.ID, r))
		}
		if finished != "" {
			return &Result{Files: ws.files, Summary: finished, Actions: ws.actions}, nil
		}
		if len(results) > 0 {
			msgs = append(msgs, anthropic.NewBetaUserMessage(results...))
			continue
		}
		// The model stopped without calling finish (end_turn, or cut off).
		// Nudge it once to finish; after that, accept valid files as they are.
		if err := Validate(ws.files); err == nil && nudged {
			return &Result{Files: ws.files, Summary: lastText(msg), Actions: ws.actions}, nil
		}
		nudged = true
		note := "Please call the finish tool with a short summary for Ben to save your work as a new revision."
		if msg.StopReason == anthropic.BetaStopReasonMaxTokens {
			note = "Your last response was cut off (output limit). Continue with smaller steps (str_replace, or split large files), then call finish."
		} else if err := Validate(ws.files); err != nil {
			note = "The files aren't valid yet: " + err.Error() + ". Fix them, then call finish."
		}
		msgs = append(msgs, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(note)))
	}
	return nil, fmt.Errorf("stopped after %d model turns without finishing", b.cfg.MaxTurns)
}

func lastText(msg *anthropic.BetaMessage) string {
	var parts []string
	for _, b := range msg.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

// historyMessages replays earlier turns as plain text (not the raw tool
// transcript): the current files are restated in the new message anyway, and
// plain turns stay valid whatever the model or prompt version was.
func historyMessages(history []Turn) []anthropic.BetaMessageParam {
	var out []anthropic.BetaMessageParam
	for _, t := range history {
		text := strings.TrimSpace(t.Text)
		if text == "" {
			continue
		}
		if len(t.Images) > 0 {
			text += "\n\n(Attached images: " + strings.Join(t.Images, ", ") + ")"
		}
		if t.Role == "assistant" {
			if len(t.Actions) > 0 {
				text += "\n\n(Files changed: " + strings.Join(t.Actions, ", ") + ")"
			}
			// roles must alternate: merge into a previous assistant turn
			if n := len(out); n > 0 && out[n-1].Role == anthropic.BetaMessageParamRoleAssistant {
				out[n-1].Content = append(out[n-1].Content, anthropic.NewBetaTextBlock(text))
				continue
			}
			if len(out) == 0 {
				continue // history must start with a user turn
			}
			out = append(out, anthropic.BetaMessageParam{Role: anthropic.BetaMessageParamRoleAssistant,
				Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(text)}})
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == anthropic.BetaMessageParamRoleUser {
			out[n-1].Content = append(out[n-1].Content, anthropic.NewBetaTextBlock(text))
			continue
		}
		out = append(out, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(text)))
	}
	// the new message is a user turn, so the history must end with the assistant
	if n := len(out); n > 0 && out[n-1].Role == anthropic.BetaMessageParamRoleUser {
		out = append(out, anthropic.BetaMessageParam{Role: anthropic.BetaMessageParamRoleAssistant,
			Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock("(No revision was saved for that request.)")}})
	}
	return out
}

// maxInlineFiles bounds how much of the current files goes straight into the
// first message; beyond it the model reads files with read_file.
const maxInlineFiles = 200 << 10

// firstMessage states the current files, the site content, and Ben's request.
func firstMessage(req Request) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Front end: %s (%q)\n\n", req.FrontendID, req.Title)
	if len(req.Parent) == 0 {
		sb.WriteString("This is a new front end: there are no files yet.\n\n")
	} else {
		total := 0
		for _, b := range req.Parent {
			total += len(b)
		}
		sb.WriteString("Current files of the revision you are changing:\n\n")
		for _, n := range sortedNames(req.Parent) {
			if total <= maxInlineFiles {
				fmt.Fprintf(&sb, "<file path=%q>\n%s\n</file>\n\n", n, req.Parent[n])
			} else {
				fmt.Fprintf(&sb, "- %s (%d bytes; use read_file)\n", n, len(req.Parent[n]))
			}
		}
	}
	if len(req.Content) > 0 {
		sb.WriteString("The site's current content (what site.content will hold; for reference only, never hard-code it):\n\n<content>\n")
		sb.Write(req.Content)
		sb.WriteString("\n</content>\n\n")
	}
	switch {
	case req.Visitor:
		sb.WriteString("The visitor's request:\n\n")
	case req.Observer:
		sb.WriteString("A request from Ben's site observer (automatic, on Ben's behalf: the site's content changed and this front end doesn't show all of it yet):\n\n")
	default:
		sb.WriteString("Ben's request:\n\n")
	}
	sb.WriteString(req.Prompt)
	if len(req.Images) > 0 {
		who := "Ben"
		if req.Visitor {
			who = "The visitor"
		}
		fmt.Fprintf(&sb, "\n\n%s attached %d image(s), shown below. Each is already in the media store, so you can use it by its path: in the page (<img>, a CSS background, a WebGPU texture), as a reference for the look, or as the start_image of a generated video when that tool is available.\n", who, len(req.Images))
		for _, a := range req.Images {
			fmt.Fprintf(&sb, "- %s (%dx%d)\n", a.Path, a.Width, a.Height)
		}
	}
	return sb.String()
}

// toolResult is a tool_result block: the text, then any images (by URL).
func toolResult(id string, r result) anthropic.BetaContentBlockParamUnion {
	blk := anthropic.NewBetaToolResultBlock(id, r.out, r.isErr)
	for _, u := range r.images {
		blk.OfToolResult.Content = append(blk.OfToolResult.Content, anthropic.BetaToolResultBlockParamContentUnion{
			OfImage: &anthropic.BetaImageBlockParam{Source: anthropic.BetaImageBlockParamSourceUnion{
				OfURL: &anthropic.BetaURLImageSourceParam{URL: u}}}})
	}
	return blk
}

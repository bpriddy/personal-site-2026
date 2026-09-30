package builder

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
)

// ClaudeModel is the real Model: a streamed Messages API call (beta surface,
// for server-side refusal fallbacks).
type ClaudeModel struct {
	Client *anthropic.Client
}

func (c ClaudeModel) Turn(ctx context.Context, params anthropic.BetaMessageNewParams, emit func(Event)) (*anthropic.BetaMessage, error) {
	stream := c.Client.Beta.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	msg := anthropic.BetaMessage{}
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return nil, err
		}
		switch ev.Type {
		case "content_block_start":
			switch ev.ContentBlock.Type {
			case "tool_use":
				emit(Event{Type: "status", Tool: ev.ContentBlock.Name, Text: "calling " + ev.ContentBlock.Name})
			case "fallback":
				emit(Event{Type: "status", Text: "the request was re-served by a fallback model"})
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				emit(Event{Type: "text", Text: ev.Delta.Text})
			case "thinking_delta":
				emit(Event{Type: "thinking", Text: ev.Delta.Thinking})
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return &msg, nil
}

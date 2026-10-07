package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
)

// ScriptedModel is a Model for tests: each Turn returns the next scripted
// response (a Messages API response body as JSON, or "error:<message>" for a
// failed call) and records the request.
type ScriptedModel struct {
	mu        sync.Mutex
	Responses []string
	Requests  []anthropic.BetaMessageNewParams
}

// ToolUse returns a scripted response that calls one tool.
func ToolUse(id, name string, input any) string {
	b, _ := json.Marshal(map[string]any{
		"id": "msg_" + id, "type": "message", "role": "assistant", "model": "scripted",
		"stop_reason": "tool_use",
		"content": []any{
			map[string]any{"type": "text", "text": "Calling " + name + "."},
			map[string]any{"type": "tool_use", "id": "toolu_" + id, "name": name, "input": input},
		},
	})
	return string(b)
}

// EndTurn returns a scripted text-only response with the given stop reason.
func EndTurn(text, stopReason string) string {
	b, _ := json.Marshal(map[string]any{
		"id": "msg_end", "type": "message", "role": "assistant", "model": "scripted",
		"stop_reason": stopReason,
		"content":     []any{map[string]any{"type": "text", "text": text}},
	})
	return string(b)
}

func (m *ScriptedModel) Turn(_ context.Context, params anthropic.BetaMessageNewParams, emit func(Event)) (*anthropic.BetaMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Requests = append(m.Requests, params)
	if len(m.Responses) == 0 {
		return nil, errors.New("scripted model: no more responses")
	}
	raw := m.Responses[0]
	m.Responses = m.Responses[1:]
	if msg, ok := strings.CutPrefix(raw, "error:"); ok {
		return nil, errors.New(msg) // an API failure, e.g. a spend limit
	}
	var msg anthropic.BetaMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		return nil, fmt.Errorf("scripted model: %w", err)
	}
	for _, b := range msg.Content {
		if b.Type == "text" {
			emit(Event{Type: "text", Text: b.Text})
		}
	}
	return &msg, nil
}

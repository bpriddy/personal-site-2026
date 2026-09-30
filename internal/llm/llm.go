// Package llm is the shared Claude API client for the builder and observer.
package llm

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Model is the default model for both the builder and the observer.
const Model = "claude-opus-5-5"

// NewClient returns a client for apiKey, or nil when apiKey is empty (the AI
// features are then disabled; callers must handle nil).
func NewClient(apiKey string) *anthropic.Client {
	if apiKey == "" {
		return nil
	}
	c := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &c
}

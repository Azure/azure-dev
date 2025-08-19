// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package logging

import (
	"context"
	"regexp"
	"strings"

	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/schema"
)

// Compile-time check to ensure ThoughtLogger implements callbacks.Handler
var _ callbacks.Handler = &ThoughtLogger{}

// ThoughtLogger tracks and logs all agent actions
type ThoughtLogger struct {
	ThoughtChan chan<- string
}

// NewThoughtLogger creates a new action logger with a write-only channel for thoughts
func NewThoughtLogger(thoughtChan chan<- string) *ThoughtLogger {
	return &ThoughtLogger{
		ThoughtChan: thoughtChan,
	}
}

// HandleText is called when text is processed
func (al *ThoughtLogger) HandleText(ctx context.Context, text string) {
}

// HandleLLMGenerateContentStart is called when LLM content generation starts
func (al *ThoughtLogger) HandleLLMGenerateContentStart(ctx context.Context, ms []llms.MessageContent) {
}

// HandleLLMGenerateContentEnd is called when LLM content generation ends
func (al *ThoughtLogger) HandleLLMGenerateContentEnd(ctx context.Context, res *llms.ContentResponse) {
	// Parse and print thoughts as "THOUGHT: <content>" from content
	// IF thought contains: "Do I need to use a tool?", omit this thought.

	for _, choice := range res.Choices {
		content := choice.Content

		// Find all "Thought:" patterns and extract the content that follows
		// (?is) flags: i=case insensitive, s=dot matches newlines
		// .*? is non-greedy to stop at the first occurrence of next pattern or end
		thoughtRegex := regexp.MustCompile(`(?is)thought:\s*(.*?)(?:\n\s*(?:action|final answer|observation|ai|thought):|$)`)
		matches := thoughtRegex.FindAllStringSubmatch(content, -1)

		for _, match := range matches {
			if len(match) > 1 {
				thought := strings.TrimSpace(match[1])
				if thought != "" {
					// Skip thoughts that contain "Do I need to use a tool?"
					if !strings.Contains(strings.ToLower(thought), "do i need to use a tool?") {
						if al.ThoughtChan != nil {
							al.ThoughtChan <- thought
						}
					}
				}
			}
		}
	}
}

// HandleRetrieverStart is called when retrieval starts
func (al *ThoughtLogger) HandleRetrieverStart(ctx context.Context, query string) {
}

// HandleRetrieverEnd is called when retrieval ends
func (al *ThoughtLogger) HandleRetrieverEnd(ctx context.Context, query string, documents []schema.Document) {
}

// HandleToolStart is called when a tool execution starts
func (al *ThoughtLogger) HandleToolStart(ctx context.Context, input string) {
}

// HandleToolEnd is called when a tool execution ends
func (al *ThoughtLogger) HandleToolEnd(ctx context.Context, output string) {
}

// HandleToolError is called when a tool execution fails
func (al *ThoughtLogger) HandleToolError(ctx context.Context, err error) {
}

// HandleLLMStart is called when LLM call starts
func (al *ThoughtLogger) HandleLLMStart(ctx context.Context, prompts []string) {
}

// HandleChainStart is called when chain execution starts
func (al *ThoughtLogger) HandleChainStart(ctx context.Context, inputs map[string]any) {
}

// HandleChainEnd is called when chain execution ends
func (al *ThoughtLogger) HandleChainEnd(ctx context.Context, outputs map[string]any) {
}

// HandleChainError is called when chain execution fails
func (al *ThoughtLogger) HandleChainError(ctx context.Context, err error) {
}

// HandleAgentAction is called when an agent action is planned
func (al *ThoughtLogger) HandleAgentAction(ctx context.Context, action schema.AgentAction) {
}

// HandleAgentFinish is called when the agent finishes
func (al *ThoughtLogger) HandleAgentFinish(ctx context.Context, finish schema.AgentFinish) {
}

// HandleLLMError is called when LLM call fails
func (al *ThoughtLogger) HandleLLMError(ctx context.Context, err error) {
}

// HandleStreamingFunc handles streaming responses
func (al *ThoughtLogger) HandleStreamingFunc(ctx context.Context, chunk []byte) {
}

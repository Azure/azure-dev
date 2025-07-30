// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package logging

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/fatih/color"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/schema"
)

// Compile-time check to ensure ActionLogger implements callbacks.Handler
var _ callbacks.Handler = &ActionLogger{}

// ActionLogger tracks and logs all agent actions
type ActionLogger struct {
	debugEnabled bool
}

// ActionLoggerOption represents an option for configuring ActionLogger
type ActionLoggerOption func(*ActionLogger)

// WithDebug enables debug mode for verbose logging
func WithDebug(enabled bool) ActionLoggerOption {
	return func(al *ActionLogger) {
		al.debugEnabled = enabled
	}
}

// NewActionLogger creates a new action logger
func NewActionLogger(opts ...ActionLoggerOption) *ActionLogger {
	al := &ActionLogger{}

	for _, opt := range opts {
		opt(al)
	}

	return al
}

// HandleText is called when text is processed
func (al *ActionLogger) HandleText(ctx context.Context, text string) {
}

// HandleLLMGenerateContentStart is called when LLM content generation starts
func (al *ActionLogger) HandleLLMGenerateContentStart(ctx context.Context, ms []llms.MessageContent) {
}

// HandleLLMGenerateContentEnd is called when LLM content generation ends
func (al *ActionLogger) HandleLLMGenerateContentEnd(ctx context.Context, res *llms.ContentResponse) {
	// Parse and print thoughts as "THOUGHT: <content>" from content
	// IF thought contains: "Do I need to use a tool?", omit this thought.

	for _, choice := range res.Choices {
		content := choice.Content

		if al.debugEnabled {
			color.HiBlack("\nHandleLLMGenerateContentEnd\n%s\n", content)
		}

		// Find all "Thought:" patterns and extract the content that follows
		thoughtRegex := regexp.MustCompile(`(?i)thought:\s*(.*)`)
		matches := thoughtRegex.FindAllStringSubmatch(content, -1)

		for _, match := range matches {
			if len(match) > 1 {
				thought := strings.TrimSpace(match[1])
				if thought != "" {
					// Skip thoughts that contain "Do I need to use a tool?"
					if !strings.Contains(strings.ToLower(thought), "do i need to use a tool?") {
						color.White("\n🤖 Agent: %s\n", thought)
					}
				}
			}
		}
	}
}

// HandleRetrieverStart is called when retrieval starts
func (al *ActionLogger) HandleRetrieverStart(ctx context.Context, query string) {
}

// HandleRetrieverEnd is called when retrieval ends
func (al *ActionLogger) HandleRetrieverEnd(ctx context.Context, query string, documents []schema.Document) {
}

// HandleToolStart is called when a tool execution starts
func (al *ActionLogger) HandleToolStart(ctx context.Context, input string) {
}

// HandleToolEnd is called when a tool execution ends
func (al *ActionLogger) HandleToolEnd(ctx context.Context, output string) {
}

// HandleToolError is called when a tool execution fails
func (al *ActionLogger) HandleToolError(ctx context.Context, err error) {
	color.Red("\nTool Error: %s\n", err.Error())
}

// HandleLLMStart is called when LLM call starts
func (al *ActionLogger) HandleLLMStart(ctx context.Context, prompts []string) {
}

// HandleChainStart is called when chain execution starts
func (al *ActionLogger) HandleChainStart(ctx context.Context, inputs map[string]any) {
}

// HandleChainEnd is called when chain execution ends
func (al *ActionLogger) HandleChainEnd(ctx context.Context, outputs map[string]any) {
}

// HandleChainError is called when chain execution fails
func (al *ActionLogger) HandleChainError(ctx context.Context, err error) {
	color.Red("\n%s\n", err.Error())
}

// HandleAgentAction is called when an agent action is planned
func (al *ActionLogger) HandleAgentAction(ctx context.Context, action schema.AgentAction) {
	// Print "Calling <action>"
	// Inspect action.ToolInput. Attempt to parse input as JSON
	// If is valid JSON and contains a param 'filename' then print filename.
	// example: "Calling read_file <filename>"
	if al.debugEnabled {
		color.HiBlack("\nHandleAgentAction\n%s\n", action.Log)
	}

	var toolInput map[string]interface{}
	if err := json.Unmarshal([]byte(action.ToolInput), &toolInput); err == nil {
		// Successfully parsed JSON, check for filename parameter
		if filename, ok := toolInput["filename"]; ok {
			if filenameStr, ok := filename.(string); ok {
				color.Green("\n🤖 Agent: Calling %s %s\n", action.Tool, filenameStr)
				return
			}
		}
		// JSON parsed but no filename found, use fallback format
		color.Green("\n🤖 Agent: Calling %s tool\n", action.Tool)
	} else {
		// JSON parsing failed, show the input as text
		color.Green("\n🤖 Agent: Calling %s tool with %s\n", action.Tool, action.ToolInput)
	}
}

// HandleAgentFinish is called when the agent finishes
func (al *ActionLogger) HandleAgentFinish(ctx context.Context, finish schema.AgentFinish) {
	// Find summary from format "AI: <summary>"
	// Print: <summary>
	if al.debugEnabled {
		color.HiBlack("\nHandleAgentFinish\n%s\n", finish.Log)
	}

	// Use regex to find AI summary, capturing everything after "AI:" (including multi-line)
	// The (?s) flag makes . match newlines, (.+) captures everything after "AI:"
	aiRegex := regexp.MustCompile(`(?is)AI:\s*(.+)`)
	matches := aiRegex.FindStringSubmatch(finish.Log)

	if len(matches) > 1 {
		summary := strings.TrimSpace(matches[1])
		color.White("\n🤖 Agent: %s\n", summary)
	}
	// If "AI:" not found, don't print anything
}

// HandleLLMError is called when LLM call fails
func (al *ActionLogger) HandleLLMError(ctx context.Context, err error) {
	color.Red("\nLLM Error: %s\n", err.Error())
}

// HandleStreamingFunc handles streaming responses
func (al *ActionLogger) HandleStreamingFunc(ctx context.Context, chunk []byte) {
}

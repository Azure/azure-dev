// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/schema"
)

// Thought represents a single thought or action taken by the agent
type Thought struct {
	Thought     string
	Action      string
	ActionInput string
}

// ThoughtLogger tracks and logs all agent thoughts and actions
type ThoughtLogger struct {
	ThoughtChan chan<- Thought
}

// NewThoughtLogger creates a new callbacks handler with a write-only channel for thoughts
func NewThoughtLogger(thoughtChan chan<- Thought) callbacks.Handler {
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
							al.ThoughtChan <- Thought{
								Thought: thought,
							}
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
	// Print "Calling <action>"
	// Inspect action.ToolInput. Attempt to parse input as JSON
	// If is valid JSON and contains a param 'filename' then print filename.
	// example: "Calling read_file <filename>"

	prioritizedParams := map[string]struct{}{
		"path":     {},
		"pattern":  {},
		"filename": {},
		"command":  {},
	}

	var toolInput map[string]interface{}
	if err := json.Unmarshal([]byte(action.ToolInput), &toolInput); err == nil {
		// Successfully parsed JSON, create comma-delimited key-value pairs
		excludedKeys := map[string]bool{"content": true}
		var params []string

		for key, value := range toolInput {
			if excludedKeys[key] {
				continue
			}

			var valueStr string
			switch v := value.(type) {
			case []interface{}:
				// Skip empty arrays
				if len(v) == 0 {
					continue
				}
				// Handle arrays by joining with spaces
				var strSlice []string
				for _, item := range v {
					strSlice = append(strSlice, strings.TrimSpace(string(fmt.Sprintf("%v", item))))
				}
				valueStr = strings.Join(strSlice, " ")
			case map[string]interface{}:
				// Skip empty maps
				if len(v) == 0 {
					continue
				}
				valueStr = strings.TrimSpace(fmt.Sprintf("%v", v))
			case string:
				// Skip empty strings
				trimmed := strings.TrimSpace(v)
				if trimmed == "" {
					continue
				}
				valueStr = trimmed
			default:
				valueStr = strings.TrimSpace(fmt.Sprintf("%v", v))
			}

			if valueStr != "" {
				params = append(params, fmt.Sprintf("%s: %s", key, valueStr))
			}
		}

		// Identify prioritized params
		for _, param := range params {
			for key := range prioritizedParams {
				if strings.HasPrefix(param, key) {
					paramStr := truncateString(param, 120)
					al.ThoughtChan <- Thought{
						Action:      action.Tool,
						ActionInput: paramStr,
					}
					return
				}
			}
		}

		al.ThoughtChan <- Thought{
			Action: action.Tool,
		}

	} else {
		// JSON parsing failed, show the input as text with truncation
		toolInput := strings.TrimSpace(action.ToolInput)
		if toolInput == "" || strings.HasPrefix(toolInput, "{") {
			al.ThoughtChan <- Thought{
				Action: action.Tool,
			}
		} else {
			toolInput = truncateString(toolInput, 120)
			al.ThoughtChan <- Thought{
				Action:      action.Tool,
				ActionInput: toolInput,
			}
		}
	}
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

// truncateString truncates a string to maxLen characters and adds "..." if truncated
func truncateString(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

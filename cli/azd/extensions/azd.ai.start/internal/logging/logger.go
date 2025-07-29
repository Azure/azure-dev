// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package logging

import (
	"context"
	"fmt"

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
	if al.debugEnabled {
		fmt.Printf("📝 Text (full): %s\n", text)
	}
}

// HandleLLMGenerateContentStart is called when LLM content generation starts
func (al *ActionLogger) HandleLLMGenerateContentStart(ctx context.Context, ms []llms.MessageContent) {
	if al.debugEnabled {
		for i, msg := range ms {
			fmt.Printf("🤖 Debug - Message %d: %+v\n", i, msg)
		}
	}
}

// HandleLLMGenerateContentEnd is called when LLM content generation ends
func (al *ActionLogger) HandleLLMGenerateContentEnd(ctx context.Context, res *llms.ContentResponse) {
	if al.debugEnabled && res != nil {
		fmt.Printf("🤖 Debug - Response: %+v\n", res)
	}
}

// HandleRetrieverStart is called when retrieval starts
func (al *ActionLogger) HandleRetrieverStart(ctx context.Context, query string) {
	if al.debugEnabled {
		fmt.Printf("🔍 Retrieval starting for query (full): %s\n", query)
	}
}

// HandleRetrieverEnd is called when retrieval ends
func (al *ActionLogger) HandleRetrieverEnd(ctx context.Context, query string, documents []schema.Document) {
	fmt.Printf("🔍 Retrieval completed: found %d documents\n", len(documents))
	if al.debugEnabled {
		fmt.Printf("🔍 Debug - Query (full): %s\n", query)
		for i, doc := range documents {
			fmt.Printf("🔍 Debug - Document %d: %+v\n", i, doc)
		}
	}
}

// HandleToolStart is called when a tool execution starts
func (al *ActionLogger) HandleToolStart(ctx context.Context, input string) {
	if al.debugEnabled {
		fmt.Printf("🔧 Executing Tool: %s\n", input)
	}
}

// HandleToolEnd is called when a tool execution ends
func (al *ActionLogger) HandleToolEnd(ctx context.Context, output string) {
	if al.debugEnabled {
		fmt.Printf("✅ Tool Result (full): %s\n", output)
	}
}

// HandleToolError is called when a tool execution fails
func (al *ActionLogger) HandleToolError(ctx context.Context, err error) {
	fmt.Printf("❌ Tool Error: %s\n", err.Error())
}

// HandleLLMStart is called when LLM call starts
func (al *ActionLogger) HandleLLMStart(ctx context.Context, prompts []string) {
	for i, prompt := range prompts {
		if al.debugEnabled {
			fmt.Printf("🤖 Prompt %d (full): %s\n", i, prompt)
		}
	}
}

// HandleChainStart is called when chain execution starts
func (al *ActionLogger) HandleChainStart(ctx context.Context, inputs map[string]any) {
	for key, value := range inputs {
		if al.debugEnabled {
			fmt.Printf("🔗 Input [%s]: %v\n", key, value)
		}
	}
}

// HandleChainEnd is called when chain execution ends
func (al *ActionLogger) HandleChainEnd(ctx context.Context, outputs map[string]any) {
	for key, value := range outputs {
		if al.debugEnabled {
			fmt.Printf("🔗 Output [%s]: %v\n", key, value)
		}
	}
}

// HandleChainError is called when chain execution fails
func (al *ActionLogger) HandleChainError(ctx context.Context, err error) {
	fmt.Printf("🔗 Chain execution failed: %s\n", err.Error())
}

// HandleAgentAction is called when an agent action is planned
func (al *ActionLogger) HandleAgentAction(ctx context.Context, action schema.AgentAction) {
	fmt.Printf("%s\n\n", action.Log)

	if al.debugEnabled {
		fmt.Printf("🎯 Agent planned action (debug): %+v\n", action)
	}
}

// HandleAgentFinish is called when the agent finishes
func (al *ActionLogger) HandleAgentFinish(ctx context.Context, finish schema.AgentFinish) {
	fmt.Printf("%s\n\n", finish.Log)

	if al.debugEnabled {
		fmt.Printf("🏁 Agent finished (debug): %+v\n", finish)
	}
}

// HandleLLMError is called when LLM call fails
func (al *ActionLogger) HandleLLMError(ctx context.Context, err error) {
	fmt.Printf("🤖 LLM error: %s\n", err.Error())
}

// HandleStreamingFunc handles streaming responses
func (al *ActionLogger) HandleStreamingFunc(ctx context.Context, chunk []byte) {
	// if len(chunk) > 0 {
	// 	fmt.Print(string(chunk))
	// }
}

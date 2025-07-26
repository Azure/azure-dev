// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package logging

import (
	"context"
	"fmt"
	"time"

	"github.com/tmc/langchaingo/schema"

	"azd.ai.start/internal/session"
	"azd.ai.start/internal/utils"
)

// ActionLogger tracks and logs all agent actions
type ActionLogger struct {
	actions []session.ActionLog
	current *session.ActionLog
}

// NewActionLogger creates a new action logger
func NewActionLogger() *ActionLogger {
	return &ActionLogger{
		actions: make([]session.ActionLog, 0),
	}
}

// HandleToolStart is called when a tool execution starts
func (al *ActionLogger) HandleToolStart(ctx context.Context, input string) {
	al.current = &session.ActionLog{
		Timestamp: time.Now(),
		Input:     input,
	}
	fmt.Printf("🔧 Executing: %s\n", input)
}

// HandleToolEnd is called when a tool execution ends
func (al *ActionLogger) HandleToolEnd(ctx context.Context, output string) {
	if al.current != nil {
		al.current.Output = output
		al.current.Success = true
		al.current.Duration = time.Since(al.current.Timestamp)
		al.actions = append(al.actions, *al.current)
		fmt.Printf("✅ Result: %s\n", utils.TruncateString(output, 100))
	}
}

// HandleToolError is called when a tool execution fails
func (al *ActionLogger) HandleToolError(ctx context.Context, err error) {
	if al.current != nil {
		al.current.Output = err.Error()
		al.current.Success = false
		al.current.Duration = time.Since(al.current.Timestamp)
		al.actions = append(al.actions, *al.current)
		fmt.Printf("❌ Error: %s\n", err.Error())
	}
}

// HandleAgentStart is called when agent planning starts
func (al *ActionLogger) HandleAgentStart(ctx context.Context, input map[string]any) {
	if userInput, ok := input["input"].(string); ok {
		fmt.Printf("🎯 Processing: %s\n", userInput)
	}
}

// HandleAgentEnd is called when agent planning ends
func (al *ActionLogger) HandleAgentEnd(ctx context.Context, output schema.AgentFinish) {
	fmt.Printf("🏁 Agent completed planning\n")
}

// HandleChainStart is called when chain execution starts
func (al *ActionLogger) HandleChainStart(ctx context.Context, input map[string]any) {
	fmt.Printf("🔗 Starting chain execution\n")
}

// HandleChainEnd is called when chain execution ends
func (al *ActionLogger) HandleChainEnd(ctx context.Context, output map[string]any) {
	fmt.Printf("🔗 Chain execution completed\n")
}

// HandleChainError is called when chain execution fails
func (al *ActionLogger) HandleChainError(ctx context.Context, err error) {
	fmt.Printf("🔗 Chain execution failed: %s\n", err.Error())
}

// HandleLLMStart is called when LLM call starts
func (al *ActionLogger) HandleLLMStart(ctx context.Context, prompts []string) {
	fmt.Printf("🤖 LLM thinking...\n")
}

// HandleLLMEnd is called when LLM call ends
func (al *ActionLogger) HandleLLMEnd(ctx context.Context, result string) {
	fmt.Printf("🤖 LLM response received\n")
}

// HandleAgentAction is called when an agent action is planned
func (al *ActionLogger) HandleAgentAction(ctx context.Context, action schema.AgentAction) {
	al.current = &session.ActionLog{
		Timestamp: time.Now(),
		Action:    action.Tool,
		Tool:      action.Tool,
		Input:     action.ToolInput,
	}
	fmt.Printf("🎯 Agent planned action: %s with input: %s\n", action.Tool, action.ToolInput)
}

// HandleAgentFinish is called when the agent finishes
func (al *ActionLogger) HandleAgentFinish(ctx context.Context, finish schema.AgentFinish) {
	fmt.Printf("🏁 Agent finished with result\n")
}

// HandleLLMError is called when LLM call fails
func (al *ActionLogger) HandleLLMError(ctx context.Context, err error) {
	fmt.Printf("🤖 LLM error: %s\n", err.Error())
}

// HandleStreamingFunc handles streaming responses
func (al *ActionLogger) HandleStreamingFunc(ctx context.Context, chunk []byte) error {
	// Optional: Handle streaming output
	return nil
}

// GetActions returns all logged actions
func (al *ActionLogger) GetActions() []session.ActionLog {
	return al.actions
}

// Clear clears all logged actions
func (al *ActionLogger) Clear() {
	al.actions = al.actions[:0]
	al.current = nil
}

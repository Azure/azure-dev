// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package logging

import (
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/require"
)

func TestSessionEventLogger_HandleEvent(t *testing.T) {
	t.Run("AssistantMessage", func(t *testing.T) {
		ch := make(chan Thought, 10)
		logger := NewSessionEventLogger(ch)

		content := "I will analyze the project structure."
		logger.HandleEvent(copilot.SessionEvent{
			Type: copilot.AssistantMessage,
			Data: copilot.Data{Content: &content},
		})

		require.Len(t, ch, 1)
		thought := <-ch
		require.Equal(t, "I will analyze the project structure.", thought.Thought)
		require.Empty(t, thought.Action)
	})

	t.Run("ToolStart", func(t *testing.T) {
		ch := make(chan Thought, 10)
		logger := NewSessionEventLogger(ch)

		toolName := "read_file"
		logger.HandleEvent(copilot.SessionEvent{
			Type: copilot.ToolExecutionStart,
			Data: copilot.Data{
				ToolName:  &toolName,
				Arguments: map[string]any{"path": "/src/main.go"},
			},
		})

		require.Len(t, ch, 1)
		thought := <-ch
		require.Equal(t, "read_file", thought.Action)
		require.Equal(t, "path: /src/main.go", thought.ActionInput)
	})

	t.Run("ToolStartWithMCPToolName", func(t *testing.T) {
		ch := make(chan Thought, 10)
		logger := NewSessionEventLogger(ch)

		mcpToolName := "azd_plan_init"
		logger.HandleEvent(copilot.SessionEvent{
			Type: copilot.ToolExecutionStart,
			Data: copilot.Data{MCPToolName: &mcpToolName},
		})

		require.Len(t, ch, 1)
		thought := <-ch
		require.Equal(t, "azd_plan_init", thought.Action)
	})

	t.Run("SkipsToolPromptThoughts", func(t *testing.T) {
		ch := make(chan Thought, 10)
		logger := NewSessionEventLogger(ch)

		content := "Do I need to use a tool? Yes."
		logger.HandleEvent(copilot.SessionEvent{
			Type: copilot.AssistantMessage,
			Data: copilot.Data{Content: &content},
		})

		require.Empty(t, ch)
	})

	t.Run("NilChannel", func(t *testing.T) {
		logger := NewSessionEventLogger(nil)
		content := "test"
		// Should not panic
		logger.HandleEvent(copilot.SessionEvent{
			Type: copilot.AssistantMessage,
			Data: copilot.Data{Content: &content},
		})
	})
}

func TestExtractToolInputSummary(t *testing.T) {
	t.Run("PathParam", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{"path": "/src/main.go", "content": "data"})
		require.Equal(t, "path: /src/main.go", result)
	})

	t.Run("CommandParam", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{"command": "go build ./..."})
		require.Equal(t, "command: go build ./...", result)
	})

	t.Run("NilArgs", func(t *testing.T) {
		result := extractToolInputSummary(nil)
		require.Empty(t, result)
	})

	t.Run("NonMapArgs", func(t *testing.T) {
		result := extractToolInputSummary("not a map")
		require.Empty(t, result)
	})
}

func TestCompositeEventHandler(t *testing.T) {
	var calls []string

	handler := NewCompositeEventHandler(
		func(e copilot.SessionEvent) { calls = append(calls, "handler1") },
		func(e copilot.SessionEvent) { calls = append(calls, "handler2") },
	)

	handler.HandleEvent(copilot.SessionEvent{Type: copilot.SessionStart})

	require.Equal(t, []string{"handler1", "handler2"}, calls)
}

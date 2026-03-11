// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"os"
	"path/filepath"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/require"
)

func TestExtractToolInputSummary(t *testing.T) {
	t.Run("NilArgs", func(t *testing.T) {
		require.Empty(t, extractToolInputSummary(nil))
	})

	t.Run("NonMap", func(t *testing.T) {
		require.Empty(t, extractToolInputSummary("string"))
	})

	t.Run("PathParam", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{"path": "/src/main.go", "content": "data"})
		require.Equal(t, "path: /src/main.go", result)
	})

	t.Run("CommandParam", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{"command": "go build ./..."})
		require.Equal(t, "command: go build ./...", result)
	})

	t.Run("PatternParam", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{"pattern": "*.go"})
		require.Equal(t, "pattern: *.go", result)
	})

	t.Run("NoMatchingKey", func(t *testing.T) {
		result := extractToolInputSummary(map[string]any{"other": "value"})
		require.Empty(t, result)
	})

	t.Run("Truncation", func(t *testing.T) {
		longPath := "/very/long/path/" + string(make([]byte, 200))
		result := extractToolInputSummary(map[string]any{"path": longPath})
		require.LessOrEqual(t, len(result), 120)
	})
}

func TestExtractIntentFromArgs(t *testing.T) {
	t.Run("Nil", func(t *testing.T) {
		require.Empty(t, extractIntentFromArgs(nil))
	})

	t.Run("IntentField", func(t *testing.T) {
		result := extractIntentFromArgs(map[string]any{"intent": "Analyzing project"})
		require.Equal(t, "Analyzing project", result)
	})

	t.Run("DescriptionField", func(t *testing.T) {
		result := extractIntentFromArgs(map[string]any{"description": "Reading files"})
		require.Equal(t, "Reading files", result)
	})

	t.Run("TextField", func(t *testing.T) {
		result := extractIntentFromArgs(map[string]any{"text": "Working on it"})
		require.Equal(t, "Working on it", result)
	})

	t.Run("NoMatch", func(t *testing.T) {
		result := extractIntentFromArgs(map[string]any{"other": "value"})
		require.Empty(t, result)
	})

	t.Run("Truncation", func(t *testing.T) {
		long := string(make([]byte, 200))
		result := extractIntentFromArgs(map[string]any{"intent": long})
		require.LessOrEqual(t, len(result), 80)
	})
}

func TestToRelativePath(t *testing.T) {
	t.Run("RelativeUnderCwd", func(t *testing.T) {
		cwd, _ := os.Getwd()
		absPath := filepath.Join(cwd, "src", "main.go")
		result := toRelativePath(absPath)
		require.Equal(t, filepath.Join("src", "main.go"), result)
	})

	t.Run("AbsoluteOutsideCwd", func(t *testing.T) {
		// Path that's definitely outside cwd and plugins
		result := toRelativePath("/some/random/path/file.go")
		require.Equal(t, "/some/random/path/file.go", result)
	})

	t.Run("AlreadyRelative", func(t *testing.T) {
		result := toRelativePath("src/main.go")
		// Should return as-is since it's already relative
		require.Equal(t, "src/main.go", result)
	})
}

func TestGetUsageMetrics(t *testing.T) {
	d := &AgentDisplay{
		idleCh: make(chan struct{}, 1),
	}

	// Simulate usage events
	d.HandleEvent(copilot.SessionEvent{
		Type: copilot.AssistantUsage,
		Data: copilot.Data{
			InputTokens:  new(float64(1000)),
			OutputTokens: new(float64(500)),
			Cost:         new(1.0),
			Duration:     new(float64(5000)),
			Model:        new("gpt-4.1"),
		},
	})

	d.HandleEvent(copilot.SessionEvent{
		Type: copilot.AssistantUsage,
		Data: copilot.Data{
			InputTokens:  new(float64(2000)),
			OutputTokens: new(float64(800)),
			Cost:         new(1.0),
			Duration:     new(float64(3000)),
		},
	})

	metrics := d.GetUsageMetrics()
	require.Equal(t, float64(3000), metrics.InputTokens)
	require.Equal(t, float64(1300), metrics.OutputTokens)
	require.Equal(t, float64(1.0), metrics.BillingRate) // last value, not sum
	require.Equal(t, float64(8000), metrics.DurationMS)
	require.Equal(t, "gpt-4.1", metrics.Model)
}

//go:fix inline
func floatPtr(v float64) *float64 { return new(v) }

//go:fix inline
func strPtr(v string) *string { return new(v) }

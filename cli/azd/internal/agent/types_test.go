// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageMetrics_Format(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		u := UsageMetrics{}
		require.Empty(t, u.Format())
	})

	t.Run("BasicTokens", func(t *testing.T) {
		u := UsageMetrics{
			InputTokens:  1500,
			OutputTokens: 500,
		}
		result := u.Format()
		require.Contains(t, result, "1.5K")
		require.Contains(t, result, "500")
		require.Contains(t, result, "2.0K")
	})

	t.Run("WithModel", func(t *testing.T) {
		u := UsageMetrics{
			Model:        "claude-sonnet-4.5",
			InputTokens:  10000,
			OutputTokens: 5000,
		}
		result := u.Format()
		require.Contains(t, result, "claude-sonnet-4.5")
	})

	t.Run("WithCostAndPremium", func(t *testing.T) {
		u := UsageMetrics{
			InputTokens:     50000,
			OutputTokens:    20000,
			BillingRate:     2.0,
			PremiumRequests: 15,
		}
		result := u.Format()
		require.Contains(t, result, "2x per request")
		require.Contains(t, result, "15")
	})

	t.Run("DurationSeconds", func(t *testing.T) {
		u := UsageMetrics{
			InputTokens:  100,
			OutputTokens: 50,
			DurationMS:   45000,
		}
		result := u.Format()
		require.Contains(t, result, "45.0s")
	})

	t.Run("DurationMinutes", func(t *testing.T) {
		u := UsageMetrics{
			InputTokens:  100,
			OutputTokens: 50,
			DurationMS:   125000,
		}
		result := u.Format()
		require.Contains(t, result, "2m")
	})
}

func TestUsageMetrics_TotalTokens(t *testing.T) {
	u := UsageMetrics{InputTokens: 1000, OutputTokens: 500}
	require.Equal(t, float64(1500), u.TotalTokens())
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{0, "0"},
		{500, "500"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{45200, "45.2K"},
		{1000000, "1.0M"},
		{2500000, "2.5M"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			require.Equal(t, tt.expected, formatTokenCount(tt.input))
		})
	}
}

func TestStripMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Plain", "hello world", "hello world"},
		{"Bold", "**bold text**", "bold text"},
		{"Italic", "*italic*", "italic"},
		{"Backticks", "`code`", "code"},
		{"Heading", "# Title", "Title"},
		{"H2", "## Subtitle", "Subtitle"},
		{"H3", "### Section", "Section"},
		{"Mixed", "**bold** and `code` text", "bold and code text"},
		{"Underscore bold", "__bold__", "bold"},
		{"Empty", "", ""},
		{"Multiline heading", "# Title\nsome text", "Title\nsome text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, stripMarkdown(tt.input))
		})
	}
}

func TestAgentOptions(t *testing.T) {
	t.Run("WithSystemMessage", func(t *testing.T) {
		agent := &CopilotAgent{}
		opt := WithSystemMessage("Custom system prompt")
		opt(agent)
		require.Equal(t, "Custom system prompt", agent.systemMessageOverride)
	})

	t.Run("WithModel", func(t *testing.T) {
		agent := &CopilotAgent{}
		opt := WithModel("gpt-4.1")
		opt(agent)
		require.Equal(t, "gpt-4.1", agent.modelOverride)
	})

	t.Run("WithReasoningEffort", func(t *testing.T) {
		agent := &CopilotAgent{}
		opt := WithReasoningEffort("high")
		opt(agent)
		require.Equal(t, "high", agent.reasoningEffortOverride)
	})
}

func TestFormatSessionTime(t *testing.T) {
	t.Run("RFC3339", func(t *testing.T) {
		// Just verify it doesn't crash and returns something
		result := formatSessionTime("2026-03-10T22:30:00Z")
		require.NotEmpty(t, result)
	})

	t.Run("Fallback", func(t *testing.T) {
		result := formatSessionTime("not-a-timestamp-at-all")
		require.Equal(t, "not-a-timestamp-at-", result)
	})

	t.Run("Short fallback", func(t *testing.T) {
		result := formatSessionTime("short")
		require.Equal(t, "short", result)
	})
}

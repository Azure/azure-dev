// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"testing"

	"azure.ai.finetune/pkg/models"
	"github.com/stretchr/testify/require"
)

func TestGetStatusSymbol(t *testing.T) {
	tests := []struct {
		name     string
		status   models.JobStatus
		expected string
	}{
		{
			name:     "StatusPending",
			status:   models.StatusPending,
			expected: "⌛",
		},
		{
			name:     "StatusQueued",
			status:   models.StatusQueued,
			expected: "📚",
		},
		{
			name:     "StatusRunning",
			status:   models.StatusRunning,
			expected: "🔄",
		},
		{
			name:     "StatusSucceeded",
			status:   models.StatusSucceeded,
			expected: "✅",
		},
		{
			name:     "StatusFailed",
			status:   models.StatusFailed,
			expected: "💥",
		},
		{
			name:     "StatusCancelled",
			status:   models.StatusCancelled,
			expected: "❌",
		},
		{
			name:     "UnknownStatus",
			status:   models.JobStatus("unknown"),
			expected: "❓",
		},
		{
			name:     "EmptyStatus",
			status:   models.JobStatus(""),
			expected: "❓",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetStatusSymbol(tt.status)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestIsTerminalStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   models.JobStatus
		expected bool
	}{
		{
			name:     "StatusSucceeded_IsTerminal",
			status:   models.StatusSucceeded,
			expected: true,
		},
		{
			name:     "StatusFailed_IsTerminal",
			status:   models.StatusFailed,
			expected: true,
		},
		{
			name:     "StatusCancelled_IsTerminal",
			status:   models.StatusCancelled,
			expected: true,
		},
		{
			name:     "StatusPending_NotTerminal",
			status:   models.StatusPending,
			expected: false,
		},
		{
			name:     "StatusQueued_NotTerminal",
			status:   models.StatusQueued,
			expected: false,
		},
		{
			name:     "StatusRunning_NotTerminal",
			status:   models.StatusRunning,
			expected: false,
		},
		{
			name:     "StatusPaused_NotTerminal",
			status:   models.StatusPaused,
			expected: false,
		},
		{
			name:     "UnknownStatus_NotTerminal",
			status:   models.JobStatus("unknown"),
			expected: false,
		},
		{
			name:     "EmptyStatus_NotTerminal",
			status:   models.JobStatus(""),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTerminalStatus(tt.status)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestStatusSymbol_ConsistentWithTerminalStatus(t *testing.T) {
	// Terminal statuses should have definitive symbols (not the unknown symbol)
	terminalStatuses := []models.JobStatus{
		models.StatusSucceeded,
		models.StatusFailed,
		models.StatusCancelled,
	}

	for _, status := range terminalStatuses {
		t.Run(string(status), func(t *testing.T) {
			symbol := GetStatusSymbol(status)
			require.NotEqual(t, "❓", symbol, "Terminal status %s should have a known symbol", status)
			require.True(t, IsTerminalStatus(status), "Status %s should be terminal", status)
		})
	}
}

func TestStatusSymbol_NonTerminalStatuses(t *testing.T) {
	// Non-terminal statuses should also have definitive symbols
	nonTerminalStatuses := []models.JobStatus{
		models.StatusPending,
		models.StatusQueued,
		models.StatusRunning,
	}

	for _, status := range nonTerminalStatuses {
		t.Run(string(status), func(t *testing.T) {
			symbol := GetStatusSymbol(status)
			require.NotEqual(t, "❓", symbol, "Non-terminal status %s should have a known symbol", status)
			require.False(t, IsTerminalStatus(status), "Status %s should not be terminal", status)
		})
	}
}

func TestGetStatusSymbol_AllDefinedStatuses(t *testing.T) {
	// Ensure all defined status constants have a symbol mapping
	allStatuses := []models.JobStatus{
		models.StatusPending,
		models.StatusQueued,
		models.StatusRunning,
		models.StatusSucceeded,
		models.StatusFailed,
		models.StatusCancelled,
	}

	for _, status := range allStatuses {
		t.Run(string(status), func(t *testing.T) {
			symbol := GetStatusSymbol(status)
			require.NotEmpty(t, symbol)
			require.NotEqual(t, "❓", symbol)
		})
	}
}

func TestIsTerminalStatus_CaseSensitivity(t *testing.T) {
	// Status values are case-sensitive
	t.Run("UppercaseSucceeded_NotTerminal", func(t *testing.T) {
		result := IsTerminalStatus(models.JobStatus("SUCCEEDED"))
		require.False(t, result)
	})

	t.Run("MixedCaseFailed_NotTerminal", func(t *testing.T) {
		result := IsTerminalStatus(models.JobStatus("Failed"))
		require.False(t, result)
	})

	t.Run("LowercaseCancelled_IsTerminal", func(t *testing.T) {
		result := IsTerminalStatus(models.JobStatus("cancelled"))
		require.True(t, result)
	})
}

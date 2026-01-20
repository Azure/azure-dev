// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"testing"

	"azure.ai.finetune/pkg/models"
	"github.com/stretchr/testify/require"
)

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

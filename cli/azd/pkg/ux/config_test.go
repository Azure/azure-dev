// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSetAndGetPromptTimeout(t *testing.T) {
	// Save the initial state and restore after test
	original := GetPromptTimeout()
	defer SetPromptTimeout(original)

	t.Run("default is zero", func(t *testing.T) {
		SetPromptTimeout(0)
		require.Equal(t, time.Duration(0), GetPromptTimeout())
	})

	t.Run("can set and get timeout", func(t *testing.T) {
		timeout := 30 * time.Second
		SetPromptTimeout(timeout)
		require.Equal(t, timeout, GetPromptTimeout())
	})

	t.Run("can update timeout", func(t *testing.T) {
		SetPromptTimeout(10 * time.Second)
		require.Equal(t, 10*time.Second, GetPromptTimeout())

		SetPromptTimeout(60 * time.Second)
		require.Equal(t, 60*time.Second, GetPromptTimeout())
	})

	t.Run("can disable timeout by setting to zero", func(t *testing.T) {
		SetPromptTimeout(30 * time.Second)
		require.Equal(t, 30*time.Second, GetPromptTimeout())

		SetPromptTimeout(0)
		require.Equal(t, time.Duration(0), GetPromptTimeout())
	})
}

func TestErrPromptTimeout_Error(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "30 seconds",
			duration: 30 * time.Second,
			expected: "prompt timed out after 30 seconds",
		},
		{
			name:     "60 seconds",
			duration: 60 * time.Second,
			expected: "prompt timed out after 60 seconds",
		},
		{
			name:     "5 seconds",
			duration: 5 * time.Second,
			expected: "prompt timed out after 5 seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &ErrPromptTimeout{Duration: tt.duration}
			require.Equal(t, tt.expected, err.Error())
		})
	}
}

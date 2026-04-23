// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package common

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDetailedError(t *testing.T) {
	inner := errors.New("inner error")
	err := NewDetailedError("something went wrong", inner)

	require.NotNil(t, err)
	require.Equal(t, "something went wrong", err.Description())
	require.Equal(t, inner, err.Unwrap())
}

func TestDetailedError_Error(t *testing.T) {
	tests := []struct {
		name        string
		description string
		inner       error
		expected    string
	}{
		{
			name:        "simple error",
			description: "operation failed",
			inner:       errors.New("connection refused"),
			expected:    "operation failed\n\nDetails:\nconnection refused",
		},
		{
			name:        "formatted inner error",
			description: "deploy failed",
			inner:       fmt.Errorf("status code: %d", 500),
			expected:    "deploy failed\n\nDetails:\nstatus code: 500",
		},
		{
			name:        "empty description",
			description: "",
			inner:       errors.New("some error"),
			expected:    "\n\nDetails:\nsome error",
		},
		{
			name:        "multiline inner error",
			description: "build failed",
			inner:       errors.New("line 1\nline 2\nline 3"),
			expected:    "build failed\n\nDetails:\nline 1\nline 2\nline 3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewDetailedError(tt.description, tt.inner)
			require.Equal(t, tt.expected, err.Error())
		})
	}
}

func TestDetailedError_Description(t *testing.T) {
	tests := []struct {
		name        string
		description string
	}{
		{"non-empty", "failed to deploy"},
		{"empty", ""},
		{"with special characters", "error: 'file not found' (code=404)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewDetailedError(tt.description, errors.New("inner"))
			require.Equal(t, tt.description, err.Description())
		})
	}
}

func TestDetailedError_Unwrap(t *testing.T) {
	inner := errors.New("root cause")
	err := NewDetailedError("wrapper", inner)

	// errors.Is should work through the wrapping
	require.True(t, errors.Is(err, inner))

	// errors.Unwrap should return the inner error
	require.Equal(t, inner, errors.Unwrap(err))
}

func TestDetailedError_UnwrapChain(t *testing.T) {
	root := errors.New("root cause")
	mid := fmt.Errorf("mid-level: %w", root)
	outer := NewDetailedError("top-level failure", mid)

	// Should be able to find the root cause through the chain
	require.True(t, errors.Is(outer, root))
	require.True(t, errors.Is(outer, mid))
}

func TestDetailedError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	detailed := NewDetailedError("description", inner)
	outer := fmt.Errorf("outer: %w", detailed)

	var target *DetailedError
	require.True(t, errors.As(outer, &target))
	require.Equal(t, "description", target.Description())
}

func TestDetailedError_ImplementsErrorInterface(t *testing.T) {
	inner := errors.New("inner")
	err := NewDetailedError("desc", inner)

	// Verify it satisfies the error interface
	var e error = err
	require.NotEmpty(t, e.Error())
}

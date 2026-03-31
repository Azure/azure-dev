// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorSuggestion(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name: "LocalErrorWithSuggestion",
			err: &LocalError{
				Message:    "missing config",
				Suggestion: "Run azd init first",
			},
			expected: "Run azd init first",
		},
		{
			name: "LocalErrorNoSuggestion",
			err: &LocalError{
				Message: "missing config",
			},
			expected: "",
		},
		{
			name: "ServiceErrorWithSuggestion",
			err: &ServiceError{
				Message:    "rate limited",
				Suggestion: "Retry after 60 seconds",
			},
			expected: "Retry after 60 seconds",
		},
		{
			name: "ServiceErrorNoSuggestion",
			err: &ServiceError{
				Message: "rate limited",
			},
			expected: "",
		},
		{
			name:     "GenericError",
			err:      &testGenericError{msg: "generic"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ErrorSuggestion(tt.err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name: "LocalError",
			err: &LocalError{
				Message: "config invalid",
			},
			expected: "config invalid",
		},
		{
			name: "ServiceError",
			err: &ServiceError{
				Message: "service unavailable",
			},
			expected: "service unavailable",
		},
		{
			name: "LocalErrorEmptyMessage",
			err: &LocalError{
				Message: "",
			},
			expected: "",
		},
		{
			name:     "GenericError",
			err:      &testGenericError{msg: "generic"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ErrorMessage(tt.err)
			require.Equal(t, tt.expected, result)
		})
	}
}

// testGenericError is a plain error for testing non-extension error types.
type testGenericError struct {
	msg string
}

func (e *testGenericError) Error() string {
	return e.msg
}

func TestVersion_IsSet(t *testing.T) {
	require.NotEmpty(t, Version)
	// Validate semver format (major.minor.patch with optional pre-release)
	// instead of an exact value, since Update-CliVersion.ps1 auto-bumps this on each release.
	semverPattern := regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$`)
	require.Regexp(t, semverPattern, Version)
}

func TestAiErrorConstants(t *testing.T) {
	// Verify domain constant
	require.Equal(t, "azd.ai", AiErrorDomain)

	// Verify all reason codes are non-empty and unique
	reasons := []string{
		AiErrorReasonMissingSubscription,
		AiErrorReasonLocationRequired,
		AiErrorReasonQuotaLocation,
		AiErrorReasonModelNotFound,
		AiErrorReasonNoModelsMatch,
		AiErrorReasonNoDeploymentMatch,
		AiErrorReasonNoValidSkus,
		AiErrorReasonNoLocationsWithQuota,
		AiErrorReasonInvalidCapacity,
		AiErrorReasonInteractiveRequired,
	}

	seen := make(map[string]bool, len(reasons))
	for _, r := range reasons {
		require.NotEmpty(t, r, "reason code must not be empty")
		require.False(t, seen[r], "duplicate reason code: %s", r)
		seen[r] = true
	}
}

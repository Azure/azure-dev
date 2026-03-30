// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"
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
			err:      errors.New("generic"),
			expected: "",
		},
		{
			name:     "NilError",
			err:      nil,
			expected: "",
		},
		{
			name: "WrappedLocalError",
			err: fmt.Errorf("operation failed: %w", &LocalError{
				Message:    "bad input",
				Suggestion: "Fix the input",
			}),
			expected: "Fix the input",
		},
		{
			name: "WrappedServiceError",
			err: fmt.Errorf("deploy failed: %w", &ServiceError{
				Message:    "quota exceeded",
				Suggestion: "Request a quota increase",
			}),
			expected: "Request a quota increase",
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
			err:      errors.New("generic"),
			expected: "",
		},
		{
			name:     "NilError",
			err:      nil,
			expected: "",
		},
		{
			name: "WrappedLocalError",
			err: fmt.Errorf("op: %w", &LocalError{
				Message: "wrapped local",
			}),
			expected: "wrapped local",
		},
		{
			name: "WrappedServiceError",
			err: fmt.Errorf("op: %w", &ServiceError{
				Message: "wrapped service",
			}),
			expected: "wrapped service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ErrorMessage(tt.err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestVersion_IsSet(t *testing.T) {
	require.NotEmpty(t, Version)
	require.Equal(t, "0.1.0", Version)
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

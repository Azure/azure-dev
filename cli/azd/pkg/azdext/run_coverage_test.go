// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"
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

func TestWithExitCode(t *testing.T) {
	called := false
	extractor := func(err error) (int, bool) {
		called = true
		return 42, true
	}

	opt := WithExitCode(extractor)

	var cfg runConfig
	opt(&cfg)

	require.NotNil(t, cfg.exitCodeFunc)

	code, ok := cfg.exitCodeFunc(errors.New("test"))
	require.True(t, called)
	require.True(t, ok)
	require.Equal(t, 42, code)
}

func TestWithExitCode_NotMatched(t *testing.T) {
	extractor := func(err error) (int, bool) {
		return 0, false
	}

	var cfg runConfig
	WithExitCode(extractor)(&cfg)

	code, ok := cfg.exitCodeFunc(errors.New("test"))
	require.False(t, ok)
	require.Equal(t, 0, code)
}

func TestWithExitCode_ZeroCodeTreatedAsUnmatched(t *testing.T) {
	// Returning (0, true) should be treated as unmatched by Run()
	// because exit code 0 would mask a real error.
	extractor := func(err error) (int, bool) {
		return 0, true
	}

	var cfg runConfig
	WithExitCode(extractor)(&cfg)

	code, ok := cfg.exitCodeFunc(errors.New("test"))
	// The func itself returns (0, true), but Run() checks code != 0
	require.True(t, ok)
	require.Equal(t, 0, code)
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

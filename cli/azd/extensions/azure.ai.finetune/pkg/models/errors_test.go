// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorDetail_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ErrorDetail
		expected string
	}{
		{
			name: "SimpleMessage",
			err: &ErrorDetail{
				Code:    ErrorCodeInvalidRequest,
				Message: "Invalid request parameters",
			},
			expected: "Invalid request parameters",
		},
		{
			name: "EmptyMessage",
			err: &ErrorDetail{
				Code:    ErrorCodeNotFound,
				Message: "",
			},
			expected: "",
		},
		{
			name: "MessageWithDetails",
			err: &ErrorDetail{
				Code:       ErrorCodeRateLimited,
				Message:    "Rate limit exceeded. Please try again later.",
				Retryable:  true,
				VendorCode: "rate_limit_exceeded",
			},
			expected: "Rate limit exceeded. Please try again later.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

func TestErrorCodes_AreDistinct(t *testing.T) {
	// Ensures error codes are unique - prevents copy-paste bugs where
	// two constants might accidentally have the same value
	codes := []string{
		ErrorCodeInvalidRequest,
		ErrorCodeNotFound,
		ErrorCodeUnauthorized,
		ErrorCodeForbidden,
		ErrorCodeRateLimited,
		ErrorCodeServiceUnavailable,
		ErrorCodeInternalError,
		ErrorCodeInvalidModel,
		ErrorCodeInvalidFileSize,
		ErrorCodeOperationFailed,
	}

	seen := make(map[string]bool)
	for _, code := range codes {
		require.False(t, seen[code], "Duplicate error code found: %s - each error code must be unique", code)
		require.NotEmpty(t, code, "Error code should not be empty")
		seen[code] = true
	}
}

func TestErrorDetail_Properties(t *testing.T) {
	t.Run("RetryableError", func(t *testing.T) {
		err := &ErrorDetail{
			Code:      ErrorCodeRateLimited,
			Message:   "Rate limited",
			Retryable: true,
		}
		require.True(t, err.Retryable)
	})

	t.Run("NonRetryableError", func(t *testing.T) {
		err := &ErrorDetail{
			Code:      ErrorCodeInvalidRequest,
			Message:   "Invalid request",
			Retryable: false,
		}
		require.False(t, err.Retryable)
	})

	t.Run("WithVendorError", func(t *testing.T) {
		vendorErr := &ErrorDetail{Message: "vendor specific error"}
		err := &ErrorDetail{
			Code:        ErrorCodeInternalError,
			Message:     "Internal error occurred",
			VendorError: vendorErr,
			VendorCode:  "internal_error",
		}
		require.Equal(t, "internal_error", err.VendorCode)
		require.NotNil(t, err.VendorError)
	})
}

func TestErrorDetail_ImplementsError(t *testing.T) {
	// Verify that ErrorDetail implements the error interface
	var _ error = &ErrorDetail{}

	err := &ErrorDetail{
		Code:    ErrorCodeNotFound,
		Message: "Resource not found",
	}

	// Can be used as error
	var genericErr error = err
	require.Equal(t, "Resource not found", genericErr.Error())
}

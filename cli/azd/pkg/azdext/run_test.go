// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorSuggestion(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "LocalErrorWithSuggestion",
			err: &LocalError{
				Message:    "invalid config",
				Code:       "invalid_config",
				Category:   LocalErrorCategoryValidation,
				Suggestion: "Check your azure.yaml file",
			},
			want: "Check your azure.yaml file",
		},
		{
			name: "ServiceErrorWithSuggestion",
			err: &ServiceError{
				Message:    "rate limited",
				ErrorCode:  "TooManyRequests",
				StatusCode: 429,
				Suggestion: "Retry with exponential backoff",
			},
			want: "Retry with exponential backoff",
		},
		{
			name: "LocalErrorWithoutSuggestion",
			err: &LocalError{
				Message:  "missing field",
				Code:     "missing_field",
				Category: LocalErrorCategoryValidation,
			},
			want: "",
		},
		{
			name: "ServiceErrorWithoutSuggestion",
			err: &ServiceError{
				Message:    "not found",
				ErrorCode:  "NotFound",
				StatusCode: 404,
			},
			want: "",
		},
		{
			name: "PlainError",
			err:  errors.New("something went wrong"),
			want: "",
		},
		{
			name: "NilError",
			err:  nil,
			want: "",
		},
		{
			name: "WrappedLocalError",
			err: fmt.Errorf("operation failed: %w", &LocalError{
				Message:    "bad input",
				Suggestion: "Fix the input",
			}),
			want: "Fix the input",
		},
		{
			name: "WrappedServiceError",
			err: fmt.Errorf("deploy failed: %w", &ServiceError{
				Message:    "quota exceeded",
				Suggestion: "Request a quota increase",
			}),
			want: "Request a quota increase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ErrorSuggestion(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "LocalError",
			err: &LocalError{
				Message:  "invalid config",
				Code:     "invalid_config",
				Category: LocalErrorCategoryValidation,
			},
			want: "invalid config",
		},
		{
			name: "ServiceError",
			err: &ServiceError{
				Message:    "rate limited",
				ErrorCode:  "TooManyRequests",
				StatusCode: 429,
			},
			want: "rate limited",
		},
		{
			name: "LocalErrorEmptyMessage",
			err: &LocalError{
				Code:     "no_msg",
				Category: LocalErrorCategoryLocal,
			},
			want: "",
		},
		{
			name: "ServiceErrorEmptyMessage",
			err: &ServiceError{
				ErrorCode:  "Unknown",
				StatusCode: 500,
			},
			want: "",
		},
		{
			name: "PlainError",
			err:  errors.New("plain error"),
			want: "",
		},
		{
			name: "NilError",
			err:  nil,
			want: "",
		},
		{
			name: "WrappedLocalError",
			err: fmt.Errorf("op: %w", &LocalError{
				Message: "wrapped local",
			}),
			want: "wrapped local",
		},
		{
			name: "WrappedServiceError",
			err: fmt.Errorf("op: %w", &ServiceError{
				Message: "wrapped service",
			}),
			want: "wrapped service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ErrorMessage(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

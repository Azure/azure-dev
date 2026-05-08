// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"errors"
	"testing"
)

func TestAPIError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *APIError
		expected string
	}{
		{
			name:     "with message",
			err:      &APIError{StatusCode: 409, Code: "Conflict", Message: "already exists"},
			expected: "API error (409): Conflict - already exists",
		},
		{
			name:     "without message",
			err:      &APIError{StatusCode: 500, Code: "raw body content"},
			expected: "API error (500): raw body content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.expected {
				t.Errorf("APIError.Error() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestAPIError_ErrorsAs(t *testing.T) {
	err := &APIError{StatusCode: 403, Code: "Forbidden", Message: "access denied"}
	wrapped := errors.Join(errors.New("request failed"), err)

	var apiErr *APIError
	if !errors.As(wrapped, &apiErr) {
		t.Fatal("errors.As failed to unwrap APIError")
	}
	if apiErr.StatusCode != 403 {
		t.Errorf("expected status 403, got %d", apiErr.StatusCode)
	}
}

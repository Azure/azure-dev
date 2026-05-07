// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"errors"
	"testing"
)

func TestResourceNotFound_with_error(t *testing.T) {
	inner := errors.New("some azure error")
	err := ResourceNotFound(inner)

	if err == nil {
		t.Fatal("ResourceNotFound should return non-nil error")
	}

	expected := "resource not found: some azure error"
	if err.Error() != expected {
		t.Fatalf("Error() = %q, want %q", err.Error(), expected)
	}
}

func TestResourceNotFound_with_nil_error(t *testing.T) {
	err := ResourceNotFound(nil)

	if err == nil {
		t.Fatal("ResourceNotFound(nil) should return non-nil error")
	}

	expected := "resource not found: <nil>"
	if err.Error() != expected {
		t.Fatalf("Error() = %q, want %q", err.Error(), expected)
	}
}

func TestResourceNotFoundError_is_error_type(t *testing.T) {
	inner := errors.New("not found")
	err := ResourceNotFound(inner)

	var rnfErr *ResourceNotFoundError
	if !errors.As(err, &rnfErr) {
		t.Fatal("ResourceNotFound should return *ResourceNotFoundError")
	}
}

func TestResourceNotFoundError_errors_As(t *testing.T) {
	inner := errors.New("original cause")
	err := ResourceNotFound(inner)

	// Verify that errors.As can extract the ResourceNotFoundError
	var target *ResourceNotFoundError
	if !errors.As(err, &target) {
		t.Fatal("errors.As should find *ResourceNotFoundError")
	}

	// The inner error message should be embedded
	if target.Error() != "resource not found: original cause" {
		t.Fatalf("target.Error() = %q, want %q", target.Error(), "resource not found: original cause")
	}
}

func TestResourceNotFoundError_different_inner_errors(t *testing.T) {
	tests := []struct {
		name     string
		inner    error
		expected string
	}{
		{
			name:     "simple error",
			inner:    errors.New("not found"),
			expected: "resource not found: not found",
		},
		{
			name:     "empty error message",
			inner:    errors.New(""),
			expected: "resource not found: ",
		},
		{
			name:     "nil error",
			inner:    nil,
			expected: "resource not found: <nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ResourceNotFound(tt.inner)
			if err.Error() != tt.expected {
				t.Fatalf("Error() = %q, want %q", err.Error(), tt.expected)
			}
		})
	}
}

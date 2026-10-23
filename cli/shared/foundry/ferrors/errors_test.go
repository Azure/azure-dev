// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ferrors

import (
	"errors"
	"testing"
)

func TestErrorFields(t *testing.T) {
	err := InvalidFileRefValidation("bad ref", "use a relative path")
	if err.Code != CodeInvalidFileRef {
		t.Fatalf("Code = %q, want %q", err.Code, CodeInvalidFileRef)
	}
	if err.Category != CategoryValidation {
		t.Fatalf("Category = %q, want %q", err.Category, CategoryValidation)
	}
	if err.Message != "bad ref" {
		t.Fatalf("Message = %q, want %q", err.Message, "bad ref")
	}
	if err.Suggestion != "use a relative path" {
		t.Fatalf("Suggestion = %q, want %q", err.Suggestion, "use a relative path")
	}
	if err.Error() != "bad ref" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "bad ref")
	}
	if !errors.As(err, new(*Error)) {
		t.Fatal("expected errors.As to match *Error")
	}
}

func TestInternalErrorFields(t *testing.T) {
	err := InvalidFileRefInternal("unexpected failure")
	if err.Code != CodeInvalidFileRef {
		t.Fatalf("Code = %q, want %q", err.Code, CodeInvalidFileRef)
	}
	if err.Category != CategoryInternal {
		t.Fatalf("Category = %q, want %q", err.Category, CategoryInternal)
	}
	if err.Suggestion != "" {
		t.Fatalf("Suggestion = %q, want empty", err.Suggestion)
	}
}

func TestNilErrorString(t *testing.T) {
	var err *Error
	if err.Error() != "" {
		t.Fatalf("nil Error() = %q, want empty", err.Error())
	}
}

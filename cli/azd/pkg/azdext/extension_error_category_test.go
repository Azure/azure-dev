// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import "testing"

func TestNormalizeLocalErrorCategory(t *testing.T) {
	tests := []struct {
		name     string
		input    LocalErrorCategory
		expected LocalErrorCategory
	}{
		{name: "validation", input: "validation", expected: LocalErrorCategoryValidation},
		{name: "auth", input: "auth", expected: LocalErrorCategoryAuth},
		{name: "dependency", input: "dependency", expected: LocalErrorCategoryDependency},
		{name: "compatibility", input: "compatibility", expected: LocalErrorCategoryCompatibility},
		{name: "user", input: "user", expected: LocalErrorCategoryUser},
		{name: "internal", input: "internal", expected: LocalErrorCategoryInternal},
		{name: "dot variant", input: "dependency.error", expected: LocalErrorCategoryLocal},
		{name: "dash variant", input: "compatibility-check", expected: LocalErrorCategoryLocal},
		{name: "unknown", input: "custom", expected: LocalErrorCategoryLocal},
		{name: "empty", input: "", expected: LocalErrorCategoryLocal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeLocalErrorCategory(tt.input); got != tt.expected {
				t.Fatalf("NormalizeLocalErrorCategory(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseLocalErrorCategory(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected LocalErrorCategory
	}{
		{name: "known", input: "validation", expected: LocalErrorCategoryValidation},
		{name: "unknown", input: "custom", expected: LocalErrorCategoryLocal},
		{name: "empty", input: "", expected: LocalErrorCategoryLocal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseLocalErrorCategory(tt.input); got != tt.expected {
				t.Fatalf("ParseLocalErrorCategory(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

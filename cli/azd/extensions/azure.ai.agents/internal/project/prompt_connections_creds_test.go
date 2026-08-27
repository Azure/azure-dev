// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import "testing"

// TestExpandCredentialPlaceholders verifies ${ENV_VAR} credential references
// are resolved before reaching the connection API, and that an unset variable
// fails loudly instead of storing the literal placeholder as the secret.
func TestExpandCredentialPlaceholders(t *testing.T) {
	t.Setenv("PROMPT_TEST_API_KEY", "s3cret")

	got, err := expandCredentialPlaceholders("search", map[string]any{
		"key":     "${PROMPT_TEST_API_KEY}",
		"literal": "not-a-placeholder",
		"number":  42,
	})
	if err != nil {
		t.Fatalf("expandCredentialPlaceholders: %v", err)
	}
	if got["key"] != "s3cret" {
		t.Errorf("key: got %v, want s3cret", got["key"])
	}
	if got["literal"] != "not-a-placeholder" {
		t.Errorf("literal: got %v", got["literal"])
	}
	if got["number"] != 42 {
		t.Errorf("number: got %v", got["number"])
	}

	if _, err := expandCredentialPlaceholders("search", map[string]any{
		"key": "${PROMPT_TEST_MISSING_KEY}",
	}); err == nil {
		t.Error("expected an error for an unset environment variable")
	}
}

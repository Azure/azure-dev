// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"
)

func TestNormalizeCredentials_CustomKeys_FlatToNested(t *testing.T) {
	t.Parallel()

	// Old-format flat credentials should be wrapped under "keys"
	creds := map[string]any{"key": "${FOUNDRY_TOOL_CONTEXT7_KEY}"}
	result := normalizeCredentials("CustomKeys", creds)

	keysRaw, ok := result["keys"]
	if !ok {
		t.Fatal("Expected 'keys' wrapper in normalized credentials")
	}
	keys, ok := keysRaw.(map[string]any)
	if !ok {
		t.Fatalf("Expected keys to be map[string]any, got %T", keysRaw)
	}
	if keys["key"] != "${FOUNDRY_TOOL_CONTEXT7_KEY}" {
		t.Errorf("Expected key value preserved, got %v", keys["key"])
	}
}

func TestNormalizeCredentials_CustomKeys_AlreadyNested(t *testing.T) {
	t.Parallel()

	// Already-correct nested credentials should be returned as-is
	creds := map[string]any{
		"keys": map[string]any{"key": "${FOUNDRY_TOOL_CONTEXT7_KEY}"},
	}
	result := normalizeCredentials("CustomKeys", creds)

	keysRaw, ok := result["keys"]
	if !ok {
		t.Fatal("Expected 'keys' wrapper preserved")
	}
	keys, ok := keysRaw.(map[string]any)
	if !ok {
		t.Fatalf("Expected keys to be map[string]any, got %T", keysRaw)
	}
	if keys["key"] != "${FOUNDRY_TOOL_CONTEXT7_KEY}" {
		t.Errorf("Expected key value preserved, got %v", keys["key"])
	}
	if len(result) != 1 {
		t.Errorf("Expected only 'keys' in result, got %d entries", len(result))
	}
}

func TestNormalizeCredentials_OAuth2_Unchanged(t *testing.T) {
	t.Parallel()

	// Non-CustomKeys auth types should be returned unchanged
	creds := map[string]any{
		"clientId":     "${VAR_ID}",
		"clientSecret": "${VAR_SECRET}",
	}
	result := normalizeCredentials("OAuth2", creds)

	if _, hasKeys := result["keys"]; hasKeys {
		t.Error("OAuth2 credentials should not be wrapped in 'keys'")
	}
	if result["clientId"] != "${VAR_ID}" {
		t.Errorf("Expected clientId preserved, got %v", result["clientId"])
	}
}

func TestNormalizeCredentials_EmptyCredentials(t *testing.T) {
	t.Parallel()

	result := normalizeCredentials("CustomKeys", nil)
	if result != nil {
		t.Errorf("Expected nil for nil input, got %v", result)
	}

	result = normalizeCredentials("CustomKeys", map[string]any{})
	if len(result) != 0 {
		t.Errorf("Expected empty map for empty input, got %v", result)
	}
}

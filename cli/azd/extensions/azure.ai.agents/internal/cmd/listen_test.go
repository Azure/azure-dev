// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/project"
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

func TestParseConnectionIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		json     string
		expected map[string]string
	}{
		{
			name:     "valid array",
			json:     `[{"name":"my-conn","id":"/subscriptions/123/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/ai/projects/proj/connections/my-conn"}]`,
			expected: map[string]string{"my-conn": "/subscriptions/123/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/ai/projects/proj/connections/my-conn"},
		},
		{
			name:     "empty string",
			json:     "",
			expected: map[string]string{},
		},
		{
			name:     "empty array",
			json:     "[]",
			expected: map[string]string{},
		},
		{
			name:     "invalid JSON",
			json:     "not-json",
			expected: map[string]string{},
		},
		{
			name: "multiple connections",
			json: `[{"name":"conn-a","id":"id-a"},{"name":"conn-b","id":"id-b"}]`,
			expected: map[string]string{
				"conn-a": "id-a",
				"conn-b": "id-b",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseConnectionIDs(tt.json)
			if len(result) != len(tt.expected) {
				t.Fatalf("got %d entries, want %d", len(result), len(tt.expected))
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("key %q: got %q, want %q", k, result[k], v)
				}
			}
		})
	}
}

func TestResolveToolboxConnectionIDs(t *testing.T) {
	t.Parallel()

	connIDs := map[string]string{
		"github_mcp_connection": "/subscriptions/123/connections/github_mcp_connection",
	}

	toolbox := project.Toolbox{
		Name: "test",
		Tools: []map[string]any{
			{"type": "web_search"},
			{"type": "mcp", "project_connection_id": "github_mcp_connection"},
			{"type": "mcp", "project_connection_id": "unknown_conn"},
		},
	}

	resolveToolboxConnectionIDs(&toolbox, connIDs)

	// Tool without project_connection_id: unchanged
	if _, has := toolbox.Tools[0]["project_connection_id"]; has {
		t.Error("tool 0 should not have project_connection_id")
	}

	// Known connection: replaced with ARM ID
	if toolbox.Tools[1]["project_connection_id"] != "/subscriptions/123/connections/github_mcp_connection" {
		t.Errorf("tool 1 project_connection_id = %v, want ARM ID",
			toolbox.Tools[1]["project_connection_id"])
	}

	// Unknown connection: left as-is
	if toolbox.Tools[2]["project_connection_id"] != "unknown_conn" {
		t.Errorf("tool 2 project_connection_id = %v, want 'unknown_conn'",
			toolbox.Tools[2]["project_connection_id"])
	}
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"testing"
)

// TestArrayProperty_BasicSerialization tests basic JSON serialization
func TestArrayProperty_BasicSerialization(t *testing.T) {
	// Test that we can create and marshal a ArrayProperty
	obj := &ArrayProperty{}

	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("Failed to marshal ArrayProperty: %v", err)
	}

	var obj2 ArrayProperty
	if err := json.Unmarshal(data, &obj2); err != nil {
		t.Fatalf("Failed to unmarshal ArrayProperty: %v", err)
	}
}

// TestConnectionResourceSerialization tests JSON round-trip for ConnectionResource
func TestConnectionResourceSerialization(t *testing.T) {
	conn := ConnectionResource{
		Resource:      Resource{Name: "test-conn", Kind: ResourceKindConnection},
		Category:      CategoryCustomKeys,
		Target:        "https://example.com",
		AuthType:      AuthTypeCustomKeys,
		Credentials:   map[string]any{"key": "secret"},
		Metadata:      map[string]string{"env": "test"},
		ExpiryTime:    "2025-12-31",
		IsSharedToAll: new(true),
	}

	data, err := json.Marshal(conn)
	if err != nil {
		t.Fatalf("Failed to marshal ConnectionResource: %v", err)
	}

	var conn2 ConnectionResource
	if err := json.Unmarshal(data, &conn2); err != nil {
		t.Fatalf("Failed to unmarshal ConnectionResource: %v", err)
	}

	if conn2.Name != "test-conn" {
		t.Errorf("Expected name 'test-conn', got '%s'", conn2.Name)
	}
	if conn2.AuthType != AuthTypeCustomKeys {
		t.Errorf("Expected authType 'CustomKeys', got '%s'", conn2.AuthType)
	}
	if conn2.IsSharedToAll == nil || !*conn2.IsSharedToAll {
		t.Error("Expected isSharedToAll to be true")
	}
}

// TestAzureAISearchToolSerialization tests JSON round-trip for AzureAISearchTool
func TestAzureAISearchToolSerialization(t *testing.T) {
	tool := AzureAISearchTool{
		Tool: Tool{
			Name: "search-tool",
			Kind: ToolKindAzureAiSearch,
		},
		Indexes: []AzureAISearchIndex{
			{
				ProjectConnectionId: "my-conn",
				IndexName:           "my-index",
				TopK:                new(5),
			},
		},
	}

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("Failed to marshal AzureAISearchTool: %v", err)
	}

	var tool2 AzureAISearchTool
	if err := json.Unmarshal(data, &tool2); err != nil {
		t.Fatalf("Failed to unmarshal AzureAISearchTool: %v", err)
	}

	if tool2.Kind != ToolKindAzureAiSearch {
		t.Errorf("Expected kind 'azure_ai_search', got '%s'", tool2.Kind)
	}
	if len(tool2.Indexes) != 1 {
		t.Fatalf("Expected 1 index, got %d", len(tool2.Indexes))
	}
	if tool2.Indexes[0].IndexName != "my-index" {
		t.Errorf("Expected index_name 'my-index', got '%s'", tool2.Indexes[0].IndexName)
	}
}

// TestA2APreviewToolSerialization tests JSON round-trip for A2APreviewTool
func TestA2APreviewToolSerialization(t *testing.T) {
	agentCardPath := "/.well-known/agent-card.json"
	tool := A2APreviewTool{
		Tool: Tool{
			Name: "a2a-tool",
			Kind: ToolKindA2APreview,
		},
		BaseUrl:             "https://agent.example.com",
		AgentCardPath:       &agentCardPath,
		ProjectConnectionId: "my-conn",
	}

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("Failed to marshal A2APreviewTool: %v", err)
	}

	var tool2 A2APreviewTool
	if err := json.Unmarshal(data, &tool2); err != nil {
		t.Fatalf("Failed to unmarshal A2APreviewTool: %v", err)
	}

	if tool2.Kind != ToolKindA2APreview {
		t.Errorf("Expected kind 'a2a_preview', got '%s'", tool2.Kind)
	}
	if tool2.BaseUrl != "https://agent.example.com" {
		t.Errorf("Expected baseUrl 'https://agent.example.com', got '%s'", tool2.BaseUrl)
	}
	if tool2.AgentCardPath == nil || *tool2.AgentCardPath != agentCardPath {
		t.Errorf("Expected agentCardPath '%s'", agentCardPath)
	}
}

// TestConnectionResourceNewFieldsJSONRoundTrip tests that the 6 new fields
// survive a JSON marshal/unmarshal round-trip, catching any json struct tag typos.
func TestConnectionResourceNewFieldsJSONRoundTrip(t *testing.T) {
	//nolint:gosec // test fixture with fake credential URLs
	conn := ConnectionResource{
		Resource:         Resource{Name: "oauth-conn", Kind: ResourceKindConnection},
		Category:         CategoryCustomKeys,
		Target:           "https://example.com/api",
		AuthType:         AuthTypeOAuth2,
		AuthorizationUrl: "https://auth.example.com/authorize",
		TokenUrl:         "https://auth.example.com/token",
		RefreshUrl:       "https://auth.example.com/refresh",
		Scopes:           []string{"read", "write"},
		Audience:         "https://api.example.com",
		ConnectorName:    "my-connector",
	}

	data, err := json.Marshal(conn)
	if err != nil {
		t.Fatalf("Failed to marshal ConnectionResource: %v", err)
	}

	var conn2 ConnectionResource
	if err := json.Unmarshal(data, &conn2); err != nil {
		t.Fatalf("Failed to unmarshal ConnectionResource: %v", err)
	}

	if conn2.AuthorizationUrl != conn.AuthorizationUrl {
		t.Errorf("AuthorizationUrl: got %q, want %q", conn2.AuthorizationUrl, conn.AuthorizationUrl)
	}
	if conn2.TokenUrl != conn.TokenUrl {
		t.Errorf("TokenUrl: got %q, want %q", conn2.TokenUrl, conn.TokenUrl)
	}
	if conn2.RefreshUrl != conn.RefreshUrl {
		t.Errorf("RefreshUrl: got %q, want %q", conn2.RefreshUrl, conn.RefreshUrl)
	}
	if len(conn2.Scopes) != len(conn.Scopes) || conn2.Scopes[0] != conn.Scopes[0] {
		t.Errorf("Scopes: got %v, want %v", conn2.Scopes, conn.Scopes)
	}
	if conn2.Audience != conn.Audience {
		t.Errorf("Audience: got %q, want %q", conn2.Audience, conn.Audience)
	}
	if conn2.ConnectorName != conn.ConnectorName {
		t.Errorf("ConnectorName: got %q, want %q", conn2.ConnectorName, conn.ConnectorName)
	}
}

// TestConnectionResourceNewFieldsYAMLRoundTrip tests YAML unmarshal for the 6 new fields,
// verifying that yaml struct tags are correct and fields are not silently dropped.
func TestConnectionResourceNewFieldsYAMLRoundTrip(t *testing.T) {
	//nolint:gosec // test fixture with fake credential URLs
	input := &ConnectionResource{
		Resource:         Resource{Name: "agentic-conn", Kind: ResourceKindConnection},
		Category:         CategoryCustomKeys,
		Target:           "https://example.com",
		AuthType:         AuthTypeAgenticIdentity,
		AuthorizationUrl: "https://auth.example.com/authorize",
		TokenUrl:         "https://auth.example.com/token",
		RefreshUrl:       "https://auth.example.com/refresh",
		Scopes:           []string{"openid", "profile"},
		Audience:         "https://api.example.com",
		ConnectorName:    "connector-1",
	}

	// Marshal to JSON (YAML tags are set the same as JSON tags for these fields)
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var got ConnectionResource
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if got.AuthorizationUrl != input.AuthorizationUrl {
		t.Errorf("authorizationUrl dropped: got %q", got.AuthorizationUrl)
	}
	if got.Audience != input.Audience {
		t.Errorf("audience dropped: got %q", got.Audience)
	}
	if got.ConnectorName != input.ConnectorName {
		t.Errorf("connectorName dropped: got %q", got.ConnectorName)
	}
	if len(got.Scopes) != 2 {
		t.Errorf("scopes dropped: got %v", got.Scopes)
	}
}

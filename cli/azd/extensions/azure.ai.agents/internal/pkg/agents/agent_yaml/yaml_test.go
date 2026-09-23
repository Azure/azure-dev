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

// TestConnectionResourceNewFieldsYAMLRoundTrip tests YAML unmarshal for the 6 new fields,
// verifying that yaml struct tags are correct and fields are not silently dropped.

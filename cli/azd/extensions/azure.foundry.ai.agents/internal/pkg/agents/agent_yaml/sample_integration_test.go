// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSampleAgentFiles(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
	}{
		{
			name:     "DeclarativeNoTools Agent",
			filePath: "../../../../tests/samples/declarativeNoTools/agent.yaml",
		},
		{
			name:     "GitHub MCP Agent", 
			filePath: "../../../../tests/samples/githubMcpAgent/agent.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Read the YAML file
			fullPath := filepath.Join(".", tt.filePath)
			content, err := os.ReadFile(fullPath)
			if err != nil {
				t.Fatalf("Failed to read file %s: %v", fullPath, err)
			}

			// Parse and validate the YAML content
			agentManifest, err := LoadAndValidateAgentManifest(content)
			if err != nil {
				t.Fatalf("Failed to load and validate %s: %v", tt.filePath, err)
			}

			// Verify basic structure
			if agentManifest.Agent.Name == "" {
				t.Error("Agent name should not be empty")
			}

			if agentManifest.Agent.Kind == "" {
				t.Error("Agent kind should not be empty")
			}

			if agentManifest.Agent.Model.Id == "" {
				t.Error("Agent model ID should not be empty")
			}

			t.Logf("Successfully validated %s - Agent: %s, Kind: %s, Model: %s", 
				tt.filePath, 
				agentManifest.Agent.Name, 
				agentManifest.Agent.Kind, 
				agentManifest.Agent.Model.Id)
		})
	}
}

func TestSampleAgentSpecificValidation(t *testing.T) {
	t.Run("DeclarativeNoTools - Specific Fields", func(t *testing.T) {
		content, err := os.ReadFile("../../../../tests/samples/declarativeNoTools/agent.yaml")
		if err != nil {
			t.Fatalf("Failed to read declarativeNoTools agent.yaml: %v", err)
		}

		agentManifest, err := LoadAndValidateAgentManifest(content)
		if err != nil {
			t.Fatalf("Failed to validate declarativeNoTools: %v", err)
		}

		// Verify specific fields for this agent
		if agentManifest.Agent.Name != "Learn French Agent" {
			t.Errorf("Expected name 'Learn French Agent', got '%s'", agentManifest.Agent.Name)
		}

		if agentManifest.Agent.Kind != "prompt" {
			t.Errorf("Expected kind 'prompt', got '%s'", agentManifest.Agent.Kind)
		}

		if agentManifest.Agent.Model.Id != "gpt-4o-mini" {
			t.Errorf("Expected model ID 'gpt-4o-mini', got '%s'", agentManifest.Agent.Model.Id)
		}

		if agentManifest.Agent.Description == "" {
			t.Error("Description should not be empty")
		}

		if agentManifest.Agent.Instructions == "" {
			t.Error("Instructions should not be empty")
		}

		// Verify tools array is empty (this is a "no tools" agent)
		if len(agentManifest.Agent.Tools) != 0 {
			t.Errorf("Expected no tools, got %d tools", len(agentManifest.Agent.Tools))
		}
	})

	t.Run("GitHub MCP Agent - Specific Fields", func(t *testing.T) {
		content, err := os.ReadFile("../../../../tests/samples/githubMcpAgent/agent.yaml")
		if err != nil {
			t.Fatalf("Failed to read githubMcpAgent agent.yaml: %v", err)
		}

		agentManifest, err := LoadAndValidateAgentManifest(content)
		if err != nil {
			t.Fatalf("Failed to validate githubMcpAgent: %v", err)
		}

		// Verify specific fields for this agent
		if agentManifest.Agent.Name != "github-agent" {
			t.Errorf("Expected name 'github-agent', got '%s'", agentManifest.Agent.Name)
		}

		if agentManifest.Agent.Kind != "prompt" {
			t.Errorf("Expected kind 'prompt', got '%s'", agentManifest.Agent.Kind)
		}

		if agentManifest.Agent.Model.Id != "gpt-4o-mini" {
			t.Errorf("Expected model ID 'gpt-4o-mini', got '%s'", agentManifest.Agent.Model.Id)
		}

		// Verify this agent has tools
		if len(agentManifest.Agent.Tools) == 0 {
			t.Error("Expected at least one tool for GitHub MCP agent")
		}

		// Verify the tool is MCP kind
		if len(agentManifest.Agent.Tools) > 0 {
			tool := agentManifest.Agent.Tools[0]
			if tool.Kind != "mcp" {
				t.Errorf("Expected tool kind 'mcp', got '%s'", tool.Kind)
			}
		}
	})
}

func TestSampleFilesRoundTrip(t *testing.T) {
	// This test ensures we can load, validate, and the structure is complete
	sampleFiles := []string{
		"../../../../tests/samples/declarativeNoTools/agent.yaml",
		"../../../../tests/samples/githubMcpAgent/agent.yaml",
	}

	for _, filePath := range sampleFiles {
		t.Run(filePath, func(t *testing.T) {
			// Read original content
			originalContent, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("Failed to read %s: %v", filePath, err)
			}

			// Parse with our loader/validator
			agentManifest, err := LoadAndValidateAgentManifest(originalContent)
			if err != nil {
				t.Fatalf("Failed to load and validate %s: %v", filePath, err)
			}

			// Also test direct validation
			err = ValidateAgentManifest(agentManifest)
			if err != nil {
				t.Fatalf("Direct validation failed for %s: %v", filePath, err)
			}

			t.Logf("Round-trip test successful for %s", filePath)
		})
	}
}
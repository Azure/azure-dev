// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"testing"
)

// TestExtractAgentDefinition_WithTemplateField tests parsing YAML with a template field (manifest format)
func TestExtractAgentDefinition_WithTemplateField(t *testing.T) {
	yamlContent := []byte(`
name: test-manifest
template:
  kind: hosted
  name: test-agent
  description: Test agent with template field
  protocols:
    - protocol: responses
      version: v1
`)

	agent, err := ExtractAgentDefinition(yamlContent)
	if err != nil {
		t.Fatalf("ExtractAgentDefinition failed: %v", err)
	}

	containerAgent, ok := agent.(ContainerAgent)
	if !ok {
		t.Fatalf("Expected ContainerAgent, got %T", agent)
	}

	if containerAgent.Name != "test-agent" {
		t.Errorf("Expected name 'test-agent', got '%s'", containerAgent.Name)
	}

	if containerAgent.Kind != AgentKindHosted {
		t.Errorf("Expected kind 'hosted', got '%s'", containerAgent.Kind)
	}
}

// TestExtractAgentDefinition_WithAgentField tests parsing YAML with an agent field (alias for template)
func TestExtractAgentDefinition_WithAgentField(t *testing.T) {
	yamlContent := []byte(`
name: test-manifest
agent:
  kind: hosted
  name: test-agent-with-agent-field
  description: Test agent with agent field
  protocols:
    - protocol: responses
      version: v1
`)

	agent, err := ExtractAgentDefinition(yamlContent)
	if err != nil {
		t.Fatalf("ExtractAgentDefinition failed: %v", err)
	}

	containerAgent, ok := agent.(ContainerAgent)
	if !ok {
		t.Fatalf("Expected ContainerAgent, got %T", agent)
	}

	if containerAgent.Name != "test-agent-with-agent-field" {
		t.Errorf("Expected name 'test-agent-with-agent-field', got '%s'", containerAgent.Name)
	}

	if containerAgent.Kind != AgentKindHosted {
		t.Errorf("Expected kind 'hosted', got '%s'", containerAgent.Kind)
	}
}

// TestExtractAgentDefinition_WithoutTemplateField tests parsing YAML without a template field (standalone format)
func TestExtractAgentDefinition_WithoutTemplateField(t *testing.T) {
	yamlContent := []byte(`
kind: hosted
name: lego-social-media-agent
description: An AI-powered social media content generator for LEGO products.
metadata:
  authors:
    - LEGO Social Media Team
  tags:
    - Social Media
    - Content Generation
protocols:
  - protocol: responses
    version: v1
environment_variables:
  - name: POSTGRES_SERVER
    value: ${POSTGRES_SERVER}
  - name: POSTGRES_DATABASE
    value: ${POSTGRES_DATABASE}
`)

	agent, err := ExtractAgentDefinition(yamlContent)
	if err != nil {
		t.Fatalf("ExtractAgentDefinition failed: %v", err)
	}

	containerAgent, ok := agent.(ContainerAgent)
	if !ok {
		t.Fatalf("Expected ContainerAgent, got %T", agent)
	}

	if containerAgent.Name != "lego-social-media-agent" {
		t.Errorf("Expected name 'lego-social-media-agent', got '%s'", containerAgent.Name)
	}

	if containerAgent.Kind != AgentKindHosted {
		t.Errorf("Expected kind 'hosted', got '%s'", containerAgent.Kind)
	}

	if containerAgent.Description == nil || *containerAgent.Description == "" {
		t.Error("Expected description to be set")
	}

	if containerAgent.Metadata == nil {
		t.Error("Expected metadata to be set")
	}

	if len(containerAgent.Protocols) == 0 {
		t.Error("Expected protocols to be set")
	}

	if containerAgent.EnvironmentVariables == nil || len(*containerAgent.EnvironmentVariables) == 0 {
		t.Error("Expected environment_variables to be set")
	}
}

// TestExtractAgentDefinition_InvalidTemplate tests that an invalid template field returns an error
func TestExtractAgentDefinition_InvalidTemplate(t *testing.T) {
	yamlContent := []byte(`
name: test-manifest
template: "this is not a map"
`)

	_, err := ExtractAgentDefinition(yamlContent)
	if err == nil {
		t.Fatal("Expected error for invalid template field, got nil")
	}

	expectedMsg := "template field must be a map"
	if !contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain '%s', got '%s'", expectedMsg, err.Error())
	}
}

// TestExtractAgentDefinition_MissingKind tests that missing kind field returns an error
func TestExtractAgentDefinition_MissingKind(t *testing.T) {
	yamlContent := []byte(`
name: test-agent
description: Test agent without kind field
`)

	_, err := ExtractAgentDefinition(yamlContent)
	if err == nil {
		t.Fatal("Expected error for missing kind field, got nil")
	}

	expectedMsg := "unrecognized agent kind"
	if !contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain '%s', got '%s'", expectedMsg, err.Error())
	}
}

// TestLoadAndValidateAgentManifest_StandaloneFormat tests the full validation flow with standalone format
func TestLoadAndValidateAgentManifest_StandaloneFormat(t *testing.T) {
	yamlContent := []byte(`
kind: hosted
name: test-standalone-agent
description: A standalone agent definition
protocols:
  - protocol: responses
    version: v1
`)

	manifest, err := LoadAndValidateAgentManifest(yamlContent)
	if err != nil {
		t.Fatalf("LoadAndValidateAgentManifest failed: %v", err)
	}

	if manifest == nil {
		t.Fatal("Expected manifest to be non-nil")
	}

	containerAgent, ok := manifest.Template.(ContainerAgent)
	if !ok {
		t.Fatalf("Expected Template to be ContainerAgent, got %T", manifest.Template)
	}

	if containerAgent.Name != "test-standalone-agent" {
		t.Errorf("Expected name 'test-standalone-agent', got '%s'", containerAgent.Name)
	}
}

// TestExtractAgentDefinition_IssueExample tests the exact YAML from the GitHub issue
func TestExtractAgentDefinition_IssueExample(t *testing.T) {
	// This is the exact YAML from the GitHub issue that was causing the panic
	yamlContent := []byte(`# yaml-language-server: $schema=https://raw.githubusercontent.com/microsoft/AgentSchema/refs/heads/main/schemas/v1.0/ContainerAgent.yaml

kind: hosted
name: lego-social-media-agent
description: |
    An AI-powered social media content generator for LEGO products.
metadata:
    authors:
        - LEGO Social Media Team
    example:
        - content: Generate a Twitter post about Star Wars LEGO sets
          role: user
    tags:
        - Social Media
        - Content Generation
protocols:
    - protocol: responses
      version: v1
environment_variables:
    - name: POSTGRES_SERVER
      value: ${POSTGRES_SERVER}
    - name: POSTGRES_DATABASE
      value: ${POSTGRES_DATABASE}
`)

	agent, err := ExtractAgentDefinition(yamlContent)
	if err != nil {
		t.Fatalf("ExtractAgentDefinition failed with issue example: %v", err)
	}

	containerAgent, ok := agent.(ContainerAgent)
	if !ok {
		t.Fatalf("Expected ContainerAgent, got %T", agent)
	}

	if containerAgent.Name != "lego-social-media-agent" {
		t.Errorf("Expected name 'lego-social-media-agent', got '%s'", containerAgent.Name)
	}

	if containerAgent.Kind != AgentKindHosted {
		t.Errorf("Expected kind 'hosted', got '%s'", containerAgent.Kind)
	}

	// Test full validation flow
	manifest, err := LoadAndValidateAgentManifest(yamlContent)
	if err != nil {
		t.Fatalf("LoadAndValidateAgentManifest failed with issue example: %v", err)
	}

	if manifest.Name != "lego-social-media-agent" {
		t.Errorf("Expected manifest name 'lego-social-media-agent', got '%s'", manifest.Name)
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

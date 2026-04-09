// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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

// TestExtractAgentDefinition_WithResources tests that resources (cpu/memory) are parsed and round-tripped
func TestExtractAgentDefinition_WithResources(t *testing.T) {
	yamlContent := []byte(`
name: echo-agent
template:
  kind: hosted
  name: echo-agent
  description: A simple echo agent
  protocols:
    - protocol: invocations
      version: 1.0.0
  resources:
    cpu: "0.25"
    memory: 0.5Gi
`)

	agent, err := ExtractAgentDefinition(yamlContent)
	if err != nil {
		t.Fatalf("ExtractAgentDefinition failed: %v", err)
	}

	containerAgent, ok := agent.(ContainerAgent)
	if !ok {
		t.Fatalf("Expected ContainerAgent, got %T", agent)
	}

	if containerAgent.Resources == nil {
		t.Fatal("Expected Resources to be set, got nil")
	}
	if containerAgent.Resources.Cpu != "0.25" {
		t.Errorf("Expected cpu '0.25', got '%s'", containerAgent.Resources.Cpu)
	}
	if containerAgent.Resources.Memory != "0.5Gi" {
		t.Errorf("Expected memory '0.5Gi', got '%s'", containerAgent.Resources.Memory)
	}

	// Verify YAML round-trip: marshal the agent and check resources are preserved
	marshaled, err := yaml.Marshal(containerAgent)
	if err != nil {
		t.Fatalf("Failed to marshal ContainerAgent: %v", err)
	}

	marshaledStr := string(marshaled)
	if !strings.Contains(marshaledStr, "cpu:") || !strings.Contains(marshaledStr, "memory:") {
		t.Errorf("Marshaled YAML should contain cpu and memory under resources, got:\n%s", marshaledStr)
	}

	// Unmarshal back and verify
	var roundTripped ContainerAgent
	if err := yaml.Unmarshal(marshaled, &roundTripped); err != nil {
		t.Fatalf("Failed to unmarshal ContainerAgent: %v", err)
	}

	if roundTripped.Resources == nil {
		t.Fatal("Expected Resources after round-trip, got nil")
	}
	if roundTripped.Resources.Cpu != "0.25" {
		t.Errorf("Round-trip cpu: expected '0.25', got '%s'", roundTripped.Resources.Cpu)
	}
	if roundTripped.Resources.Memory != "0.5Gi" {
		t.Errorf("Round-trip memory: expected '0.5Gi', got '%s'", roundTripped.Resources.Memory)
	}
}

// TestExtractAgentDefinition_WithoutResources tests that ContainerAgent without resources still parses
func TestExtractAgentDefinition_WithoutResources(t *testing.T) {
	yamlContent := []byte(`
name: test-manifest
template:
  kind: hosted
  name: test-agent
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

	if containerAgent.Resources != nil {
		t.Errorf("Expected Resources to be nil when not specified, got %+v", containerAgent.Resources)
	}

	// Verify marshaling omits resources when nil
	marshaled, err := yaml.Marshal(containerAgent)
	if err != nil {
		t.Fatalf("Failed to marshal ContainerAgent: %v", err)
	}

	if strings.Contains(string(marshaled), "resources:") {
		t.Errorf("Marshaled YAML should not contain resources when nil, got:\n%s", string(marshaled))
	}
}

// TestExtractAgentDefinition_EmptyTemplateField tests that an empty or null template field returns an error
func TestExtractAgentDefinition_EmptyTemplateField(t *testing.T) {
	testCases := []struct {
		name string
		yaml string
	}{
		{
			name: "null template field",
			yaml: `
name: test-manifest
template: null
`,
		},
		{
			name: "empty template field",
			yaml: `
name: test-manifest
template: {}
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExtractAgentDefinition([]byte(tc.yaml))
			if err == nil {
				t.Fatal("Expected error for empty/null template field, got nil")
			}

			// The error should indicate the template field issue
			expectedMsg := "YAML content does not conform to AgentManifest format"
			if !strings.Contains(err.Error(), expectedMsg) {
				t.Errorf("Expected error message to contain '%s', got '%s'", expectedMsg, err.Error())
			}
		})
	}
}

// TestExtractAgentDefinition_WithoutTemplateField tests that YAML without template field returns an error
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

	_, err := ExtractAgentDefinition(yamlContent)
	if err == nil {
		t.Fatal("Expected error for YAML without template field, got nil")
	}

	expectedMsg := "must contain 'template' field"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain '%s', got '%s'", expectedMsg, err.Error())
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
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain '%s', got '%s'", expectedMsg, err.Error())
	}
}

// TestExtractAgentDefinition_MissingTemplateField tests that YAML without template field returns an error
func TestExtractAgentDefinition_MissingTemplateField(t *testing.T) {
	yamlContent := []byte(`
name: test-agent
description: Test agent without template field
`)

	_, err := ExtractAgentDefinition(yamlContent)
	if err == nil {
		t.Fatal("Expected error for YAML without template field, got nil")
	}

	expectedMsg := "must contain 'template' field"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain '%s', got '%s'", expectedMsg, err.Error())
	}
}

// TestLoadAndValidateAgentManifest_WithoutTemplateField tests that YAML without template field returns an error
func TestLoadAndValidateAgentManifest_WithoutTemplateField(t *testing.T) {
	yamlContent := []byte(`
kind: hosted
name: test-standalone-agent
description: A standalone agent definition
protocols:
  - protocol: responses
    version: v1
`)

	_, err := LoadAndValidateAgentManifest(yamlContent)
	if err == nil {
		t.Fatal("Expected error for YAML without template field, got nil")
	}

	expectedMsg := "must contain 'template' field"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain '%s', got '%s'", expectedMsg, err.Error())
	}
}

// TestExtractAgentDefinition_IssueExample tests the exact YAML from the GitHub issue
func TestExtractAgentDefinition_IssueExample(t *testing.T) {
	// This is the exact YAML from the GitHub issue that was causing the panic
	// It should now return a proper error message instead of panicking
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

	_, err := ExtractAgentDefinition(yamlContent)
	if err == nil {
		t.Fatal("Expected error for YAML without template field, got nil")
	}

	expectedMsg := "must contain 'template' field"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain '%s', got '%s'", expectedMsg, err.Error())
	}

	// Test full validation flow - should also return error
	_, err = LoadAndValidateAgentManifest(yamlContent)
	if err == nil {
		t.Fatal("Expected error from LoadAndValidateAgentManifest for YAML without template field, got nil")
	}

	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain '%s', got '%s'", expectedMsg, err.Error())
	}
}

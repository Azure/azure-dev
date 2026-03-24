// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"strings"
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

// TestExtractResourceDefinitions_ToolboxResource tests parsing toolbox resources from manifest
func TestExtractResourceDefinitions_ToolboxResource(t *testing.T) {
	yamlContent := []byte(`
name: test-manifest
template:
  kind: prompt
  name: test-agent
  model:
    id: gpt-4.1-mini
resources:
  - kind: toolbox
    name: echo-toolbox
    id: echo-toolbox
    options:
      description: A sample toolbox
      tools:
        - type: mcp
          server_label: github
          server_url: https://api.example.com/mcp
          project_connection_id: TestKey
`)

	resources, err := ExtractResourceDefinitions(yamlContent)
	if err != nil {
		t.Fatalf("ExtractResourceDefinitions failed: %v", err)
	}

	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}

	toolboxRes, ok := resources[0].(ToolboxResource)
	if !ok {
		t.Fatalf("Expected ToolboxResource, got %T", resources[0])
	}

	if toolboxRes.Name != "echo-toolbox" {
		t.Errorf("Expected name 'echo-toolbox', got '%s'", toolboxRes.Name)
	}

	if toolboxRes.Kind != ResourceKindToolbox {
		t.Errorf("Expected kind 'toolbox', got '%s'", toolboxRes.Kind)
	}

	if toolboxRes.Id != "echo-toolbox" {
		t.Errorf("Expected id 'echo-toolbox', got '%s'", toolboxRes.Id)
	}

	desc, ok := toolboxRes.Options["description"]
	if !ok {
		t.Fatal("Expected 'description' in options")
	}
	if desc != "A sample toolbox" {
		t.Errorf("Expected description 'A sample toolbox', got '%v'", desc)
	}

	toolsVal, ok := toolboxRes.Options["tools"]
	if !ok {
		t.Fatal("Expected 'tools' in options")
	}
	toolsSlice, ok := toolsVal.([]any)
	if !ok {
		t.Fatalf("Expected tools to be a slice, got %T", toolsVal)
	}
	if len(toolsSlice) != 1 {
		t.Fatalf("Expected 1 tool in options, got %d", len(toolsSlice))
	}
}

// TestExtractResourceDefinitions_MixedResources tests parsing both model and toolbox resources
func TestExtractResourceDefinitions_MixedResources(t *testing.T) {
	yamlContent := []byte(`
name: test-manifest
template:
  kind: prompt
  name: test-agent
  model:
    id: gpt-4.1-mini
resources:
  - kind: model
    name: primary-model
    id: gpt-4.1-mini
  - kind: toolbox
    name: my-toolbox
    id: my-toolbox
    options:
      description: My toolbox
`)

	resources, err := ExtractResourceDefinitions(yamlContent)
	if err != nil {
		t.Fatalf("ExtractResourceDefinitions failed: %v", err)
	}

	if len(resources) != 2 {
		t.Fatalf("Expected 2 resources, got %d", len(resources))
	}

	if _, ok := resources[0].(ModelResource); !ok {
		t.Errorf("Expected first resource to be ModelResource, got %T", resources[0])
	}

	toolboxRes, ok := resources[1].(ToolboxResource)
	if !ok {
		t.Fatalf("Expected second resource to be ToolboxResource, got %T", resources[1])
	}

	if toolboxRes.Name != "my-toolbox" {
		t.Errorf("Expected name 'my-toolbox', got '%s'", toolboxRes.Name)
	}

	if toolboxRes.Id != "my-toolbox" {
		t.Errorf("Expected id 'my-toolbox', got '%s'", toolboxRes.Id)
	}
}

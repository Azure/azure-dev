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

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

// TestExtractResourceDefinitions_ToolboxResourceWithTypedTools tests parsing a toolbox
// resource that has typed tool definitions (ToolboxToolDefinition) in the Tools field,
// matching the AgentSchema ToolboxResource/ToolboxTool format.
func TestExtractResourceDefinitions_ToolboxResourceWithTypedTools(t *testing.T) {
	yamlContent := []byte(`
name: test-manifest
template:
  kind: prompt
  name: test-agent
  model:
    id: gpt-4.1-mini
resources:
  - kind: toolbox
    name: platform-tools
    description: Platform tools with typed definitions
    tools:
      - id: bing_grounding
      - id: mcp
        name: github-copilot
        target: https://api.githubcopilot.com/mcp
        authType: OAuth2
        options:
          clientId: my-client-id
          clientSecret: my-client-secret
      - id: mcp
        name: custom-api
        target: https://my-api.example.com/sse
        authType: CustomKeys
        options:
          key: my-api-key
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

	if toolboxRes.Name != "platform-tools" {
		t.Errorf("Expected name 'platform-tools', got '%s'", toolboxRes.Name)
	}

	if toolboxRes.Description != "Platform tools with typed definitions" {
		t.Errorf("Expected description, got '%s'", toolboxRes.Description)
	}

	if len(toolboxRes.Tools) != 3 {
		t.Fatalf("Expected 3 typed tools, got %d", len(toolboxRes.Tools))
	}

	// Check built-in tool (no target/authType/name)
	if toolboxRes.Tools[0].Id != "bing_grounding" {
		t.Errorf("Expected first tool id 'bing_grounding', got '%s'", toolboxRes.Tools[0].Id)
	}
	if toolboxRes.Tools[0].Target != "" {
		t.Errorf("Expected empty target for built-in tool, got '%s'", toolboxRes.Tools[0].Target)
	}

	// Check MCP tool with name and OAuth2
	if toolboxRes.Tools[1].Id != "mcp" {
		t.Errorf("Expected second tool id 'mcp', got '%s'", toolboxRes.Tools[1].Id)
	}
	if toolboxRes.Tools[1].Name != "github-copilot" {
		t.Errorf("Expected second tool name 'github-copilot', got '%s'", toolboxRes.Tools[1].Name)
	}
	if toolboxRes.Tools[1].Target != "https://api.githubcopilot.com/mcp" {
		t.Errorf("Expected second tool target, got '%s'", toolboxRes.Tools[1].Target)
	}
	if toolboxRes.Tools[1].AuthType != "OAuth2" {
		t.Errorf("Expected second tool authType 'OAuth2', got '%s'", toolboxRes.Tools[1].AuthType)
	}
	if toolboxRes.Tools[1].Options["clientId"] != "my-client-id" {
		t.Errorf("Expected second tool clientId, got '%v'", toolboxRes.Tools[1].Options["clientId"])
	}

	// Check MCP tool with CustomKeys
	if toolboxRes.Tools[2].Id != "mcp" {
		t.Errorf("Expected third tool id 'mcp', got '%s'", toolboxRes.Tools[2].Id)
	}
	if toolboxRes.Tools[2].Name != "custom-api" {
		t.Errorf("Expected third tool name 'custom-api', got '%s'", toolboxRes.Tools[2].Name)
	}
	if toolboxRes.Tools[2].AuthType != "CustomKeys" {
		t.Errorf("Expected third tool authType 'CustomKeys', got '%s'", toolboxRes.Tools[2].AuthType)
	}
}

// TestLoadAndValidateAgentManifest_RecordFormatParameters verifies that the
// record/map format for parameters (canonical agent manifest schema) is parsed
// correctly into PropertySchema.Properties.
func TestLoadAndValidateAgentManifest_RecordFormatParameters(t *testing.T) {
	yamlContent := []byte(`
name: test-params
template:
  name: test
  kind: hosted
  protocols:
    - protocol: responses
resources:
  - kind: model
    name: chat
    id: gpt-5
  - kind: toolbox
    name: tools
    tools:
      - id: mcp
        name: github
        target: https://api.githubcopilot.com/mcp
        authType: OAuth2
        options:
          clientId: "{{ github_client_id }}"
          clientSecret: "{{ github_client_secret }}"
parameters:
  github_client_id:
    schema:
      type: string
    description: OAuth client ID
    required: true
  github_client_secret:
    schema:
      type: string
    description: OAuth client secret
    required: true
  model_name:
    schema:
      type: string
      enum:
        - gpt-4o
        - gpt-4o-mini
      default: gpt-4o
    required: true
`)

	manifest, err := LoadAndValidateAgentManifest(yamlContent)
	if err != nil {
		t.Fatalf("LoadAndValidateAgentManifest failed: %v", err)
	}

	if len(manifest.Parameters.Properties) != 3 {
		t.Fatalf("Expected 3 parameters, got %d", len(manifest.Parameters.Properties))
	}

	// Find parameters by name (map order is not guaranteed)
	paramsByName := map[string]Property{}
	for _, p := range manifest.Parameters.Properties {
		paramsByName[p.Name] = p
	}

	// Check github_client_id
	p, ok := paramsByName["github_client_id"]
	if !ok {
		t.Fatal("Missing parameter github_client_id")
	}
	if p.Kind != "string" {
		t.Errorf("Expected kind 'string', got '%s'", p.Kind)
	}
	if p.Description == nil || *p.Description != "OAuth client ID" {
		t.Errorf("Unexpected description: %v", p.Description)
	}
	if p.Required == nil || !*p.Required {
		t.Error("Expected required=true")
	}

	// Check model_name with enum and default
	p, ok = paramsByName["model_name"]
	if !ok {
		t.Fatal("Missing parameter model_name")
	}
	if p.EnumValues == nil || len(*p.EnumValues) != 2 {
		t.Fatalf("Expected 2 enum values, got %v", p.EnumValues)
	}
	if p.Default == nil {
		t.Fatal("Expected default value")
	}
	if defaultStr, ok := (*p.Default).(string); !ok || defaultStr != "gpt-4o" {
		t.Errorf("Expected default 'gpt-4o', got %v", *p.Default)
	}
}

// TestLoadAndValidateAgentManifest_ArrayFormatParameters verifies that the
// traditional array format for parameters still works after the UnmarshalYAML change.
func TestLoadAndValidateAgentManifest_ArrayFormatParameters(t *testing.T) {
	yamlContent := []byte(`
name: test-array-params
template:
  name: test
  kind: hosted
  protocols:
    - protocol: responses
resources:
  - kind: model
    name: chat
    id: gpt-5
parameters:
  properties:
    - name: my_param
      kind: string
      description: A test parameter
      required: true
`)

	manifest, err := LoadAndValidateAgentManifest(yamlContent)
	if err != nil {
		t.Fatalf("LoadAndValidateAgentManifest failed: %v", err)
	}

	if len(manifest.Parameters.Properties) != 1 {
		t.Fatalf("Expected 1 parameter, got %d", len(manifest.Parameters.Properties))
	}

	p := manifest.Parameters.Properties[0]
	if p.Name != "my_param" {
		t.Errorf("Expected name 'my_param', got '%s'", p.Name)
	}
	if p.Kind != "string" {
		t.Errorf("Expected kind 'string', got '%s'", p.Kind)
	}
}

// TestLoadAndValidateAgentManifest_SecretParameter verifies that
// secret: true inside the schema block is parsed into Property.Secret.
func TestLoadAndValidateAgentManifest_SecretParameter(t *testing.T) {
	yamlContent := []byte(`
name: test-secret
template:
  name: test
  kind: hosted
  protocols:
    - protocol: responses
resources:
  - kind: model
    name: chat
    id: gpt-5
parameters:
  api_key:
    description: API key for the custom MCP server
    schema:
      type: string
      secret: true
    required: true
  display_name:
    description: A non-secret parameter
    schema:
      type: string
`)

	manifest, err := LoadAndValidateAgentManifest(yamlContent)
	if err != nil {
		t.Fatalf("LoadAndValidateAgentManifest failed: %v", err)
	}

	if len(manifest.Parameters.Properties) != 2 {
		t.Fatalf("Expected 2 parameters, got %d", len(manifest.Parameters.Properties))
	}

	paramsByName := map[string]Property{}
	for _, p := range manifest.Parameters.Properties {
		paramsByName[p.Name] = p
	}

	// api_key should be secret
	apiKey, ok := paramsByName["api_key"]
	if !ok {
		t.Fatal("Missing parameter api_key")
	}
	if apiKey.Secret == nil || !*apiKey.Secret {
		t.Error("Expected api_key to have secret=true")
	}

	// display_name should NOT be secret
	displayName, ok := paramsByName["display_name"]
	if !ok {
		t.Fatal("Missing parameter display_name")
	}
	if displayName.Secret != nil {
		t.Errorf("Expected display_name secret to be nil, got %v", *displayName.Secret)
	}
}

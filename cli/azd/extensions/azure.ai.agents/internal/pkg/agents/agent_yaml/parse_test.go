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

	if toolboxRes.Description != "A sample toolbox" {
		t.Errorf("Expected description 'A sample toolbox', got '%s'", toolboxRes.Description)
	}

	if len(toolboxRes.Tools) != 1 {
		t.Fatalf("Expected 1 tool, got %d", len(toolboxRes.Tools))
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
    description: My toolbox
    tools:
      - type: web_search
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

	if toolboxRes.Description != "My toolbox" {
		t.Errorf("Expected description 'My toolbox', got '%s'", toolboxRes.Description)
	}
}

// TestExtractResourceDefinitions_ConnectionResource tests parsing connection resources
func TestExtractResourceDefinitions_ConnectionResource(t *testing.T) {
	yamlContent := []byte(`
name: test-manifest
template:
  kind: prompt
  name: test-agent
  model:
    id: gpt-4.1-mini
resources:
  - kind: connection
    name: context7
    category: CustomKeys
    target: https://mcp.context7.com/mcp
    authType: CustomKeys
    credentials:
      key: my-api-key
    metadata:
      source: context7
`)

	resources, err := ExtractResourceDefinitions(yamlContent)
	if err != nil {
		t.Fatalf("ExtractResourceDefinitions failed: %v", err)
	}

	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}

	connRes, ok := resources[0].(ConnectionResource)
	if !ok {
		t.Fatalf("Expected ConnectionResource, got %T", resources[0])
	}

	if connRes.Name != "context7" {
		t.Errorf("Expected name 'context7', got '%s'", connRes.Name)
	}
	if connRes.Kind != ResourceKindConnection {
		t.Errorf("Expected kind 'connection', got '%s'", connRes.Kind)
	}
	if connRes.Category != CategoryCustomKeys {
		t.Errorf("Expected category 'CustomKeys', got '%s'", connRes.Category)
	}
	if connRes.Target != "https://mcp.context7.com/mcp" {
		t.Errorf("Expected target 'https://mcp.context7.com/mcp', got '%s'", connRes.Target)
	}
	if connRes.AuthType != AuthTypeCustomKeys {
		t.Errorf("Expected authType 'CustomKeys', got '%s'", connRes.AuthType)
	}
	if connRes.Credentials["key"] != "my-api-key" {
		t.Errorf("Expected credentials.key 'my-api-key', got '%v'", connRes.Credentials["key"])
	}
	if connRes.Metadata["source"] != "context7" {
		t.Errorf("Expected metadata.source 'context7', got '%s'", connRes.Metadata["source"])
	}
}

// TestExtractResourceDefinitions_AllResourceKinds tests model + toolbox + connection together
func TestExtractResourceDefinitions_AllResourceKinds(t *testing.T) {
	yamlContent := []byte(`
name: test-manifest
template:
  kind: prompt
  name: test-agent
  model:
    id: gpt-4.1-mini
resources:
  - kind: model
    name: chat
    id: gpt-4.1-mini
  - kind: connection
    name: context7
    category: CustomKeys
    target: https://mcp.context7.com/mcp
    authType: CustomKeys
    credentials:
      key: test-key
  - kind: toolbox
    name: agent-tools
    description: MCP tools for documentation search
    tools:
      - id: web_search
      - id: mcp
        project_connection_id: context7
`)

	resources, err := ExtractResourceDefinitions(yamlContent)
	if err != nil {
		t.Fatalf("ExtractResourceDefinitions failed: %v", err)
	}

	if len(resources) != 3 {
		t.Fatalf("Expected 3 resources, got %d", len(resources))
	}

	if _, ok := resources[0].(ModelResource); !ok {
		t.Errorf("Expected first resource to be ModelResource, got %T", resources[0])
	}
	if _, ok := resources[1].(ConnectionResource); !ok {
		t.Errorf("Expected second resource to be ConnectionResource, got %T", resources[1])
	}
	if _, ok := resources[2].(ToolboxResource); !ok {
		t.Errorf("Expected third resource to be ToolboxResource, got %T", resources[2])
	}
}

// TestExtractResourceDefinitions_ConnectionAllAuthTypes tests all supported auth types
func TestExtractResourceDefinitions_ConnectionAllAuthTypes(t *testing.T) {
	authTypes := []AuthType{
		AuthTypeAAD,
		AuthTypeApiKey,
		AuthTypeCustomKeys,
		AuthTypeNone,
		AuthTypeOAuth2,
		AuthTypePAT,
	}

	for _, authType := range authTypes {
		t.Run(string(authType), func(t *testing.T) {
			yamlContent := []byte(`
name: test-manifest
template:
  kind: prompt
  name: test-agent
  model:
    id: gpt-4.1-mini
resources:
  - kind: connection
    name: test-conn
    category: CustomKeys
    target: https://example.com
    authType: ` + string(authType) + `
`)
			resources, err := ExtractResourceDefinitions(yamlContent)
			if err != nil {
				t.Fatalf("Failed for authType %s: %v", authType, err)
			}

			connRes := resources[0].(ConnectionResource)
			if connRes.AuthType != authType {
				t.Errorf("Expected authType '%s', got '%s'", authType, connRes.AuthType)
			}
		})
	}
}

// TestExtractResourceDefinitions_ConnectionOptionalFields tests optional fields are preserved
func TestExtractResourceDefinitions_ConnectionOptionalFields(t *testing.T) {
	yamlContent := []byte(`
name: test-manifest
template:
  kind: prompt
  name: test-agent
  model:
    id: gpt-4.1-mini
resources:
  - kind: connection
    name: full-conn
    category: AzureOpenAI
    target: https://myendpoint.openai.azure.com
    authType: AAD
    expiryTime: "2025-12-31T00:00:00Z"
    isSharedToAll: true
    sharedUserList:
      - user1@contoso.com
      - user2@contoso.com
    metadata:
      env: production
    useWorkspaceManagedIdentity: false
`)

	resources, err := ExtractResourceDefinitions(yamlContent)
	if err != nil {
		t.Fatalf("ExtractResourceDefinitions failed: %v", err)
	}

	connRes := resources[0].(ConnectionResource)

	if connRes.ExpiryTime != "2025-12-31T00:00:00Z" {
		t.Errorf("Expected expiryTime, got '%s'", connRes.ExpiryTime)
	}
	if connRes.IsSharedToAll == nil || *connRes.IsSharedToAll != true {
		t.Error("Expected isSharedToAll to be true")
	}
	if len(connRes.SharedUserList) != 2 {
		t.Errorf("Expected 2 shared users, got %d", len(connRes.SharedUserList))
	}
	if connRes.Metadata["env"] != "production" {
		t.Errorf("Expected metadata.env 'production', got '%s'", connRes.Metadata["env"])
	}
	if connRes.UseWorkspaceManagedIdentity == nil || *connRes.UseWorkspaceManagedIdentity != false {
		t.Error("Expected useWorkspaceManagedIdentity to be false")
	}
}

// TestExtractToolsDefinitions_AzureAiSearch tests parsing an azure_ai_search tool
func TestExtractToolsDefinitions_AzureAiSearch(t *testing.T) {
	template := map[string]any{
		"tools": []any{
			map[string]any{
				"name": "my-search",
				"kind": "azure_ai_search",
				"indexes": []any{
					map[string]any{
						"project_connection_id": "search-conn",
						"index_name":            "docs-index",
						"query_type":            "semantic",
						"top_k":                 10,
					},
				},
			},
		},
	}

	tools, err := ExtractToolsDefinitions(template)
	if err != nil {
		t.Fatalf("ExtractToolsDefinitions failed: %v", err)
	}

	if len(tools) != 1 {
		t.Fatalf("Expected 1 tool, got %d", len(tools))
	}

	searchTool, ok := tools[0].(AzureAISearchTool)
	if !ok {
		t.Fatalf("Expected AzureAISearchTool, got %T", tools[0])
	}
	if searchTool.Kind != ToolKindAzureAiSearch {
		t.Errorf("Expected kind 'azure_ai_search', got '%s'", searchTool.Kind)
	}
	if len(searchTool.Indexes) != 1 {
		t.Fatalf("Expected 1 index, got %d", len(searchTool.Indexes))
	}
	if searchTool.Indexes[0].IndexName != "docs-index" {
		t.Errorf("Expected index_name 'docs-index', got '%s'", searchTool.Indexes[0].IndexName)
	}
}

// TestExtractToolsDefinitions_A2APreview tests parsing an a2a_preview tool
func TestExtractToolsDefinitions_A2APreview(t *testing.T) {
	template := map[string]any{
		"tools": []any{
			map[string]any{
				"name":                "a2a-delegate",
				"kind":                "a2a_preview",
				"baseUrl":             "https://remote-agent.example.com",
				"agentCardPath":       "/.well-known/agent.json",
				"projectConnectionId": "remote-conn",
			},
		},
	}

	tools, err := ExtractToolsDefinitions(template)
	if err != nil {
		t.Fatalf("ExtractToolsDefinitions failed: %v", err)
	}

	if len(tools) != 1 {
		t.Fatalf("Expected 1 tool, got %d", len(tools))
	}

	a2aTool, ok := tools[0].(A2APreviewTool)
	if !ok {
		t.Fatalf("Expected A2APreviewTool, got %T", tools[0])
	}
	if a2aTool.Kind != ToolKindA2APreview {
		t.Errorf("Expected kind 'a2a_preview', got '%s'", a2aTool.Kind)
	}
	if a2aTool.BaseUrl != "https://remote-agent.example.com" {
		t.Errorf("Expected baseUrl, got '%s'", a2aTool.BaseUrl)
	}
	if a2aTool.ProjectConnectionId != "remote-conn" {
		t.Errorf("Expected projectConnectionId 'remote-conn', got '%s'", a2aTool.ProjectConnectionId)
	}
}

// TestExtractResourceDefinitions_ToolboxResourceWithTypedTools tests parsing a toolbox
// resource that has tool entries in the Tools []any field,
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
        credentials:
          clientId: my-client-id
          clientSecret: my-client-secret
      - id: mcp
        name: custom-api
        target: https://my-api.example.com/sse
        authType: CustomKeys
        credentials:
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

	// Helper to get tool as map
	tool := func(i int) map[string]any {
		m, ok := toolboxRes.Tools[i].(map[string]any)
		if !ok {
			t.Fatalf("Expected tool[%d] to be map[string]any, got %T", i, toolboxRes.Tools[i])
		}
		return m
	}

	// Check built-in tool (no target/authType/name)
	if tool(0)["id"] != "bing_grounding" {
		t.Errorf("Expected first tool id 'bing_grounding', got '%v'", tool(0)["id"])
	}
	if tool(0)["target"] != nil {
		t.Errorf("Expected no target for built-in tool, got '%v'", tool(0)["target"])
	}

	// Check MCP tool with name and OAuth2
	if tool(1)["id"] != "mcp" {
		t.Errorf("Expected second tool id 'mcp', got '%v'", tool(1)["id"])
	}
	if tool(1)["name"] != "github-copilot" {
		t.Errorf("Expected second tool name 'github-copilot', got '%v'", tool(1)["name"])
	}
	if tool(1)["target"] != "https://api.githubcopilot.com/mcp" {
		t.Errorf("Expected second tool target, got '%v'", tool(1)["target"])
	}
	if tool(1)["authType"] != "OAuth2" {
		t.Errorf("Expected second tool authType 'OAuth2', got '%v'", tool(1)["authType"])
	}
	creds1, _ := tool(1)["credentials"].(map[string]any)
	if creds1["clientId"] != "my-client-id" {
		t.Errorf("Expected second tool clientId, got '%v'", creds1["clientId"])
	}

	// Check MCP tool with CustomKeys
	if tool(2)["id"] != "mcp" {
		t.Errorf("Expected third tool id 'mcp', got '%v'", tool(2)["id"])
	}
	if tool(2)["name"] != "custom-api" {
		t.Errorf("Expected third tool name 'custom-api', got '%v'", tool(2)["name"])
	}
	if tool(2)["authType"] != "CustomKeys" {
		t.Errorf("Expected third tool authType 'CustomKeys', got '%v'", tool(2)["authType"])
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
        credentials:
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

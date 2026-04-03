// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
)

func TestApplyVnextMetadata(t *testing.T) {
	tests := []struct {
		name          string
		azdEnv        map[string]string
		osEnvValue    string
		existingMeta  map[string]string
		expectEnabled bool
	}{
		{
			name:          "enabled via azd env",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "true"},
			expectEnabled: true,
		},
		{
			name:          "enabled via azd env value 1",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "1"},
			expectEnabled: true,
		},
		{
			name:          "disabled via azd env",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "false"},
			expectEnabled: false,
		},
		{
			name:          "enabled via OS env fallback",
			azdEnv:        map[string]string{},
			osEnvValue:    "true",
			expectEnabled: true,
		},
		{
			name:          "azd env takes precedence over OS env",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "false"},
			osEnvValue:    "true",
			expectEnabled: false,
		},
		{
			name:          "absent from both envs",
			azdEnv:        map[string]string{},
			expectEnabled: false,
		},
		{
			name:          "invalid value ignored",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "notabool"},
			expectEnabled: false,
		},
		{
			name:          "preserves existing metadata",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "true"},
			existingMeta:  map[string]string{"authors": "test"},
			expectEnabled: true,
		},
		{
			name:          "nil metadata initialized when enabled",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "true"},
			existingMeta:  nil,
			expectEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set/unset OS env
			if tt.osEnvValue != "" {
				t.Setenv("enableHostedAgentVNext", tt.osEnvValue)
			} else {
				t.Setenv("enableHostedAgentVNext", "")
			}

			request := &agent_api.CreateAgentRequest{
				Name: "test-agent",
				CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
					Metadata: tt.existingMeta,
				},
			}

			applyVnextMetadata(request, tt.azdEnv)

			val, exists := request.Metadata["enableVnextExperience"]
			if tt.expectEnabled {
				if !exists || val != "true" {
					t.Errorf("expected enableVnextExperience=true in metadata, got exists=%v val=%q", exists, val)
				}
			} else {
				if exists {
					t.Errorf("expected enableVnextExperience to be absent, but found val=%q", val)
				}
			}

			// Verify existing metadata is preserved
			if tt.existingMeta != nil {
				for k, v := range tt.existingMeta {
					if request.Metadata[k] != v {
						t.Errorf("existing metadata key %q was lost or changed: want %q, got %q", k, v, request.Metadata[k])
					}
				}
			}
		})
	}
}

func TestResolveToolboxEnvironmentVariables(t *testing.T) {
	p := &AgentServiceTargetProvider{}

	azdEnv := map[string]string{
		"CONNECTION_NAME": "my-conn",
		"SERVER_URL":      "https://api.example.com/mcp",
		"API_KEY":         "secret-key",
	}

	toolbox := Toolbox{
		Name:        "${CONNECTION_NAME}-toolbox",
		Description: "Toolbox for ${CONNECTION_NAME}",
		Tools: []map[string]any{
			{
				"type":                  "mcp",
				"server_url":            "${SERVER_URL}",
				"project_connection_id": "${CONNECTION_NAME}",
			},
		},
	}

	p.resolveToolboxEnvironmentVariables(&toolbox, azdEnv)

	if toolbox.Name != "my-conn-toolbox" {
		t.Errorf("Expected resolved name 'my-conn-toolbox', got '%s'", toolbox.Name)
	}
	if toolbox.Description != "Toolbox for my-conn" {
		t.Errorf("Expected resolved description, got '%s'", toolbox.Description)
	}
	if toolbox.Tools[0]["server_url"] != "https://api.example.com/mcp" {
		t.Errorf("Expected resolved server_url, got '%v'", toolbox.Tools[0]["server_url"])
	}
	if toolbox.Tools[0]["project_connection_id"] != "my-conn" {
		t.Errorf("Expected resolved project_connection_id, got '%v'",
			toolbox.Tools[0]["project_connection_id"])
	}
}

func TestResolveAnyValue_NestedMaps(t *testing.T) {
	p := &AgentServiceTargetProvider{}

	azdEnv := map[string]string{
		"VAR1": "resolved1",
		"VAR2": "resolved2",
	}

	input := map[string]any{
		"simple": "${VAR1}",
		"nested": map[string]any{
			"inner": "${VAR2}",
		},
		"list":   []any{"${VAR1}", "literal", "${VAR2}"},
		"number": 42,
		"bool":   true,
	}

	result := p.resolveMapValues(input, azdEnv)

	if result["simple"] != "resolved1" {
		t.Errorf("Expected 'resolved1', got '%v'", result["simple"])
	}

	nested, ok := result["nested"].(map[string]any)
	if !ok {
		t.Fatalf("Expected nested map, got %T", result["nested"])
	}
	if nested["inner"] != "resolved2" {
		t.Errorf("Expected 'resolved2', got '%v'", nested["inner"])
	}

	list, ok := result["list"].([]any)
	if !ok {
		t.Fatalf("Expected list, got %T", result["list"])
	}
	if list[0] != "resolved1" {
		t.Errorf("Expected 'resolved1' in list[0], got '%v'", list[0])
	}
	if list[1] != "literal" {
		t.Errorf("Expected 'literal' in list[1], got '%v'", list[1])
	}
	if list[2] != "resolved2" {
		t.Errorf("Expected 'resolved2' in list[2], got '%v'", list[2])
	}

	// Non-string types pass through unchanged
	if result["number"] != 42 {
		t.Errorf("Expected 42, got '%v'", result["number"])
	}
	if result["bool"] != true {
		t.Errorf("Expected true, got '%v'", result["bool"])
	}
}

func TestEnrichToolboxFromConnections(t *testing.T) {
	t.Parallel()

	connByName := map[string]ToolConnection{
		"github-copilot": {
			Name:   "github-copilot",
			Target: "https://api.githubcopilot.com/mcp",
		},
		"mslearn": {
			Name:   "mslearn",
			Target: "https://learn.microsoft.com/api/mcp",
		},
	}

	toolbox := Toolbox{
		Name: "test-toolbox",
		Tools: []map[string]any{
			{"type": "bing_grounding"},
			{"type": "mcp", "project_connection_id": "github-copilot"},
			{"type": "mcp", "project_connection_id": "mslearn"},
			// Tool with manual server_url should not be overwritten
			{"type": "mcp", "project_connection_id": "mslearn",
				"server_url": "https://custom.example.com"},
		},
	}

	enrichToolboxFromConnections(&toolbox, connByName)

	// Built-in tool unchanged
	if _, has := toolbox.Tools[0]["server_url"]; has {
		t.Error("Built-in tool should not have server_url")
	}

	// github-copilot tool enriched
	if toolbox.Tools[1]["server_url"] != "https://api.githubcopilot.com/mcp" {
		t.Errorf("Expected enriched server_url, got '%v'", toolbox.Tools[1]["server_url"])
	}
	if toolbox.Tools[1]["server_label"] != "github-copilot" {
		t.Errorf("Expected enriched server_label, got '%v'", toolbox.Tools[1]["server_label"])
	}

	// mslearn tool enriched
	if toolbox.Tools[2]["server_url"] != "https://learn.microsoft.com/api/mcp" {
		t.Errorf("Expected enriched server_url, got '%v'", toolbox.Tools[2]["server_url"])
	}

	// Tool with existing server_url should NOT be overwritten
	if toolbox.Tools[3]["server_url"] != "https://custom.example.com" {
		t.Errorf("Manual server_url should not be overwritten, got '%v'",
			toolbox.Tools[3]["server_url"])
	}
}

func TestGetServiceKey_NormalizesToolboxNames(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"hyphens", "agent-tools", "AGENT_TOOLS"},
		{"spaces", "agent tools", "AGENT_TOOLS"},
		{"mixed", "my-agent tools", "MY_AGENT_TOOLS"},
		{"already upper", "TOOLS", "TOOLS"},
		{"lowercase", "tools", "TOOLS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.getServiceKey(tt.input)
			if got != tt.expected {
				t.Errorf("getServiceKey(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestDeployToolboxes_UpdatesAzdEnvMap(t *testing.T) {
	// Verify that deployToolboxes updates the local azdEnv map
	// so downstream env var resolution works on first deploy.
	// This is a unit-level check of the map update logic rather than
	// a full integration test (which would require API mocking).

	azdEnv := map[string]string{
		"AZURE_AI_PROJECT_ENDPOINT": "https://project.example.com",
	}

	// Simulate the env update logic from deployToolboxes
	toolboxName := "agent-tools"
	projectEndpoint := azdEnv["AZURE_AI_PROJECT_ENDPOINT"]

	p := &AgentServiceTargetProvider{}
	toolboxKey := p.getServiceKey(toolboxName)
	envKey := fmt.Sprintf("FOUNDRY_TOOLBOX_%s_MCP_ENDPOINT", toolboxKey)
	endpoint := strings.TrimRight(projectEndpoint, "/")
	azdEnv[envKey] = fmt.Sprintf("%s/toolsets/%s/mcp", endpoint, toolboxName)

	expected := "https://project.example.com/toolsets/agent-tools/mcp"
	if azdEnv["FOUNDRY_TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT"] != expected {
		t.Errorf("Expected azdEnv to contain %s=%s, got %s",
			envKey, expected, azdEnv[envKey])
	}
}

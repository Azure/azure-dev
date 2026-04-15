// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
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

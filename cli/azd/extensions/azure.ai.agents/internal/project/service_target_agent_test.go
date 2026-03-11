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

func TestIsVnextEnabled(t *testing.T) {
	tests := []struct {
		name       string
		azdEnv     map[string]string
		osEnvValue string
		expected   bool
	}{
		{
			name:     "enabled via azd env",
			azdEnv:   map[string]string{"enableHostedAgentVNext": "true"},
			expected: true,
		},
		{
			name:     "enabled via azd env value 1",
			azdEnv:   map[string]string{"enableHostedAgentVNext": "1"},
			expected: true,
		},
		{
			name:     "disabled via azd env",
			azdEnv:   map[string]string{"enableHostedAgentVNext": "false"},
			expected: false,
		},
		{
			name:       "enabled via OS env fallback",
			azdEnv:     map[string]string{},
			osEnvValue: "true",
			expected:   true,
		},
		{
			name:       "azd env takes precedence over OS env",
			azdEnv:     map[string]string{"enableHostedAgentVNext": "false"},
			osEnvValue: "true",
			expected:   false,
		},
		{
			name:     "absent from both envs",
			azdEnv:   map[string]string{},
			expected: false,
		},
		{
			name:     "invalid value returns false",
			azdEnv:   map[string]string{"enableHostedAgentVNext": "notabool"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.osEnvValue != "" {
				t.Setenv("enableHostedAgentVNext", tt.osEnvValue)
			} else {
				t.Setenv("enableHostedAgentVNext", "")
			}

			result := isVnextEnabled(tt.azdEnv)
			if result != tt.expected {
				t.Errorf("isVnextEnabled() = %v, want %v", result, tt.expected)
			}
		})
	}
}

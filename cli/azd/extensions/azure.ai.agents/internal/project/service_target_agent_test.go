// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
)

func TestApplyVnextMetadata(t *testing.T) {
	tests := []struct {
		name         string
		existingMeta map[string]string
	}{
		{
			name: "nil metadata initialized",
		},
		{
			name:         "preserves existing metadata",
			existingMeta: map[string]string{"authors": "test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &agent_api.CreateAgentRequest{
				Name: "test-agent",
				CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
					Metadata: tt.existingMeta,
				},
			}

			applyVnextMetadata(request, map[string]string{})

			val, exists := request.Metadata["enableVnextExperience"]
			if !exists || val != "true" {
				t.Errorf("expected enableVnextExperience=true in metadata, got exists=%v val=%q", exists, val)
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

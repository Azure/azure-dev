// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"strings"
	"testing"
)

func TestValidateAgentManifest(t *testing.T) {
	tests := []struct {
		name        string
		manifest    *AgentManifest
		expectValid bool
		errorSubstr string
	}{
		{
			name: "Valid manifest",
			manifest: &AgentManifest{
				Agent: AgentDefinition{
					Name: "Test Agent",
					Kind: "prompt",
					Model: Model{
						Id: "gpt-4",
					},
				},
			},
			expectValid: true,
		},
		{
			name: "Missing agent name",
			manifest: &AgentManifest{
				Agent: AgentDefinition{
					Kind: "prompt",
					Model: Model{
						Id: "gpt-4",
					},
				},
			},
			expectValid: false,
			errorSubstr: "agent.name is required",
		},
		{
			name: "Missing agent kind",
			manifest: &AgentManifest{
				Agent: AgentDefinition{
					Name: "Test Agent",
					Model: Model{
						Id: "gpt-4",
					},
				},
			},
			expectValid: false,
			errorSubstr: "agent.kind is required",
		},
		{
			name: "Missing model ID",
			manifest: &AgentManifest{
				Agent: AgentDefinition{
					Name: "Test Agent",
					Kind: "prompt",
					Model: Model{},
				},
			},
			expectValid: false,
			errorSubstr: "agent.model.id is required",
		},
		{
			name: "Invalid agent kind",
			manifest: &AgentManifest{
				Agent: AgentDefinition{
					Name: "Test Agent",
					Kind: "invalid_kind",
					Model: Model{
						Id: "gpt-4",
					},
				},
			},
			expectValid: false,
			errorSubstr: "agent.kind must be one of:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAgentManifest(tt.manifest)

			if tt.expectValid {
				if err != nil {
					t.Errorf("Expected no error for valid manifest, but got: %v", err)
				}
			} else {
				if err == nil {
					t.Error("Expected error for invalid manifest, but got none")
				} else if !strings.Contains(err.Error(), tt.errorSubstr) {
					t.Errorf("Expected error containing '%s', but got: %v", tt.errorSubstr, err)
				}
			}
		})
	}
}
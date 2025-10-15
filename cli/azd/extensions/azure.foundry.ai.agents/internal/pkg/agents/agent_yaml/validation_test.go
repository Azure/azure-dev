// Copyright (c) Microsoft. All rights reserved.

package agent_yaml

import (
	"strings"
	"testing"
)

func TestValidateAgentManifest(t *testing.T) {
	tests := []struct {
		name          string
		manifest      *AgentManifest
		expectValid   bool
		expectedErrors []string
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
				Models: []Model{
					{
						Id: "gpt-4",
					},
				},
			},
			expectValid:    true,
			expectedErrors: []string{},
		},
		{
			name: "Missing required fields",
			manifest: &AgentManifest{
				Agent: AgentDefinition{
					// Missing Name, Kind, and Model.Id
				},
				Models: []Model{}, // Empty models array
			},
			expectValid: false,
			expectedErrors: []string{
				"agent.name: agent name is required",
				"agent.kind: agent kind is required",
				"agent.model.id: model ID is required",
				"models: at least one model must be specified",
			},
		},
		{
			name: "Invalid kind and publisher",
			manifest: &AgentManifest{
				Agent: AgentDefinition{
					Name: "Test Agent",
					Kind: "invalid_kind",
					Model: Model{
						Id:        "gpt-4",
						Publisher: "invalid_publisher",
					},
				},
				Models: []Model{
					{
						Id:        "gpt-4",
						Publisher: "invalid_publisher",
					},
				},
			},
			expectValid: false,
			expectedErrors: []string{
				"agent.kind: agent kind must be one of",
				"agent.model.publisher: publisher should be one of",
				"models[0].publisher: publisher should be one of",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := ValidateAgentManifest(tt.manifest)

			if report.IsValid != tt.expectValid {
				t.Errorf("Expected IsValid=%v, got %v", tt.expectValid, report.IsValid)
			}

			if tt.expectValid && report.HasErrors() {
				t.Errorf("Expected no errors for valid manifest, but got: %s", report.GetErrorSummary())
			}

			if !tt.expectValid {
				summary := report.GetErrorSummary()
				for _, expectedError := range tt.expectedErrors {
					if !strings.Contains(summary, expectedError) {
						t.Errorf("Expected error containing '%s' not found in summary: %s", expectedError, summary)
					}
				}
			}
		})
	}
}

func TestValidationReportMethods(t *testing.T) {
	report := &ValidationReport{IsValid: true}
	
	// Test adding errors
	if report.HasErrors() {
		t.Error("New report should not have errors")
	}

	report.AddError("test.field", "test error message")
	
	if !report.HasErrors() {
		t.Error("Report should have errors after adding one")
	}

	if report.IsValid {
		t.Error("Report should not be valid after adding error")
	}

	summary := report.GetErrorSummary()
	if !strings.Contains(summary, "test.field") || !strings.Contains(summary, "test error message") {
		t.Errorf("Error summary should contain field and message: %s", summary)
	}
}
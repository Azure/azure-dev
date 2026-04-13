// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestFixtures_ValidYAML verifies that valid YAML fixtures parse successfully
// and produce the expected agent kind and name via ExtractAgentDefinition.
func TestFixtures_ValidYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		file         string
		wantKind     AgentKind
		wantName     string
		wantErrSubst string // if non-empty, expect this error instead of success
	}{
		{
			name:     "hosted agent",
			file:     filepath.Join("testdata", "hosted-agent.yaml"),
			wantKind: AgentKindHosted,
			wantName: "hosted-test-agent",
		},
		{
			// Prompt agents are not currently supported by ExtractAgentDefinition.
			// This test documents the current expected behavior.
			name:         "prompt agent minimal",
			file:         filepath.Join("testdata", "prompt-agent-minimal.yaml"),
			wantErrSubst: "prompt agents not currently supported",
		},
		{
			name:         "prompt agent full",
			file:         filepath.Join("testdata", "prompt-agent-full.yaml"),
			wantErrSubst: "prompt agents not currently supported",
		},
		{
			name:         "mcp tools agent",
			file:         filepath.Join("testdata", "mcp-tools-agent.yaml"),
			wantErrSubst: "prompt agents not currently supported",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", tc.file, err)
			}

			agent, err := ExtractAgentDefinition(data)

			if tc.wantErrSubst != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrSubst)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubst) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErrSubst, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("ExtractAgentDefinition failed: %v", err)
			}

			containerAgent, ok := agent.(ContainerAgent)
			if !ok {
				t.Fatalf("expected ContainerAgent, got %T", agent)
			}

			if containerAgent.Kind != tc.wantKind {
				t.Errorf("kind: got %q, want %q", containerAgent.Kind, tc.wantKind)
			}
			if containerAgent.Name != tc.wantName {
				t.Errorf("name: got %q, want %q", containerAgent.Name, tc.wantName)
			}
		})
	}
}

// TestFixtures_ValidatePromptAgents uses ValidateAgentDefinition to confirm
// that prompt agent fixtures have a structurally valid YAML schema, even though
// ExtractAgentDefinition does not yet support prompt agents.
func TestFixtures_ValidatePromptAgents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
	}{
		{name: "prompt agent minimal", file: filepath.Join("testdata", "prompt-agent-minimal.yaml")},
		{name: "prompt agent full", file: filepath.Join("testdata", "prompt-agent-full.yaml")},
		{name: "mcp tools agent", file: filepath.Join("testdata", "mcp-tools-agent.yaml")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", tc.file, err)
			}

			// Extract the template section to pass to ValidateAgentDefinition,
			// which operates on template bytes rather than the full manifest.
			templateBytes, err := extractTemplateBytes(data)
			if err != nil {
				t.Fatalf("failed to extract template bytes: %v", err)
			}

			if err := ValidateAgentDefinition(templateBytes); err != nil {
				t.Fatalf("ValidateAgentDefinition failed for valid fixture: %v", err)
			}
		})
	}
}

// TestFixtures_InvalidYAML verifies that invalid YAML fixtures return appropriate errors.
func TestFixtures_InvalidYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		file         string
		wantErrSubst string
	}{
		{
			name:         "missing kind field",
			file:         filepath.Join("testdata", "invalid-no-kind.yaml"),
			wantErrSubst: "template.kind must be one of",
		},
		{
			name:         "prompt agent missing model",
			file:         filepath.Join("testdata", "invalid-no-model.yaml"),
			wantErrSubst: "template.model.id is required",
		},
		{
			name:         "empty template",
			file:         filepath.Join("testdata", "invalid-empty-template.yaml"),
			wantErrSubst: "template field is empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", tc.file, err)
			}

			// For "empty template", ExtractAgentDefinition catches the error.
			// For schema validation errors, ValidateAgentDefinition is used on the template bytes.
			_, extractErr := ExtractAgentDefinition(data)

			if extractErr != nil && strings.Contains(extractErr.Error(), tc.wantErrSubst) {
				return // error caught at extraction level
			}

			// Try validation-level check for schema errors (no-kind, no-model).
			templateBytes, err := extractTemplateBytes(data)
			if err != nil {
				// If we can't even extract template bytes but got an extraction error, that's fine.
				if extractErr != nil {
					t.Logf("ExtractAgentDefinition error: %v", extractErr)
					return
				}
				t.Fatalf("failed to extract template bytes and no extraction error: %v", err)
			}

			validateErr := ValidateAgentDefinition(templateBytes)
			if validateErr == nil {
				t.Fatalf("expected validation error containing %q, got nil (extractErr=%v)",
					tc.wantErrSubst, extractErr)
			}
			if !strings.Contains(validateErr.Error(), tc.wantErrSubst) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErrSubst, validateErr.Error())
			}
		})
	}
}

// TestFixtures_SampleAgents is a regression test that ensures the sample agent
// YAML files in tests/samples/ continue to parse correctly.
func TestFixtures_SampleAgents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		file     string
		wantName string
	}{
		{
			name:     "declarativeNoTools sample",
			file:     filepath.Join("..", "..", "..", "..", "tests", "samples", "declarativeNoTools", "agent.yaml"),
			wantName: "Learn French Agent",
		},
		{
			name:     "githubMcpAgent sample",
			file:     filepath.Join("..", "..", "..", "..", "tests", "samples", "githubMcpAgent", "agent.yaml"),
			wantName: "github-agent",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("failed to read sample %s: %v", tc.file, err)
			}

			// Both samples are prompt agents, so ExtractAgentDefinition returns
			// "prompt agents not currently supported". Validate structure instead.
			_, extractErr := ExtractAgentDefinition(data)
			if extractErr == nil {
				t.Fatal("expected error for prompt agent sample, got nil")
			}
			if !strings.Contains(extractErr.Error(), "prompt agents not currently supported") {
				t.Fatalf("unexpected error: %v", extractErr)
			}

			// Validate that the YAML structure is well-formed by unmarshaling
			// the template section into the typed structs.
			templateBytes, err := extractTemplateBytes(data)
			if err != nil {
				t.Fatalf("failed to extract template bytes: %v", err)
			}

			var agentDef AgentDefinition
			if err := yaml.Unmarshal(templateBytes, &agentDef); err != nil {
				t.Fatalf("failed to unmarshal AgentDefinition: %v", err)
			}
			if agentDef.Name != tc.wantName {
				t.Errorf("name: got %q, want %q", agentDef.Name, tc.wantName)
			}
			if agentDef.Kind != AgentKindPrompt {
				t.Errorf("kind: got %q, want %q", agentDef.Kind, AgentKindPrompt)
			}

			// Also confirm the model is present for these prompt agents.
			var promptAgent PromptAgent
			if err := yaml.Unmarshal(templateBytes, &promptAgent); err != nil {
				t.Fatalf("failed to unmarshal PromptAgent: %v", err)
			}
			if promptAgent.Model.Id == "" {
				t.Error("expected non-empty model.id in sample agent")
			}
		})
	}
}

// extractTemplateBytes reads YAML content with a top-level "template" field
// and returns the marshaled bytes of just the template section.
func extractTemplateBytes(manifestYaml []byte) ([]byte, error) {
	var generic map[string]any
	if err := yaml.Unmarshal(manifestYaml, &generic); err != nil {
		return nil, err
	}

	templateVal, ok := generic["template"]
	if !ok || templateVal == nil {
		return nil, fmt.Errorf("manifest missing top-level 'template' field")
	}

	templateMap, ok := templateVal.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("'template' field is not a mapping")
	}

	return yaml.Marshal(templateMap)
}

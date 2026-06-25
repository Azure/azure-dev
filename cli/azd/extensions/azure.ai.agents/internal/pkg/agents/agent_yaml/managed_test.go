// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"go.yaml.in/yaml/v3"
)

// TestExtractAgentDefinition_Managed_TemplateWrapper verifies the manifest
// parser routes a "managed" kind to a ManagedAgent value with all declared
// fields preserved.
func TestExtractAgentDefinition_Managed_TemplateWrapper(t *testing.T) {
	yamlContent := []byte(`
name: my-managed-manifest
template:
  kind: managed
  name: my-managed
  model: gpt-4.1-mini
  instructions: You are a careful assistant.
  skills:
    - websearch
    - code_interpreter
`)
	agent, err := ExtractAgentDefinition(yamlContent)
	if err != nil {
		t.Fatalf("ExtractAgentDefinition failed: %v", err)
	}
	managed, ok := agent.(ManagedAgent)
	if !ok {
		t.Fatalf("expected ManagedAgent from template wrapper, got %T", agent)
	}
	if managed.Name != "my-managed" {
		t.Errorf("name: got %q, want %q", managed.Name, "my-managed")
	}
	if managed.Kind != AgentKindManaged {
		t.Errorf("kind: got %q, want %q", managed.Kind, AgentKindManaged)
	}
	if managed.Model != "gpt-4.1-mini" {
		t.Errorf("model: got %q, want %q", managed.Model, "gpt-4.1-mini")
	}
	if managed.Instructions != "You are a careful assistant." {
		t.Errorf("instructions: got %q", managed.Instructions)
	}
	if len(managed.Skills) != 2 {
		t.Fatalf("skills: got %d entries, want 2", len(managed.Skills))
	}
}

// TestManagedAgent_YAMLRoundTrip verifies a ManagedAgent value round-trips
// through yaml.Marshal / yaml.Unmarshal cleanly. This is the path used when
// writing agent.yaml from the init scaffolding and later reading it from disk
// as a bare AgentDefinition (without the manifest `template:` wrapper).
func TestManagedAgent_YAMLRoundTrip(t *testing.T) {
	original := ManagedAgent{
		AgentDefinition: AgentDefinition{
			Name: "my-managed",
			Kind: AgentKindManaged,
		},
		Model:        "gpt-4.1-mini",
		Instructions: "Be helpful.",
	}
	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "kind: managed") {
		t.Fatalf("marshaled YAML missing kind discriminator:\n%s", data)
	}

	var roundTripped ManagedAgent
	if err := yaml.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if roundTripped.Model != original.Model {
		t.Errorf("model: got %q, want %q", roundTripped.Model, original.Model)
	}
	if roundTripped.Instructions != original.Instructions {
		t.Errorf("instructions: got %q, want %q", roundTripped.Instructions, original.Instructions)
	}
	if roundTripped.Kind != original.Kind {
		t.Errorf("kind: got %q, want %q", roundTripped.Kind, original.Kind)
	}
}

// TestValidateAgentDefinition_Managed_RequiresModelAndInstructions ensures the
// validator surfaces actionable errors when required managed-agent fields are
// missing.
func TestValidateAgentDefinition_Managed_RequiresModelAndInstructions(t *testing.T) {
	cases := []struct {
		name        string
		yamlContent string
		wantSubstr  string
		shouldError bool
	}{
		{
			name: "missing model",
			yamlContent: `
name: n
kind: managed
instructions: ok
`,
			wantSubstr:  "model",
			shouldError: true,
		},
		{
			name: "missing instructions",
			yamlContent: `
name: n
kind: managed
model: gpt-4.1-mini
`,
			wantSubstr:  "instructions",
			shouldError: true,
		},
		{
			name: "valid",
			yamlContent: `
name: n
kind: managed
model: gpt-4.1-mini
instructions: Be helpful.
`,
			shouldError: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAgentDefinition([]byte(tc.yamlContent))
			if tc.shouldError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantSubstr)
				}
				if !strings.Contains(strings.ToLower(err.Error()), tc.wantSubstr) {
					t.Errorf("error message %q does not contain %q", err.Error(), tc.wantSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestCreateManagedAgentAPIRequest_SetsHarness verifies the managed create
// request carries the GitHub Copilot harness identifier in the definition.
func TestCreateManagedAgentAPIRequest_SetsHarness(t *testing.T) {
	managed := ManagedAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindManaged,
			Name: "my-agent",
		},
		Model:        "gpt-4.1-mini",
		Instructions: "Be helpful.",
	}

	req, err := CreateManagedAgentAPIRequest(managed, nil)
	if err != nil {
		t.Fatalf("CreateManagedAgentAPIRequest: %v", err)
	}

	def, ok := req.Definition.(agent_api.ManagedAgentDefinition)
	if !ok {
		t.Fatalf("definition: got %T, want agent_api.ManagedAgentDefinition", req.Definition)
	}
	if def.Harness != agent_api.ManagedAgentHarnessGitHubCopilot {
		t.Errorf("harness: got %q, want %q", def.Harness, agent_api.ManagedAgentHarnessGitHubCopilot)
	}

	// The serialized body must include "harness":"ghcp".
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if !strings.Contains(string(data), `"harness":"ghcp"`) {
		t.Errorf("serialized request missing harness field:\n%s", data)
	}
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

// writeAgentYAML writes an agent.yaml into a temp dir and returns a provider
// pointed at it.
func writeAgentYAML(t *testing.T, agentYAML string) *AgentServiceTargetProvider {
	t.Helper()
	dir := t.TempDir()
	agentPath := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(agentPath, []byte(agentYAML), 0o600); err != nil {
		t.Fatalf("write agent.yaml: %v", err)
	}
	return &AgentServiceTargetProvider{agentDefinitionPath: agentPath}
}

// TestLoadPromptDef_InlineInstructions verifies instructions are read from the
// inline `instructions:` key, which is the only source the schema supports.
func TestLoadPromptDef_InlineInstructions(t *testing.T) {
	p := writeAgentYAML(t, `
kind: prompt
name: inline-instr
model: gpt-4.1-mini
instructions: FROM INLINE
`)

	managed, err := p.loadPromptAgentDefinition()
	if err != nil {
		t.Fatalf("loadPromptAgentDefinition: %v", err)
	}
	if managed.Instructions != "FROM INLINE" {
		t.Errorf("instructions: got %q, want inline value", managed.Instructions)
	}
}

// TestLoadPromptDef_NoInstructions confirms a manifest without instructions
// loads with an empty field; graph validation is what reports the error.
func TestLoadPromptDef_NoInstructions(t *testing.T) {
	p := writeAgentYAML(t, `
kind: prompt
name: no-instr
model: gpt-4.1-mini
`)

	managed, err := p.loadPromptAgentDefinition()
	if err != nil {
		t.Fatalf("loadPromptAgentDefinition: %v", err)
	}
	if strings.TrimSpace(managed.Instructions) != "" {
		t.Errorf("instructions: got %q, want empty", managed.Instructions)
	}
}

// TestLoadPromptDef_RejectsContainerFields verifies container-only fields are
// rejected for a prompt (kind: prompt) agent.
func TestLoadPromptDef_RejectsContainerFields(t *testing.T) {
	cases := []string{"image", "protocols", "code_configuration", "agent_endpoint"}
	for _, field := range cases {
		t.Run(field, func(t *testing.T) {
			p := writeAgentYAML(t, `
kind: prompt
name: bad
model: gpt-4.1-mini
instructions: ok
`+field+`: something
`)

			_, err := p.loadPromptAgentDefinition()
			if err == nil {
				t.Fatalf("expected error for container-only field %q", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("error should name the field %q: %v", field, err)
			}
		})
	}
}

// TestResolvePromptAgentGraph_ValidatesModelAndInstructions verifies the graph
// validation pass surfaces missing model/instructions before any resolve, and
// succeeds for a complete definition.
func TestResolvePromptAgentGraph_ValidatesModelAndInstructions(t *testing.T) {
	p := &AgentServiceTargetProvider{}

	// Missing model → error.
	missingModel := &agent_yaml.PromptAgent{Instructions: "ok"}
	missingModel.Name = "x"
	if _, err := p.resolvePromptAgentGraph(t.Context(), missingModel, nil, nil, nil); err == nil {
		t.Error("expected error when model is empty")
	}

	// Missing instructions → error.
	missingInstr := &agent_yaml.PromptAgent{Model: "gpt-4.1-mini"}
	missingInstr.Name = "x"
	if _, err := p.resolvePromptAgentGraph(t.Context(), missingInstr, nil, nil, nil); err == nil {
		t.Error("expected error when instructions are empty")
	}

	// Complete → no error.
	complete := &agent_yaml.PromptAgent{Model: "gpt-4.1-mini", Instructions: "ok"}
	complete.Name = "x"
	if _, err := p.resolvePromptAgentGraph(t.Context(), complete, nil, nil, nil); err != nil {
		t.Errorf("unexpected error for complete definition: %v", err)
	}
}

// TestResolvePromptAgentGraph_HarnessFeatureGate verifies the deploy path
// enforces the harness capability gate: guardrails pass on both a harnessed and
// a plain agent, while knowledge is rejected only when a harness is named. It
// exercises the gate through the graph rather than calling
// ValidateHarnessFeatures directly, so an unwired validation pass would be
// caught. Memory is covered separately because it needs a live endpoint.
func TestResolvePromptAgentGraph_HarnessFeatureGate(t *testing.T) {
	p := &AgentServiceTargetProvider{}

	newAgent := func(harness string, tools []any) *agent_yaml.PromptAgent {
		agent := &agent_yaml.PromptAgent{
			Model:        "gpt-4.1-mini",
			Instructions: "ok",
			Harness:      agent_yaml.NewPromptHarness(harness),
			Policies: []agent_yaml.Policy{
				{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: "/subscriptions/sub/raiPolicies/strict"},
			},
			Tools: tools,
		}
		agent.Name = "x"
		return agent
	}

	for _, harness := range []string{agent_api.ManagedAgentHarnessGitHubCopilot, ""} {
		agent := newAgent(harness, nil)
		if _, err := p.resolvePromptAgentGraph(t.Context(), agent, nil, nil, nil); err != nil {
			t.Errorf("harness %q should accept guardrails: %v", harness, err)
		}
	}

	grounding := []any{map[string]any{"type": "azure_ai_search"}}

	if _, err := p.resolvePromptAgentGraph(t.Context(), newAgent("", grounding), nil, nil, nil); err != nil {
		t.Errorf("a plain prompt agent should accept knowledge: %v", err)
	}

	_, err := p.resolvePromptAgentGraph(
		t.Context(), newAgent(agent_api.ManagedAgentHarnessGitHubCopilot, grounding), nil, nil, nil)
	if err == nil {
		t.Fatal("a harnessed agent declaring knowledge should be rejected")
	}
	if !strings.Contains(err.Error(), "knowledge") {
		t.Errorf("error should name the rejected capability, got: %v", err)
	}
}

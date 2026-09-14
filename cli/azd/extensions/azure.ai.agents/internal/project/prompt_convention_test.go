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

func TestApplyPromptAgentServiceName(t *testing.T) {
	agent := &agent_yaml.PromptAgent{}

	if err := applyPromptAgentServiceName(agent, "service-agent"); err != nil {
		t.Fatalf("applyPromptAgentServiceName: %v", err)
	}
	if agent.Name != "service-agent" {
		t.Fatalf("name = %q, want service-agent", agent.Name)
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

func TestResolvePromptAgentGraph_ValidatesName(t *testing.T) {
	p := &AgentServiceTargetProvider{}
	agent := &agent_yaml.PromptAgent{Model: "gpt-4.1-mini", Instructions: "ok"}
	agent.Name = "invalid name"

	_, err := p.resolvePromptAgentGraph(t.Context(), agent, nil, nil, nil)

	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("expected invalid name error, got %v", err)
	}
}

// TestResolvePromptAgentGraph_DefersHarnessCapabilities verifies azd validates
// structure without duplicating the service's evolving harness support matrix.
func TestResolvePromptAgentGraph_DefersHarnessCapabilities(t *testing.T) {
	p := &AgentServiceTargetProvider{}
	validateGraph := func(agent *agent_yaml.PromptAgent) error {
		graph, err := newPromptGraph(p.servicePath, agent, nil, nil, nil)
		if err != nil {
			return err
		}
		for _, node := range graph.nodes {
			if err := node.Validate(); err != nil {
				return err
			}
		}
		return nil
	}

	newAgent := func(harness string, tools []any) *agent_yaml.PromptAgent {
		agent := &agent_yaml.PromptAgent{
			Model:        "gpt-4.1-mini",
			Instructions: "ok",
			Harness:      agent_yaml.NewPromptHarness(harness),
			Policies: []agent_yaml.Policy{
				{Type: agent_yaml.PolicyTypeRai, RaiPolicyName: raiPolicyID},
			},
			Tools: tools,
		}
		agent.Name = "x"
		return agent
	}

	for _, harness := range []string{agent_api.ManagedAgentHarnessGitHubCopilot, ""} {
		agent := newAgent(harness, nil)
		if err := validateGraph(agent); err != nil {
			t.Errorf("harness %q should accept guardrails: %v", harness, err)
		}
	}

	for _, toolType := range []string{
		"function", "azure_ai_search", "file_search", "fabric_iq_preview",
		"work_iq_preview", "bing_grounding", "some_future_tool",
	} {
		if err := validateGraph(newAgent(
			agent_api.ManagedAgentHarnessGitHubCopilot,
			[]any{map[string]any{"type": toolType}},
		)); err != nil {
			t.Errorf("harness tool %q should be left to service validation: %v", toolType, err)
		}
	}
}

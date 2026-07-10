// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
)

// writeAgentYAML writes an agent.yaml (and optional instructions.md) into a temp
// dir and returns a provider pointed at it.
func writeAgentYAML(t *testing.T, agentYAML string, instructionsMD *string) *AgentServiceTargetProvider {
	t.Helper()
	dir := t.TempDir()
	agentPath := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(agentPath, []byte(agentYAML), 0o600); err != nil {
		t.Fatalf("write agent.yaml: %v", err)
	}
	if instructionsMD != nil {
		if err := os.WriteFile(filepath.Join(dir, "instructions.md"), []byte(*instructionsMD), 0o600); err != nil {
			t.Fatalf("write instructions.md: %v", err)
		}
	}
	return &AgentServiceTargetProvider{agentDefinitionPath: agentPath}
}

// TestLoadPromptDef_InstructionsFileFallback verifies a sibling instructions.md
// supplies the agent's instructions when none are declared inline.
func TestLoadPromptDef_InstructionsFileFallback(t *testing.T) {
	md := "You are a careful assistant.\nAnswer concisely."
	p := writeAgentYAML(t, `
kind: managed
name: file-instr
model: gpt-4.1-mini
`, &md)

	managed, err := p.loadPromptAgentDefinition()
	if err != nil {
		t.Fatalf("loadPromptAgentDefinition: %v", err)
	}
	if managed.Instructions != md {
		t.Errorf("instructions: got %q, want %q", managed.Instructions, md)
	}
}

// TestLoadPromptDef_InlineWinsOverFile verifies inline instructions take
// precedence over a sibling instructions.md.
func TestLoadPromptDef_InlineWinsOverFile(t *testing.T) {
	md := "FROM FILE"
	p := writeAgentYAML(t, `
kind: managed
name: inline-wins
model: gpt-4.1-mini
instructions: FROM INLINE
`, &md)

	managed, err := p.loadPromptAgentDefinition()
	if err != nil {
		t.Fatalf("loadPromptAgentDefinition: %v", err)
	}
	if managed.Instructions != "FROM INLINE" {
		t.Errorf("instructions: got %q, want inline value", managed.Instructions)
	}
}

// TestLoadPromptDef_NoInstructionsAnywhere confirms neither inline nor file
// instructions leaves the field empty (graph validation reports the error).
func TestLoadPromptDef_NoInstructionsAnywhere(t *testing.T) {
	p := writeAgentYAML(t, `
kind: managed
name: no-instr
model: gpt-4.1-mini
`, nil)

	managed, err := p.loadPromptAgentDefinition()
	if err != nil {
		t.Fatalf("loadPromptAgentDefinition: %v", err)
	}
	if strings.TrimSpace(managed.Instructions) != "" {
		t.Errorf("instructions: got %q, want empty", managed.Instructions)
	}
}

// TestLoadPromptDef_RejectsContainerFields verifies container-only fields are
// rejected for a prompt (kind: managed) agent.
func TestLoadPromptDef_RejectsContainerFields(t *testing.T) {
	cases := []string{"image", "protocols", "code_configuration", "agent_endpoint"}
	for _, field := range cases {
		t.Run(field, func(t *testing.T) {
			p := writeAgentYAML(t, `
kind: managed
name: bad
model: gpt-4.1-mini
instructions: ok
`+field+`: something
`, nil)

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
	missingModel := &agent_yaml.ManagedAgent{Instructions: "ok"}
	missingModel.Name = "x"
	if err := p.resolvePromptAgentGraph(t.Context(), missingModel, nil, nil, nil); err == nil {
		t.Error("expected error when model is empty")
	}

	// Missing instructions → error.
	missingInstr := &agent_yaml.ManagedAgent{Model: "gpt-4.1-mini"}
	missingInstr.Name = "x"
	if err := p.resolvePromptAgentGraph(t.Context(), missingInstr, nil, nil, nil); err == nil {
		t.Error("expected error when instructions are empty")
	}

	// Complete → no error.
	complete := &agent_yaml.ManagedAgent{Model: "gpt-4.1-mini", Instructions: "ok"}
	complete.Name = "x"
	if err := p.resolvePromptAgentGraph(t.Context(), complete, nil, nil, nil); err != nil {
		t.Errorf("unexpected error for complete definition: %v", err)
	}
}

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

// TestExtractAgentDefinition_Prompt_TemplateWrapper verifies the manifest
// parser routes a "prompt" kind to a PromptAgent value with all declared
// fields preserved.
func TestExtractAgentDefinition_Prompt_TemplateWrapper(t *testing.T) {
	yamlContent := []byte(`
name: my-prompt-manifest
template:
  kind: prompt
  name: my-prompt
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
	promptDef, ok := agent.(PromptAgent)
	if !ok {
		t.Fatalf("expected PromptAgent from template wrapper, got %T", agent)
	}
	if promptDef.Name != "my-prompt" {
		t.Errorf("name: got %q, want %q", promptDef.Name, "my-prompt")
	}
	if promptDef.Kind != AgentKindPrompt {
		t.Errorf("kind: got %q, want %q", promptDef.Kind, AgentKindPrompt)
	}
	if promptDef.Model != "gpt-4.1-mini" {
		t.Errorf("model: got %q, want %q", promptDef.Model, "gpt-4.1-mini")
	}
	if promptDef.Instructions != "You are a careful assistant." {
		t.Errorf("instructions: got %q", promptDef.Instructions)
	}
	if len(promptDef.Skills) != 2 {
		t.Fatalf("skills: got %d entries, want 2", len(promptDef.Skills))
	}
}

// TestPromptAgent_YAMLRoundTrip verifies a PromptAgent value round-trips
// through yaml.Marshal / yaml.Unmarshal cleanly. This is the path used when
// writing agent.yaml from the init scaffolding and later reading it from disk
// as a bare AgentDefinition (without the manifest `template:` wrapper).
func TestPromptAgent_YAMLRoundTrip(t *testing.T) {
	original := PromptAgent{
		AgentDefinition: AgentDefinition{
			Name: "my-prompt",
			Kind: AgentKindPrompt,
		},
		Model:        "gpt-4.1-mini",
		Instructions: "Be helpful.",
	}
	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "kind: prompt") {
		t.Fatalf("marshaled YAML missing kind discriminator:\n%s", data)
	}

	var roundTripped PromptAgent
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

// TestValidateAgentDefinition_Prompt_RequiresModelAndInstructions ensures the
// validator requires a model for prompt agents. Instructions are intentionally
// not required inline (they may come from a sibling instructions.md), so an
// agent.yaml without inline instructions must still validate here.
func TestValidateAgentDefinition_Prompt_RequiresModelAndInstructions(t *testing.T) {
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
kind: prompt
instructions: ok
`,
			wantSubstr:  "model",
			shouldError: true,
		},
		{
			name: "missing inline instructions is allowed (may come from instructions.md)",
			yamlContent: `
name: n
kind: prompt
model: gpt-4.1-mini
`,
			shouldError: false,
		},
		{
			name: "valid",
			yamlContent: `
name: n
kind: prompt
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

// TestCreatePromptAgentAPIRequest_SetsHarness verifies the prompt create
// request carries the GitHub Copilot harness identifier in the definition.
func TestCreatePromptAgentAPIRequest_SetsHarness(t *testing.T) {
	promptDef := PromptAgent{
		AgentDefinition: AgentDefinition{
			Kind: AgentKindPrompt,
			Name: "my-agent",
		},
		Model:        "gpt-4.1-mini",
		Instructions: "Be helpful.",
	}

	req, err := CreatePromptAgentAPIRequest(promptDef, nil)
	if err != nil {
		t.Fatalf("CreatePromptAgentAPIRequest: %v", err)
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

// TestCreatePromptAgentAPIRequest_ToolsPassthrough verifies that tools,
// tool_choice, and structured_inputs authored in agent.yaml flow through
// verbatim into the create request definition and are serialized with the
// API's snake_case shape.
func TestCreatePromptAgentAPIRequest_ToolsPassthrough(t *testing.T) {
	yamlContent := []byte(`
kind: prompt
name: kitchen-sink-agent
model: gpt-4o
instructions: You are a maximally capable assistant.
tool_choice: auto
structured_inputs:
  user_context:
    description: Extra context supplied per invocation
    required: false
tools:
  - type: function
    name: calculate_sum
    description: Adds two numbers
    parameters:
      type: object
      properties:
        a: { type: number }
        b: { type: number }
      required: [a, b]
    strict: true
  - type: code_interpreter
    container: auto
  - type: file_search
    vector_store_ids: [vs_12345]
    max_num_results: 10
  - type: mcp
    server_label: github-mcp
    server_url: https://api.githubcopilot.com/mcp
    require_approval: always
  - type: azure_ai_search
    azure_ai_search:
      index_name: my-index
  - type: bing_grounding
    bing_grounding:
      search_configurations:
        - project_connection_id: conn_bing_456
  - type: toolbox_search_preview
`)

	var promptDef PromptAgent
	if err := yaml.Unmarshal(yamlContent, &promptDef); err != nil {
		t.Fatalf("unmarshal prompt agent: %v", err)
	}
	if len(promptDef.Tools) != 7 {
		t.Fatalf("tools: got %d entries, want 7", len(promptDef.Tools))
	}

	req, err := CreatePromptAgentAPIRequest(promptDef, nil)
	if err != nil {
		t.Fatalf("CreatePromptAgentAPIRequest: %v", err)
	}

	def, ok := req.Definition.(agent_api.ManagedAgentDefinition)
	if !ok {
		t.Fatalf("definition: got %T, want agent_api.ManagedAgentDefinition", req.Definition)
	}
	if len(def.Tools) != 7 {
		t.Errorf("definition tools: got %d, want 7", len(def.Tools))
	}
	if def.ToolChoice != "auto" {
		t.Errorf("tool_choice: got %v, want auto", def.ToolChoice)
	}
	if _, ok := def.StructuredInputs["user_context"]; !ok {
		t.Errorf("structured_inputs missing user_context: %+v", def.StructuredInputs)
	}

	// The serialized body must carry the verbatim snake_case tool shapes.
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		`"tool_choice":"auto"`,
		`"structured_inputs"`,
		`"type":"function"`,
		`"type":"code_interpreter"`,
		`"type":"mcp"`,
		`"server_label":"github-mcp"`,
		`"type":"azure_ai_search"`,
		`"type":"bing_grounding"`,
		`"type":"toolbox_search_preview"`,
		`"vector_store_ids"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("serialized request missing %s:\n%s", want, body)
		}
	}
}

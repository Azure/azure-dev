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
		t.Errorf("model: got %q, want %q",
			roundTripped.Model, original.Model)
	}
	if roundTripped.Instructions != original.Instructions {
		t.Errorf("instructions: got %q, want %q", roundTripped.Instructions, original.Instructions)
	}
	if roundTripped.Kind != original.Kind {
		t.Errorf("kind: got %q, want %q", roundTripped.Kind, original.Kind)
	}
}

// TestValidateAgentDefinition_Prompt_RequiresModelAndInstructions ensures the
// validator requires both a model deployment and inline instructions for prompt
// agents — the two fields the prompt-agent API cannot default.
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
			name: "missing instructions",
			yamlContent: `
name: n
kind: prompt
model: gpt-4.1-mini
`,
			wantSubstr:  "instructions",
			shouldError: true,
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

func TestCreatePromptAgentAPIRequest_RequiresName(t *testing.T) {
	promptDef := PromptAgent{Model: "gpt-4.1-mini", Instructions: "Be helpful."}

	_, err := CreatePromptAgentAPIRequest(promptDef, nil)

	if err == nil || !strings.Contains(err.Error(), "invalid prompt agent name") {
		t.Fatalf("expected invalid prompt agent name error, got %v", err)
	}
}

// TestCreatePromptAgentAPIRequest_Harness verifies the prompt create request
// carries the agent's harness verbatim, and that a plain (harness-less) prompt
// agent omits the field entirely rather than defaulting to a harness.
func TestCreatePromptAgentAPIRequest_Harness(t *testing.T) {
	tests := []struct {
		name        string
		harness     string
		wantHarness string
		wantJSON    bool
	}{
		{
			name:        "managed agent keeps the GitHub Copilot harness",
			harness:     agent_api.ManagedAgentHarnessGitHubCopilot,
			wantHarness: agent_api.ManagedAgentHarnessGitHubCopilot,
			wantJSON:    true,
		},
		{
			name:        "plain prompt agent has no harness",
			harness:     "",
			wantHarness: "",
			wantJSON:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			promptDef := PromptAgent{
				AgentDefinition: AgentDefinition{
					Kind: AgentKindPrompt,
					Name: "my-agent",
				},
				Model:        "gpt-4.1-mini",
				Harness:      NewPromptHarness(tc.harness),
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
			gotHarness := ""
			if def.Harness != nil {
				gotHarness = def.Harness.Type
			}
			if gotHarness != tc.wantHarness {
				t.Errorf("harness: got %q, want %q", gotHarness, tc.wantHarness)
			}

			data, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			gotJSON := strings.Contains(string(data), `"harness":`)
			if gotJSON != tc.wantJSON {
				t.Errorf("serialized harness field present = %v, want %v:\n%s", gotJSON, tc.wantJSON, data)
			}
		})
	}
}

// TestCreatePromptAgentAPIRequest_HarnessSkills pins where skills land on the
// wire. A harnessed agent carries them inside the harness block as versioned
// references, because a skill is instructions plus scripts that only the
// harness sandbox can execute. Nothing about a skill becomes a tool, and no
// toolbox is involved: the harness already has a service-owned system toolbox
// whose name and lifecycle the customer does not manage.
func TestCreatePromptAgentAPIRequest_HarnessSkills(t *testing.T) {
	promptDef := PromptAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPrompt, Name: "my-agent"},
		Model:           "gpt-4.1-mini",
		Instructions:    "Be helpful.",
		Harness:         NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
		Skills:          []string{"duplicate-check"},
	}
	promptDef.Harness.Skills = []HarnessSkillRef{
		{Name: "duplicate-check", Version: "3"},
		{Name: "severity-triage", Version: "1"},
	}

	req, err := CreatePromptAgentAPIRequest(promptDef, nil)
	if err != nil {
		t.Fatalf("CreatePromptAgentAPIRequest: %v", err)
	}
	def, ok := req.Definition.(agent_api.ManagedAgentDefinition)
	if !ok {
		t.Fatalf("definition: got %T, want agent_api.ManagedAgentDefinition", req.Definition)
	}

	if def.Harness == nil {
		t.Fatal("expected a harness block")
	}
	want := []agent_api.HarnessSkillReference{
		{Name: "duplicate-check", Version: "3"},
		{Name: "severity-triage", Version: "1"},
	}
	if len(def.Harness.Skills) != len(want) {
		t.Fatalf("harness skills: got %+v, want %+v", def.Harness.Skills, want)
	}
	for i, w := range want {
		if def.Harness.Skills[i] != w {
			t.Errorf("harness skill %d: got %+v, want %+v", i, def.Harness.Skills[i], w)
		}
	}
	if len(def.Skills) != 0 {
		t.Errorf("harnessed skills must not appear on the definition-level field, got %+v", def.Skills)
	}
	if len(def.Tools) != 0 {
		t.Errorf("a skill must not become a tool, got %+v", def.Tools)
	}
}

// TestCreatePromptAgentAPIRequest_HarnessLessSkills covers the other half of
// the split: with no harness there is no sandbox to provision skills into, so
// the authored names stay on the definition-level field.
func TestCreatePromptAgentAPIRequest_HarnessLessSkills(t *testing.T) {
	promptDef := PromptAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPrompt, Name: "my-agent"},
		Model:           "gpt-4.1-mini",
		Instructions:    "Be helpful.",
		Skills:          []string{"severity-triage"},
	}

	req, err := CreatePromptAgentAPIRequest(promptDef, nil)
	if err != nil {
		t.Fatalf("CreatePromptAgentAPIRequest: %v", err)
	}
	def, ok := req.Definition.(agent_api.ManagedAgentDefinition)
	if !ok {
		t.Fatalf("definition: got %T, want agent_api.ManagedAgentDefinition", req.Definition)
	}

	if def.Harness != nil {
		t.Errorf("expected no harness block, got %+v", def.Harness)
	}
	if len(def.Skills) != 1 || def.Skills[0] != "severity-triage" {
		t.Errorf("definition skills: got %+v, want [severity-triage]", def.Skills)
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

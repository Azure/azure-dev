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

// TestPromptAgent_YAMLRoundTrip verifies a PromptAgent value round-trips
// through yaml.Marshal / yaml.Unmarshal cleanly. This is the path used when
// writing and reading direct prompt-agent definitions in azure.yaml.
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

func TestCreatePromptAgentAPIRequest_CopilotToolset(t *testing.T) {
	promptDef := PromptAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPrompt, Name: "my-agent"},
		Model:           "gpt-5-mini",
		Instructions:    "Use web research when requested.",
		Harness:         NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
		Tools: []any{map[string]any{
			"type":           githubCopilotToolsetPreview,
			"default_config": map[string]any{"enabled": false},
			"configs":        []any{map[string]any{"name": "web", "enabled": true}},
		}},
	}

	req, err := CreatePromptAgentAPIRequest(promptDef, nil)
	if err != nil {
		t.Fatalf("CreatePromptAgentAPIRequest: %v", err)
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		`"harness":{"type":"github_copilot_preview"}`,
		`"type":"github_copilot_toolset_preview"`,
		`"default_config":{"enabled":false}`,
		`"configs":[{"enabled":true,"name":"web"}]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("serialized request missing %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, `"builtin_tools"`) || strings.Contains(body, `"environment"`) {
		t.Errorf("harness contains removed configuration fields: %s", body)
	}
}

// TestCreatePromptAgentAPIRequest_HarnessSkills pins the REST contract: skills
// are versioned references at the prompt definition's top level.
func TestCreatePromptAgentAPIRequest_HarnessSkills(t *testing.T) {
	promptDef := PromptAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPrompt, Name: "my-agent"},
		Model:           "gpt-4.1-mini",
		Instructions:    "Be helpful.",
		Harness:         NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
		Skills:          []HarnessSkillRef{{Name: "duplicate-check"}},
		ResolvedSkills: []HarnessSkillRef{
			{Name: "duplicate-check", Version: "3"},
			{Name: "severity-triage", Version: "1"},
		},
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
	want := []agent_api.SkillReference{
		{Type: "skill_reference", Name: "duplicate-check", Version: "3"},
		{Type: "skill_reference", Name: "severity-triage", Version: "1"},
	}
	if len(def.Skills) != len(want) {
		t.Fatalf("definition skills: got %+v, want %+v", def.Skills, want)
	}
	for i, w := range want {
		if def.Skills[i] != w {
			t.Errorf("skill %d: got %+v, want %+v", i, def.Skills[i], w)
		}
	}
	if len(def.Tools) != 0 {
		t.Errorf("a skill must not become a tool, got %+v", def.Tools)
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	body := string(data)
	if !strings.Contains(body,
		`"skills":[{"type":"skill_reference","name":"duplicate-check","version":"3"},`+
			`{"type":"skill_reference","name":"severity-triage","version":"1"}]`) {
		t.Errorf("versioned top-level skills missing from request: %s", body)
	}
	if strings.Contains(body, `"harness":{"type":"github_copilot_preview","skills"`) {
		t.Errorf("skills must not be nested under harness: %s", body)
	}
}

func TestCreatePromptAgentAPIRequest_AuthoredVersionedSkill(t *testing.T) {
	promptDef := PromptAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPrompt, Name: "my-agent"},
		Model:           "gpt-4.1-mini",
		Instructions:    "Be helpful.",
		Harness:         NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
		Skills: []HarnessSkillRef{
			{Name: "microsoft-foundry", Version: "1"},
		},
		ResolvedSkills: []HarnessSkillRef{{Name: "MICROSOFT-FOUNDRY", Version: "3"}},
	}

	req, err := CreatePromptAgentAPIRequest(promptDef, nil)
	if err != nil {
		t.Fatalf("CreatePromptAgentAPIRequest: %v", err)
	}
	def, ok := req.Definition.(agent_api.ManagedAgentDefinition)
	if !ok {
		t.Fatalf("definition: got %T, want agent_api.ManagedAgentDefinition", req.Definition)
	}
	want := []agent_api.SkillReference{{Type: "skill_reference", Name: "microsoft-foundry", Version: "1"}}
	if len(def.Skills) != len(want) || def.Skills[0] != want[0] {
		t.Fatalf("definition skills: got %+v, want %+v", def.Skills, want)
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if !strings.Contains(string(data), `"skills":[{"type":"skill_reference","name":"microsoft-foundry","version":"1"}]`) {
		t.Errorf("authored skill reference missing discriminator or version: %s", data)
	}
}

func TestCreatePromptAgentAPIRequest_DuplicateAuthoredSkills(t *testing.T) {
	for _, tt := range []struct {
		name    string
		skills  []HarnessSkillRef
		wantErr bool
	}{
		{
			name:    "conflicting pins",
			skills:  []HarnessSkillRef{{Name: "foo", Version: "1"}, {Name: "foo", Version: "2"}},
			wantErr: true,
		},
		{
			name:    "normalized names conflict",
			skills:  []HarnessSkillRef{{Name: " Foo ", Version: "1"}, {Name: "foo", Version: "2"}},
			wantErr: true,
		},
		{
			name: "shorthand between conflicting pins",
			skills: []HarnessSkillRef{
				{Name: "foo", Version: "1"}, {Name: "foo"}, {Name: "foo", Version: "2"},
			},
			wantErr: true,
		},
		{
			name:   "identical pins",
			skills: []HarnessSkillRef{{Name: "foo", Version: "1"}, {Name: "foo", Version: " 1 "}},
		},
		{
			name:   "shorthand before pin",
			skills: []HarnessSkillRef{{Name: "foo"}, {Name: "foo", Version: "1"}},
		},
		{
			name:   "shorthand after pin",
			skills: []HarnessSkillRef{{Name: "foo", Version: "1"}, {Name: "foo"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request, err := CreatePromptAgentAPIRequest(PromptAgent{
				AgentDefinition: AgentDefinition{Kind: AgentKindPrompt, Name: "my-agent"},
				Model:           "gpt-4.1-mini",
				Instructions:    "Be helpful.",
				Skills:          tt.skills,
				ResolvedSkills:  []HarnessSkillRef{{Name: "foo", Version: "3"}},
			}, nil)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), `conflicting authored versions "1" and "2"`) {
					t.Fatalf("expected conflicting pin error, got %v", err)
				}
				if request != nil {
					t.Fatal("conflicting pins must not produce a request")
				}
				return
			}
			if err != nil {
				t.Fatalf("CreatePromptAgentAPIRequest: %v", err)
			}
			definition, ok := request.Definition.(agent_api.ManagedAgentDefinition)
			if !ok {
				t.Fatalf("unexpected definition type %T", request.Definition)
			}
			want := agent_api.SkillReference{Type: "skill_reference", Name: "foo", Version: "1"}
			if len(definition.Skills) != 1 || definition.Skills[0] != want {
				t.Fatalf("expected authored pin %+v, got %+v", want, definition.Skills)
			}
		})
	}
}

func TestPromptAgentRejectsMalformedSkillYAML(t *testing.T) {
	tests := []struct {
		name  string
		skill string
		want  string
	}{
		{"misspelled name", `{nam: foo, version: "1"}`, "field nam not found"},
		{"unknown field", `{name: foo, version: "1", extra: true}`, "field extra not found"},
		{"missing name", `{version: "1"}`, "requires a non-empty name"},
		{"empty name", `{name: "", version: "1"}`, "requires a non-empty name"},
		{"blank name", `{name: "   ", version: "1"}`, "requires a non-empty name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAgentDefinition([]byte("kind: prompt\nname: agent\n" +
				"model: test-model\ninstructions: Be helpful.\nskills:\n  - " + tt.skill + "\n"))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

// TestCreatePromptAgentAPIRequest_HarnessLessSkills rejects an authored skill
// that was not resolved to a published version.
func TestCreatePromptAgentAPIRequest_HarnessLessSkills(t *testing.T) {
	promptDef := PromptAgent{
		AgentDefinition: AgentDefinition{Kind: AgentKindPrompt, Name: "my-agent"},
		Model:           "gpt-4.1-mini",
		Instructions:    "Be helpful.",
		Skills:          []HarnessSkillRef{{Name: "severity-triage"}},
	}

	_, err := CreatePromptAgentAPIRequest(promptDef, nil)
	if err == nil {
		t.Fatalf("expected unresolved skill error, got %v", err)
	}
	for _, want := range []string{
		`prompt skill "severity-triage" requires a version`,
		`skills: [{name: "severity-triage", version: "<published-version>"}]`,
		"deploy the matching local skill dependency with 'azd deploy --all'",
		"azd does not automatically resolve the default version of an existing Foundry skill",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing guidance %q", err.Error(), want)
		}
	}
}

// TestCreatePromptAgentAPIRequest_ToolsPassthrough verifies that tools and the
// camelCase authored controls flow into the API's snake_case request shape.
func TestCreatePromptAgentAPIRequest_ToolsPassthrough(t *testing.T) {
	yamlContent := []byte(`
kind: prompt
name: kitchen-sink-agent
model: gpt-4o
instructions: You are a maximally capable assistant.
toolChoice: auto
structuredInputs:
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
	for _, unwanted := range []string{`"toolChoice"`, `"topP"`, `"structuredInputs"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("serialized API request contains authored key %s:\n%s", unwanted, body)
		}
	}
}

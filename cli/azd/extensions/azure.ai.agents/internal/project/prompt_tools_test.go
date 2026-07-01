// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/braydonk/yaml"
)

// TestPromptAgentToolsPassthrough_BraydonkDecoder verifies that the tools,
// tool_choice, and structured_inputs authored in agent.yaml survive the
// braydonk/yaml decoder used by the deploy path (deployPromptAgent /
// loadPromptAgentDefinition) and are serialized verbatim into the create
// request body sent to the managed-agent API.
//
// This guards against decoder differences: the create-request mapping is unit
// tested with go.yaml.in/yaml/v3, but deploy reads the manifest with
// braydonk/yaml, which must produce JSON-marshalable maps/slices.
func TestPromptAgentToolsPassthrough_BraydonkDecoder(t *testing.T) {
	yamlContent := []byte(`
kind: managed
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
  - type: mcp
    server_label: github-mcp
    server_url: https://api.githubcopilot.com/mcp
    require_approval: always
  - type: bing_grounding
    bing_grounding:
      search_configurations:
        - project_connection_id: conn_bing_456
  - type: toolbox_search_preview
`)

	// Decode with the SAME library the deploy path uses.
	var managed agent_yaml.ManagedAgent
	if err := yaml.Unmarshal(yamlContent, &managed); err != nil {
		t.Fatalf("braydonk unmarshal: %v", err)
	}
	if len(managed.Tools) != 4 {
		t.Fatalf("tools: got %d, want 4", len(managed.Tools))
	}

	req, err := agent_yaml.CreateManagedAgentAPIRequest(managed, nil)
	if err != nil {
		t.Fatalf("CreateManagedAgentAPIRequest: %v", err)
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		`"tool_choice":"auto"`,
		`"structured_inputs"`,
		`"type":"function"`,
		`"server_label":"github-mcp"`,
		`"type":"bing_grounding"`,
		`"type":"toolbox_search_preview"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("serialized request missing %s:\n%s", want, body)
		}
	}
}

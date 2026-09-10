// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/require"
)

func TestExpandPromptAgentTools(t *testing.T) {
	t.Parallel()
	agent := agent_yaml.PromptAgent{Tools: []any{
		map[string]any{
			"type": "azure_ai_search",
			"azure_ai_search": map[string]any{
				"indexes": []any{map[string]any{
					"index_name":            "${AZURE_SEARCH_INDEX_NAME}",
					"project_connection_id": "search-${ENVIRONMENT:-dev}",
				}},
			},
		},
		map[string]any{
			"type":       "mcp",
			"server_url": "https://${MCP_HOST}/mcp",
			"headers": map[string]any{
				"x-user-id": "${{user.id}}",
			},
		},
	}}

	require.NoError(t, expandPromptAgentTools(&agent, map[string]string{
		"AZURE_SEARCH_INDEX_NAME": "product-index",
		"MCP_HOST":                "tools.example.com",
	}))

	search := agent.Tools[0].(map[string]any)
	indexes := search["azure_ai_search"].(map[string]any)["indexes"].([]any)
	index := indexes[0].(map[string]any)
	require.Equal(t, "product-index", index["index_name"])
	require.Equal(t, "search-dev", index["project_connection_id"])
	mcp := agent.Tools[1].(map[string]any)
	require.Equal(t, "https://tools.example.com/mcp", mcp["server_url"])
	require.Equal(t, "${{user.id}}", mcp["headers"].(map[string]any)["x-user-id"])
}

func TestExpandPromptAgentToolsUnresolved(t *testing.T) {
	t.Parallel()
	agent := agent_yaml.PromptAgent{Tools: []any{map[string]any{
		"type": "azure_ai_search",
		"azure_ai_search": map[string]any{
			"indexes": []any{map[string]any{"index_name": "${AZURE_SEARCH_INDEX_NAME}"}},
		},
	}}}

	err := expandPromptAgentTools(&agent, map[string]string{})
	require.ErrorContains(t, err, "tools[0].azure_ai_search.indexes[0].index_name")
	require.ErrorContains(t, err, "unresolved environment variable ${AZURE_SEARCH_INDEX_NAME}")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Suggestion, "azd env set")
}

func TestExpandPromptAgentToolsLeavesNonStringValues(t *testing.T) {
	t.Parallel()
	agent := agent_yaml.PromptAgent{Tools: []any{map[string]any{
		"type":    "function",
		"strict":  true,
		"timeout": float64(30),
	}}}

	require.NoError(t, expandPromptAgentTools(&agent, nil))
	tool := agent.Tools[0].(map[string]any)
	require.Equal(t, true, tool["strict"])
	require.Equal(t, float64(30), tool["timeout"])
}

// TestPromptAgentToolsPassthrough_BraydonkDecoder verifies that the tools,
// toolChoice and structuredInputs authored in azure.yaml survive the
// braydonk/yaml decoder used by the deploy path (deployPromptAgent /
// loadPromptAgentDefinition) and are serialized verbatim into the create
// request body sent to the managed-agent API.
//
// This guards against decoder differences: the create-request mapping is unit
// tested with go.yaml.in/yaml/v3, but deploy reads the manifest with
// braydonk/yaml, which must produce JSON-marshalable maps/slices.
func TestPromptAgentToolsPassthrough_BraydonkDecoder(t *testing.T) {
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
	var promptDef agent_yaml.PromptAgent
	if err := yaml.Unmarshal(yamlContent, &promptDef); err != nil {
		t.Fatalf("braydonk unmarshal: %v", err)
	}
	if len(promptDef.Tools) != 4 {
		t.Fatalf("tools: got %d, want 4", len(promptDef.Tools))
	}

	req, err := agent_yaml.CreatePromptAgentAPIRequest(promptDef, nil)
	if err != nil {
		t.Fatalf("CreatePromptAgentAPIRequest: %v", err)
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

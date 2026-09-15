// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestPromptAgent_ValidateTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tools   []any
		wantErr string
	}{
		{
			name:  "no tools",
			tools: nil,
		},
		{
			name: "known types",
			tools: []any{
				map[string]any{"type": "file_search"},
				map[string]any{"type": "memory_search_preview", "memory_store_name": "m"},
			},
		},
		{
			name:  "copilot toolset requires harness",
			tools: []any{map[string]any{"type": githubCopilotToolsetPreview}},
			wantErr: `tools[0] uses "github_copilot_toolset_preview", which requires ` +
				`harness.type "github_copilot_preview"`,
		},
		{
			name: "copilot toolset rejects unknown tool",
			tools: []any{map[string]any{
				"type":    githubCopilotToolsetPreview,
				"configs": []any{map[string]any{"name": "browser"}},
			}},
			wantErr: `tools[0]: configs[0].name "browser" is not a GitHub Copilot built-in tool; ` +
				`supported tools are filesystem_read, filesystem_write, shell, subagents, web`,
		},
		{
			name: "copilot toolset rejects duplicate tool",
			tools: []any{map[string]any{
				"type": githubCopilotToolsetPreview,
				"configs": []any{
					map[string]any{"name": "web", "enabled": true},
					map[string]any{"name": "web", "enabled": false},
				},
			}},
			wantErr: `tools[0]: configs[1].name "web" is duplicated`,
		},
		{
			name: "copilot toolset rejects unknown field",
			tools: []any{map[string]any{
				"type":            githubCopilotToolsetPreview,
				"default_enabled": false,
			}},
			wantErr: `tools[0]: unknown field "default_enabled"`,
		},
		{
			// Unrecognized is not an error: the type may simply be newer than
			// this build of azd.
			name:  "unrecognized type is allowed through",
			tools: []any{map[string]any{"type": "brand_new_tool_preview"}},
		},
		{
			name:    "entry is not a mapping",
			tools:   []any{"file_search"},
			wantErr: "tools[0] must be a mapping with a 'type' key, got string",
		},
		{
			name:    "entry has no type",
			tools:   []any{map[string]any{"server_label": "toolbox"}},
			wantErr: "tools[0]: tool entry is missing a 'type' key",
		},
		{
			name:    "type is not a string",
			tools:   []any{map[string]any{"type": 42}},
			wantErr: "tools[0]: tool 'type' must be a string, got int",
		},
		{
			name:    "type is blank",
			tools:   []any{map[string]any{"type": "   "}},
			wantErr: "tools[0]: tool 'type' must not be empty",
		},
		{
			name:  "removed type names its replacement",
			tools: []any{map[string]any{"type": "memory_search"}},
			wantErr: `tools[0] uses tool type "memory_search", which the API no longer defines; ` +
				`use "memory_search_preview" instead`,
		},
		{
			name: "error names the offending index, not the first",
			tools: []any{
				map[string]any{"type": "file_search"},
				map[string]any{"type": "code_interpreter"},
				map[string]any{"no_type": true},
			},
			wantErr: "tools[2]: tool entry is missing a 'type' key",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			agent := &PromptAgent{Tools: test.tools}
			if strings.HasPrefix(test.name, "copilot toolset rejects") {
				agent.Harness = NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot)
			}
			err := agent.ValidateTools()

			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, test.wantErr)
		})
	}
}

func TestPromptAgent_ValidateCopilotToolset(t *testing.T) {
	t.Parallel()
	agent := &PromptAgent{
		Harness: NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot),
		Tools: []any{map[string]any{
			"type":           githubCopilotToolsetPreview,
			"default_config": map[string]any{"enabled": false},
			"configs":        []any{map[string]any{"name": "web", "enabled": true}},
		}},
	}
	require.NoError(t, agent.ValidateTools())
}

func TestPromptAgent_UnrecognizedToolTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		tools []any
		want  []string
	}{
		{
			name:  "all recognized",
			tools: []any{map[string]any{"type": "azure_ai_search"}, map[string]any{"type": "mcp"}},
			want:  nil,
		},
		{
			name:  "typo is reported",
			tools: []any{map[string]any{"type": "file_serach"}},
			want:  []string{"file_serach"},
		},
		{
			name: "sorted and deduplicated",
			tools: []any{
				map[string]any{"type": "zzz_tool"},
				map[string]any{"type": "aaa_tool"},
				map[string]any{"type": "zzz_tool"},
			},
			want: []string{"aaa_tool", "zzz_tool"},
		},
		{
			// Malformed entries are ValidateTools' job; reporting them here too
			// would double up on the same mistake.
			name:  "malformed entries are skipped",
			tools: []any{"not-a-map", map[string]any{"type": 7}, map[string]any{}},
			want:  nil,
		},
		{
			name:  "every preview tool type is recognized",
			tools: []any{map[string]any{"type": "sharepoint_grounding_preview"}, map[string]any{"type": "a2a_preview"}},
			want:  nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			agent := &PromptAgent{Tools: test.tools}
			require.Equal(t, test.want, agent.UnrecognizedToolTypes())
		})
	}
}

// TestPromptAgent_InjectedToolTypesAreRecognized guards against azd warning
// about a tool it injected itself.
func TestPromptAgent_InjectedToolTypesAreRecognized(t *testing.T) {
	t.Parallel()

	for _, injected := range []string{"file_search", "mcp", "memory_search_preview"} {
		_, known := knownPromptToolTypes[injected]
		require.True(t, known, "azd injects %q; it must be in the recognized set", injected)
	}
}

// TestPromptAgent_SamplingFieldsRoundTrip covers the four API definition fields
// that previously had no agent.yaml binding.
func TestPromptAgent_SamplingFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	content := []byte(`
kind: prompt
name: sampling-agent
model: gpt-4.1-mini
instructions: You are helpful.
temperature: 0
topP: 0.95
text:
  format:
    type: json_schema
reasoning:
  effort: high
`)

	var agent PromptAgent
	require.NoError(t, yaml.Unmarshal(content, &agent))

	// A pointer, so an explicit 0 is distinguishable from unset. Collapsing the
	// two would silently substitute the service default for "be deterministic".
	require.NotNil(t, agent.Temperature)
	require.Equal(t, 0.0, *agent.Temperature)

	require.NotNil(t, agent.TopP)
	require.Equal(t, 0.95, *agent.TopP)

	require.NotNil(t, agent.Text)
	require.NotNil(t, agent.Reasoning)

	request, err := CreatePromptAgentAPIRequest(agent, nil)
	require.NoError(t, err)

	definition, ok := request.Definition.(agent_api.ManagedAgentDefinition)
	require.True(t, ok, "definition type changed; update this assertion")
	require.NotNil(t, definition.Temperature)
	require.Equal(t, 0.0, *definition.Temperature)
	require.NotNil(t, definition.TopP)
	require.Equal(t, 0.95, *definition.TopP)
	require.NotNil(t, definition.Text)
	require.NotNil(t, definition.Reasoning)

	data, err := json.Marshal(request)
	require.NoError(t, err)
	body := string(data)
	require.Contains(t, body, `"top_p":0.95`)
	require.NotContains(t, body, `"topP"`)
}

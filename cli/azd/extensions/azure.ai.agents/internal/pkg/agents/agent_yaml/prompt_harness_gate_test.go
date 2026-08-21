// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateHarnessFields covers the sampling and output-shaping fields a
// harness controls itself. The harness-less cases matter most: they are the
// guarantee that this gate never narrows what a plain prompt agent accepts.
func TestValidateHarnessFields(t *testing.T) {
	t.Parallel()

	temperature := 0.7
	topP := 0.9

	cases := []struct {
		name        string
		agent       PromptAgent
		wantErr     bool
		wantMessage string
	}{
		{
			name:  "harness-less agent may set every field",
			agent: PromptAgent{Temperature: &temperature, TopP: &topP, ToolChoice: "auto", Text: map[string]any{}},
		},
		{
			name:  "harnessed agent with no sampling fields",
			agent: PromptAgent{Harness: "github-copilot"},
		},
		{
			name:        "harnessed agent rejects temperature",
			agent:       PromptAgent{Harness: "github-copilot", Temperature: &temperature},
			wantErr:     true,
			wantMessage: "temperature",
		},
		{
			name:        "harnessed agent rejects top_p",
			agent:       PromptAgent{Harness: "github-copilot", TopP: &topP},
			wantErr:     true,
			wantMessage: "top_p",
		},
		{
			name:        "harnessed agent rejects tool_choice",
			agent:       PromptAgent{Harness: "github-copilot", ToolChoice: "auto"},
			wantErr:     true,
			wantMessage: "tool_choice",
		},
		{
			name:        "harnessed agent rejects text",
			agent:       PromptAgent{Harness: "github-copilot", Text: map[string]any{"format": "json"}},
			wantErr:     true,
			wantMessage: "text",
		},
		{
			name: "all rejected fields are reported together in a stable order",
			agent: PromptAgent{
				Harness:     "github-copilot",
				Temperature: &temperature,
				TopP:        &topP,
				ToolChoice:  "auto",
				Text:        map[string]any{},
			},
			wantErr:     true,
			wantMessage: "temperature, top_p, tool_choice, text",
		},
		{
			name: "harnessed agent accepts reasoning.effort",
			agent: PromptAgent{
				Harness:   "github-copilot",
				Reasoning: map[string]any{"effort": "medium"},
			},
		},
		{
			name: "harnessed agent rejects other reasoning properties",
			agent: PromptAgent{
				Harness:   "github-copilot",
				Reasoning: map[string]any{"effort": "medium", "summary": "detailed"},
			},
			wantErr:     true,
			wantMessage: "reasoning.summary",
		},
		{
			name: "non-mapping reasoning is left to the schema check",
			agent: PromptAgent{
				Harness:   "github-copilot",
				Reasoning: "medium",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.agent.ValidateHarnessFields()
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMessage)
		})
	}
}

// TestValidateHarnessTools covers the tool types that have no representation in
// the platform-managed toolbox a harness dispatches through.
func TestValidateHarnessTools(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		agent       PromptAgent
		wantErr     bool
		wantMessage string
	}{
		{
			name: "harness-less agent may declare a function tool",
			agent: PromptAgent{
				Tools: []any{map[string]any{"type": "function", "name": "get_order_status"}},
			},
		},
		{
			name: "harness-less agent may declare a shell tool",
			agent: PromptAgent{
				Tools: []any{map[string]any{"type": "shell"}},
			},
		},
		{
			name: "harnessed agent accepts toolbox-backed tools",
			agent: PromptAgent{
				Harness: "github-copilot",
				Tools: []any{
					map[string]any{"type": "code_interpreter"},
					map[string]any{"type": "file_search"},
					map[string]any{"type": "mcp"},
				},
			},
		},
		{
			name: "harnessed agent rejects a function tool",
			agent: PromptAgent{
				Harness: "github-copilot",
				Tools:   []any{map[string]any{"type": "function", "name": "get_order_status"}},
			},
			wantErr:     true,
			wantMessage: "function",
		},
		{
			name: "harnessed agent rejects bing_grounding",
			agent: PromptAgent{
				Harness: "github-copilot",
				Tools:   []any{map[string]any{"type": "bing_grounding"}},
			},
			wantErr:     true,
			wantMessage: "bing_grounding",
		},
		{
			name: "rejected tool types are deduplicated and sorted",
			agent: PromptAgent{
				Harness: "github-copilot",
				Tools: []any{
					map[string]any{"type": "shell"},
					map[string]any{"type": "function", "name": "a"},
					map[string]any{"type": "function", "name": "b"},
				},
			},
			wantErr:     true,
			wantMessage: "function, shell",
		},
		{
			name: "an unrecognized tool type still deploys",
			agent: PromptAgent{
				Harness: "github-copilot",
				Tools:   []any{map[string]any{"type": "some_future_tool"}},
			},
		},
		{
			name: "a malformed entry is left to ValidateTools",
			agent: PromptAgent{
				Harness: "github-copilot",
				Tools:   []any{"not-a-mapping"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.agent.ValidateHarnessTools()
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMessage)
		})
	}
}

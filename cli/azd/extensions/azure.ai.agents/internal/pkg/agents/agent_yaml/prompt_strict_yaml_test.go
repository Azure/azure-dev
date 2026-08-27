// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// TestPromptHarness_RejectsStringForm pins the migration message for the
// breaking change from `harness: <name>` to a `harness:` block.
//
// The value of this change is entirely in the error text: without it go-yaml
// reports "cannot unmarshal !!str into agent_yaml.PromptHarness", which names a
// Go type and gives an author nothing to act on.
func TestPromptHarness_RejectsStringForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		yaml        string
		wantInError []string
	}{
		{
			name: "current spelling",
			yaml: "kind: prompt\nname: a\nmodel: m\nharness: github_copilot_preview\n",
			wantInError: []string{
				"harness must be a block, not a string",
				"type: github_copilot_preview",
			},
		},
		{
			// The obsolete abbreviation and the string form usually appear
			// together, since both come from the same older sample. The message
			// has to fix both at once or the author fixes one and hits the other.
			name: "obsolete abbreviation is upgraded in the suggested block",
			yaml: "kind: prompt\nname: a\nmodel: m\nharness: ghcp\n",
			wantInError: []string{
				"harness must be a block, not a string",
				"type: github_copilot_preview",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var agent PromptAgent
			err := yaml.Unmarshal([]byte(tt.yaml), &agent)
			require.Error(t, err)
			for _, want := range tt.wantInError {
				require.Contains(t, err.Error(), want)
			}
		})
	}
}

// TestPromptHarness_RejectsObsoleteType covers the block form carrying the old
// abbreviation. azd deliberately keeps no allowlist of harness names so a
// harness added by the service needs no azd release, which means an unknown
// name must still pass through -- only the name azd itself renamed is rejected.
func TestPromptHarness_RejectsObsoleteType(t *testing.T) {
	t.Parallel()

	var agent PromptAgent
	err := yaml.Unmarshal([]byte("kind: prompt\nname: a\nmodel: m\nharness:\n  type: ghcp\n"), &agent)
	require.Error(t, err)
	require.Contains(t, err.Error(), `harness.type "ghcp" is no longer accepted`)
	require.Contains(t, err.Error(), "github_copilot_preview")
}

// TestPromptHarness_UnknownHarnessTypePassesThrough is the negative of the test
// above: a name azd has never heard of is forwarded, not rejected.
func TestPromptHarness_UnknownHarnessTypePassesThrough(t *testing.T) {
	t.Parallel()

	var agent PromptAgent
	require.NoError(t, yaml.Unmarshal(
		[]byte("kind: prompt\nname: a\nmodel: m\nharness:\n  type: some_future_harness\n"), &agent))
	require.NotNil(t, agent.Harness)
	require.Equal(t, "some_future_harness", agent.Harness.Type)
}

// TestPromptAgent_RejectsUnknownKeysInAuthoredBlocks covers the blocks azd acts
// on rather than forwards. A key that binds to nothing in one of these deploys
// an agent that differs from its manifest with nothing in the output to say so
// -- `builtin_tool:` for `builtin_tools:` leaves every built-in capability on.
func TestPromptAgent_RejectsUnknownKeysInAuthoredBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		yaml     string
		wantKey  string
		wantHint string
	}{
		{
			name: "harness typo",
			yaml: "kind: prompt\nname: a\nmodel: m\n" +
				"harness:\n  type: github_copilot_preview\n  builtin_tool:\n    allowed: []\n",
			wantKey:  "builtin_tool",
			wantHint: "harness:",
		},
		{
			name: "nested environment typo",
			yaml: "kind: prompt\nname: a\nmodel: m\n" +
				"harness:\n  type: github_copilot_preview\n  environment:\n    cpus: \"1\"\n",
			wantKey:  "cpus",
			wantHint: "harness:",
		},
		{
			name: "memory typo",
			yaml: "kind: prompt\nname: a\nmodel: m\n" +
				"memory:\n  store: s\n  chat_modell: gpt-4.1-mini\n",
			wantKey:  "chat_modell",
			wantHint: "memory:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var agent PromptAgent
			err := yaml.Unmarshal([]byte(tt.yaml), &agent)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantKey)
			require.Contains(t, err.Error(), tt.wantHint)
		})
	}
}

// TestPromptAgent_ToolsStayForwardCompatible guards the boundary of the strict
// decoding above. Tools are passed to the service verbatim, so a tool type or
// property newer than this build must keep deploying -- strictness applies to
// the blocks azd interprets, not to the ones it forwards.
func TestPromptAgent_ToolsStayForwardCompatible(t *testing.T) {
	t.Parallel()

	const manifest = `kind: prompt
name: a
model: m
harness:
  type: github_copilot_preview
tools:
  - type: some_tool_invented_next_year
    some_property_azd_has_never_seen: true
`

	var agent PromptAgent
	require.NoError(t, yaml.Unmarshal([]byte(manifest), &agent))
	require.Len(t, agent.Tools, 1)

	tool, ok := agent.Tools[0].(map[string]any)
	require.True(t, ok, "tool entry should decode to a map, got %T", agent.Tools[0])
	require.Equal(t, "some_tool_invented_next_year", tool["type"])
}

// TestPromptHarness_EmptyBlockIsNotAnError pins the documented equivalence
// between an empty `harness:` block and the old bare-name string: Type is the
// only required field, and decodeStrict must not turn a null node into an error.
func TestPromptHarness_EmptyBlockIsNotAnError(t *testing.T) {
	t.Parallel()

	var harness PromptHarness
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("{}"), &node))
	require.NoError(t, harness.UnmarshalYAML(node.Content[0]))
	require.Empty(t, harness.Type)
}

// TestPromptHarness_RejectsListForm covers the remaining node kind. The message
// has to name what was found, since a list here usually means the author
// indented a `type:` under a `-`.
func TestPromptHarness_RejectsListForm(t *testing.T) {
	t.Parallel()

	var agent PromptAgent
	err := yaml.Unmarshal([]byte("kind: prompt\nname: a\nmodel: m\nharness:\n  - type: x\n"), &agent)
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "harness must be a block"),
		"unexpected error: %v", err)
}

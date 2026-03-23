// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolsetCommand_HasSubcommands(t *testing.T) {
	cmd := newToolsetCommand()

	subcommands := cmd.Commands()
	names := make([]string, len(subcommands))
	for i, c := range subcommands {
		names[i] = c.Name()
	}

	assert.Contains(t, names, "list")
	assert.Contains(t, names, "show")
	assert.Contains(t, names, "create")
	assert.Contains(t, names, "delete")
}

func TestToolsetListCommand_DefaultOutputFormat(t *testing.T) {
	cmd := newToolsetListCommand()

	output, _ := cmd.Flags().GetString("output")
	assert.Equal(t, "table", output)
}

func TestToolsetListCommand_HasFlags(t *testing.T) {
	cmd := newToolsetListCommand()

	f := cmd.Flags().Lookup("output")
	require.NotNil(t, f, "expected flag 'output'")
	assert.Equal(t, "o", f.Shorthand)
}

func TestToolsetShowCommand_RequiresArg(t *testing.T) {
	cmd := newToolsetShowCommand()

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
}

func TestToolsetShowCommand_DefaultOutputFormat(t *testing.T) {
	cmd := newToolsetShowCommand()

	output, _ := cmd.Flags().GetString("output")
	assert.Equal(t, "json", output)
}

func TestToolsetShowCommand_HasFlags(t *testing.T) {
	cmd := newToolsetShowCommand()

	f := cmd.Flags().Lookup("output")
	require.NotNil(t, f, "expected flag 'output'")
	assert.Equal(t, "o", f.Shorthand)
}

func TestToolsetCreateCommand_AcceptsOneArg(t *testing.T) {
	cmd := newToolsetCreateCommand()
	assert.NotNil(t, cmd.Args)
}

func TestToolsetDeleteCommand_RequiresArg(t *testing.T) {
	cmd := newToolsetDeleteCommand()

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
}

func TestToolsetNameToEnvVar(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "my-toolset", "MY_TOOLSET"},
		{"already upper", "MY_TOOLSET", "MY_TOOLSET"},
		{"dots and spaces", "my.toolset name", "MY_TOOLSET_NAME"},
		{"numeric", "tools123", "TOOLS123"},
		{"empty", "", ""},
		{"special chars", "a@b#c", "A_B_C"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolsetNameToEnvVar(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPrintToolsetListTable_Empty(t *testing.T) {
	list := &agent_api.ToolsetList{}
	err := printToolsetListTable(t.Context(), list)
	require.NoError(t, err)
}

func TestPrintToolsetListJSON_Empty(t *testing.T) {
	list := &agent_api.ToolsetList{}
	err := printToolsetListJSON(list)
	require.NoError(t, err)
}

func TestPrintToolsetListTable_WithData(t *testing.T) {
	list := &agent_api.ToolsetList{
		Data: []agent_api.ToolsetObject{
			{
				Name:        "test-toolset",
				Description: "A test toolset",
				Tools:       []json.RawMessage{json.RawMessage(`{"type":"mcp_server"}`)},
				CreatedAt:   1700000000,
			},
			{
				Name:        "long-desc",
				Description: "This description is longer than fifty characters and should be truncated",
				Tools:       nil,
			},
		},
	}

	err := printToolsetListTable(t.Context(), list)
	require.NoError(t, err)
}

func TestPrintToolsetListJSON_WithData(t *testing.T) {
	list := &agent_api.ToolsetList{
		Data: []agent_api.ToolsetObject{
			{
				Name:  "test-toolset",
				Tools: []json.RawMessage{json.RawMessage(`{"type":"mcp_server"}`)},
			},
		},
	}

	err := printToolsetListJSON(list)
	require.NoError(t, err)
}

func TestPrintToolsetShowJSON(t *testing.T) {
	toolset := &agent_api.ToolsetObject{
		Name:  "my-toolset",
		ID:    "ts-123",
		Tools: []json.RawMessage{json.RawMessage(`{"type":"openapi"}`)},
	}

	err := printToolsetShowJSON(toolset, "https://example.com/toolsets/my-toolset/mcp")
	require.NoError(t, err)
}

func TestPrintToolsetShowTable(t *testing.T) {
	toolset := &agent_api.ToolsetObject{
		Name:        "my-toolset",
		ID:          "ts-123",
		Description: "Test toolset",
		CreatedAt:   1700000000,
		UpdatedAt:   1700001000,
		Tools: []json.RawMessage{
			json.RawMessage(`{"type":"mcp_server","server_label":"my-mcp"}`),
			json.RawMessage(`{"type":"openapi"}`),
		},
	}

	err := printToolsetShowTable(toolset, "https://example.com/toolsets/my-toolset/mcp")
	require.NoError(t, err)
}

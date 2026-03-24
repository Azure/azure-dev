// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolboxCommand_HasSubcommands(t *testing.T) {
	cmd := newToolboxCommand()

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

func TestToolboxListCommand_DefaultOutputFormat(t *testing.T) {
	cmd := newToolboxListCommand()

	output, _ := cmd.Flags().GetString("output")
	assert.Equal(t, "table", output)
}

func TestToolboxListCommand_HasFlags(t *testing.T) {
	cmd := newToolboxListCommand()

	f := cmd.Flags().Lookup("output")
	require.NotNil(t, f, "expected flag 'output'")
	assert.Equal(t, "o", f.Shorthand)
}

func TestToolboxShowCommand_RequiresArg(t *testing.T) {
	cmd := newToolboxShowCommand()

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
}

func TestToolboxShowCommand_DefaultOutputFormat(t *testing.T) {
	cmd := newToolboxShowCommand()

	output, _ := cmd.Flags().GetString("output")
	assert.Equal(t, "json", output)
}

func TestToolboxShowCommand_HasFlags(t *testing.T) {
	cmd := newToolboxShowCommand()

	f := cmd.Flags().Lookup("output")
	require.NotNil(t, f, "expected flag 'output'")
	assert.Equal(t, "o", f.Shorthand)
}

func TestToolboxCreateCommand_AcceptsOneArg(t *testing.T) {
	cmd := newToolboxCreateCommand()
	assert.NotNil(t, cmd.Args)
}

func TestToolboxDeleteCommand_RequiresArg(t *testing.T) {
	cmd := newToolboxDeleteCommand()

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
}

func TestToolboxNameToEnvVar(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "my-toolbox", "MY_TOOLBOX"},
		{"already upper", "MY_TOOLBOX", "MY_TOOLBOX"},
		{"dots and spaces", "my.toolbox name", "MY_TOOLBOX_NAME"},
		{"numeric", "tools123", "TOOLS123"},
		{"empty", "", ""},
		{"special chars", "a@b#c", "A_B_C"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := project.ToolboxNameToEnvVar(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPrintToolboxListTable_Empty(t *testing.T) {
	list := &agent_api.ToolboxList{}
	err := printToolboxListTable(t.Context(), list)
	require.NoError(t, err)
}

func TestPrintToolboxListJSON_Empty(t *testing.T) {
	list := &agent_api.ToolboxList{}
	err := printToolboxListJSON(list)
	require.NoError(t, err)
}

func TestPrintToolboxListTable_WithData(t *testing.T) {
	list := &agent_api.ToolboxList{
		Data: []agent_api.ToolboxObject{
			{
				Name:        "test-toolbox",
				Description: "A Test toolbox",
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

	err := printToolboxListTable(t.Context(), list)
	require.NoError(t, err)
}

func TestPrintToolboxListJSON_WithData(t *testing.T) {
	list := &agent_api.ToolboxList{
		Data: []agent_api.ToolboxObject{
			{
				Name:  "test-toolbox",
				Tools: []json.RawMessage{json.RawMessage(`{"type":"mcp_server"}`)},
			},
		},
	}

	err := printToolboxListJSON(list)
	require.NoError(t, err)
}

func TestPrintToolboxShowJSON(t *testing.T) {
	toolbox := &agent_api.ToolboxObject{
		Name:  "my-toolbox",
		ID:    "ts-123",
		Tools: []json.RawMessage{json.RawMessage(`{"type":"openapi"}`)},
	}

	err := printToolboxShowJSON(toolbox, "https://example.com/toolsets/my-toolbox/mcp")
	require.NoError(t, err)
}

func TestPrintToolboxShowTable(t *testing.T) {
	toolbox := &agent_api.ToolboxObject{
		Name:        "my-toolbox",
		ID:          "ts-123",
		Description: "Test toolbox",
		CreatedAt:   1700000000,
		UpdatedAt:   1700001000,
		Tools: []json.RawMessage{
			json.RawMessage(`{"type":"mcp_server","server_label":"my-mcp"}`),
			json.RawMessage(`{"type":"openapi"}`),
		},
	}

	err := printToolboxShowTable(toolbox, "https://example.com/toolsets/my-toolbox/mcp")
	require.NoError(t, err)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package envkey

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolboxMCPEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple-hyphen", "my-tools", "TOOLBOX_MY_TOOLS_MCP_ENDPOINT"},
		{"single-space", "my tools", "TOOLBOX_MY_TOOLS_MCP_ENDPOINT"},
		{"mixed-segments", "agent-tools v2", "TOOLBOX_AGENT_TOOLS_V2_MCP_ENDPOINT"},
		{"already-upper", "TOOLS", "TOOLBOX_TOOLS_MCP_ENDPOINT"},
		{"dot-separator", "my.toolbox.v2", "TOOLBOX_MY_TOOLBOX_V2_MCP_ENDPOINT"},
		// Run-collapsing - without it doctor would search for
		// TOOLBOX_MY__TOOL_MCP_ENDPOINT and miss the real value.
		{"double-hyphen-run", "my--tool", "TOOLBOX_MY_TOOL_MCP_ENDPOINT"},
		// Symbol classes that bypassed the previous rune-by-rune
		// normalizer (it only mapped `-`, `.`, ` ` to `_`).
		{"plus", "my+tool", "TOOLBOX_MY_TOOL_MCP_ENDPOINT"},
		{"colon", "my:tool", "TOOLBOX_MY_TOOL_MCP_ENDPOINT"},
		{"slash", "my/tool", "TOOLBOX_MY_TOOL_MCP_ENDPOINT"},
		{"tab", "my\ttool", "TOOLBOX_MY_TOOL_MCP_ENDPOINT"},
		// Trailing non-alphanum produces a trailing underscore inside
		// the sanitized segment, which is consistent with how listen.go
		// has always written the value.
		{"parens", "my(tool)", "TOOLBOX_MY_TOOL__MCP_ENDPOINT"},
		{"mixed-case-symbols", "Web-Search:V2", "TOOLBOX_WEB_SEARCH_V2_MCP_ENDPOINT"},
		{"empty", "", "TOOLBOX__MCP_ENDPOINT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToolboxMCPEndpoint(tt.input)
			if got != tt.expected {
				t.Errorf("ToolboxMCPEndpoint(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSkillVersion(t *testing.T) {
	t.Parallel()
	require.Equal(t, "SKILL_SUMMARIZE_TOOLS_VERSION", SkillVersion("summarize-tools"))
	require.Equal(t, "SKILL_MY__SKILL_VERSION", SkillVersion("my--skill"))
	require.Equal(t, "SKILL_SUMMARIZE_TOOLS_PROJECT_ENDPOINT", SkillProjectEndpoint("summarize-tools"))
}

func TestReadinessScopeKeys(t *testing.T) {
	t.Parallel()
	require.Equal(t, "TOOLBOX_MY_TOOL_PROJECT_ENDPOINT", ToolboxProjectEndpoint("my-tool"))
	require.Equal(t, "AGENT_MY_AGENT_PROJECT_ENDPOINT", AgentProjectEndpoint("my-agent"))
	require.Equal(t, "AGENT_MY_AGENT_BLUEPRINT_CLIENT_ID", AgentBlueprintClientID("my-agent"))
	require.Equal(t, "AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT", ConnectionProjectEndpoint)
}

func TestConnectionServiceProjectEndpoint(t *testing.T) {
	t.Parallel()

	// Keep these wire-format vectors in sync with the Connections producer tests.
	tests := map[string]string{
		"search":         "CONNECTION_V2_736561726368_PROJECT_ENDPOINT",
		"my connection":  "CONNECTION_V2_6D7920636F6E6E656374696F6E_PROJECT_ENDPOINT",
		"my--connection": "CONNECTION_V2_6D792D2D636F6E6E656374696F6E_PROJECT_ENDPOINT",
		"A":              "CONNECTION_V2_41_PROJECT_ENDPOINT",
		"a":              "CONNECTION_V2_61_PROJECT_ENDPOINT",
	}
	for name, expected := range tests {
		require.Equal(t, expected, ConnectionServiceProjectEndpoint(name))
	}
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package envkey

import "testing"

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

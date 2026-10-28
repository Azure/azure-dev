// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"testing"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildToolboxVersionRequestAllowsSkillsOnly(t *testing.T) {
	request, err := buildToolboxVersionRequest(
		t.Context(), newStubConnectionResolver(), "https://example.test",
		&definition.Definition{Skills: []definition.SkillReference{{Name: "triage"}}},
	)
	require.NoError(t, err)
	assert.Empty(t, request.Tools)
	assert.Len(t, request.Skills, 1)
}

func TestCreateRejectsDefinitionNameMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{
		Name:  "from-file",
		Tools: []map[string]any{{"type": "web_search"}},
	}))

	err := runToolboxCreateWith(
		t.Context(), newMockToolboxClient("https://example.test"),
		newStubConnectionResolver(), "https://example.test", "from-command",
		toolboxCreateFlags{fromFile: path}, toolboxFlags{output: "json"},
	)
	requireLocalError(t, err, exterrors.CodeInvalidToolboxName)
}

func TestValidateToolboxDefinitionToolNames(t *testing.T) {
	for _, tc := range []struct {
		name        string
		connections []definition.ConnectionReference
		tools       []map[string]any
		wantCode    string
	}{
		{
			name:        "mixed duplicate names",
			connections: []definition.ConnectionReference{{Name: "mcp"}},
			tools:       []map[string]any{{"type": "web_search", "name": "mcp"}},
			wantCode:    exterrors.CodeDuplicateToolName,
		},
		{
			name:        "trimmed connection name",
			connections: []definition.ConnectionReference{{Name: " mcp "}},
			tools:       []map[string]any{{"type": "web_search", "name": "mcp"}},
			wantCode:    exterrors.CodeDuplicateToolName,
		},
		{
			name:        "distinct names",
			connections: []definition.ConnectionReference{{Name: "mcp"}},
			tools:       []map[string]any{{"type": "web_search", "name": "web"}},
		},
		{
			name:        "unnamed raw tool",
			connections: []definition.ConnectionReference{{Name: "mcp"}},
			tools:       []map[string]any{{"type": "web_search"}},
		},
		{
			name:        "case sensitive names",
			connections: []definition.ConnectionReference{{Name: "mcp"}},
			tools:       []map[string]any{{"type": "web_search", "name": "MCP"}},
		},
		{
			name: "duplicate raw names",
			tools: []map[string]any{
				{"type": "web_search", "name": "web"},
				{"type": "file_search", "name": "web"},
			},
			wantCode: exterrors.CodeDuplicateToolName,
		},
		{
			name:        "duplicate connections",
			connections: []definition.ConnectionReference{{Name: "mcp"}, {Name: "mcp"}},
			wantCode:    exterrors.CodeDuplicateConnection,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateToolboxDefinition(&definition.Definition{Connections: tc.connections, Tools: tc.tools})
			if tc.wantCode == "" {
				require.NoError(t, err)
				return
			}
			requireLocalError(t, err, tc.wantCode)
		})
	}
}

func TestBuildToolboxVersionRequestRejectsResolvedDuplicateToolNames(t *testing.T) {
	resolver := newStubConnectionResolver()
	resolver.byName["alias"] = &projectConnection{
		ID: "/connections/mcp", Category: "RemoteTool", Name: "mcp", Target: "https://mcp.test",
	}

	request, err := buildToolboxVersionRequest(t.Context(), resolver, "https://example.test",
		&definition.Definition{
			Connections: []definition.ConnectionReference{{Name: "alias"}},
			Tools:       []map[string]any{{"type": "web_search", "name": "mcp"}},
		})
	requireLocalError(t, err, exterrors.CodeDuplicateToolName)
	assert.Nil(t, request)
}

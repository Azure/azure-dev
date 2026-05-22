// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"strings"
	"testing"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/connections"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireLocalError asserts err is an *azdext.LocalError with the given code.
func requireLocalError(t *testing.T, err error, code string) *azdext.LocalError {
	t.Helper()
	require.Error(t, err)
	le, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected LocalError, got %T: %v", err, err)
	assert.Equal(t, code, le.Code, "code mismatch in %v", le)
	return le
}

func TestValidateToolboxName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"simple", "research", false},
		{"with dash", "my-tools", false},
		{"with underscore", "my_tools", false},
		{"mixed", "Tools_v2-alpha", false},
		{"max length", strings.Repeat("a", maxToolboxNameLength), false},
		{"empty", "", true},
		{"slash", "a/b", true},
		{"space", "my tools", true},
		{"dot", "tools.v1", true},
		{"too long", strings.Repeat("a", maxToolboxNameLength+1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateToolboxName(tc.input)
			if tc.wantErr {
				requireLocalError(t, err, exterrors.CodeInvalidToolboxName)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateOutputFormat(t *testing.T) {
	for _, ok := range []string{"", "table", "json", "Table", "JSON"} {
		assert.NoError(t, validateOutputFormat(ok), "expected %q to be accepted", ok)
	}
	err := validateOutputFormat("yaml")
	requireLocalError(t, err, exterrors.CodeInvalidParameter)
}

func TestBuildToolEntry(t *testing.T) {
	t.Run("RemoteTool builds mcp entry", func(t *testing.T) {
		entry, err := buildToolEntry(&projectConnection{
			ID:       "/subs/x/.../connections/my-mcp",
			Category: connections.ConnectionTypeRemoteTool,
			Name:     "my-mcp",
			Target:   "https://mcp.example.com",
		}, "", "")
		require.NoError(t, err)
		assert.Equal(t, "mcp", entry["type"])
		assert.Equal(t, "my-mcp", entry["name"])
		assert.Equal(t, "my-mcp", entry["server_label"])
		assert.Equal(t, "https://mcp.example.com", entry["server_url"])
		assert.Equal(t, "/subs/x/.../connections/my-mcp", entry["project_connection_id"])
	})

	t.Run("RemoteTool rejects --index", func(t *testing.T) {
		_, err := buildToolEntry(&projectConnection{
			Category: connections.ConnectionTypeRemoteTool,
			Name:     "my-mcp",
		}, "idx", "")
		requireLocalError(t, err, exterrors.CodeUnsupportedIndexFlag)
	})

	t.Run("RemoteTool rejects --instance-name", func(t *testing.T) {
		_, err := buildToolEntry(&projectConnection{
			Category: connections.ConnectionTypeRemoteTool,
			Name:     "my-mcp",
			Target:   "https://mcp.example.com",
		}, "", "inst")
		requireLocalError(t, err, exterrors.CodeUnsupportedInstanceNameFlag)
	})

	t.Run("RemoteTool rejects empty target", func(t *testing.T) {
		_, err := buildToolEntry(&projectConnection{
			ID:       "/c/x",
			Category: connections.ConnectionTypeRemoteTool,
			Name:     "x",
			Target:   "  ", // whitespace-only is treated as empty
		}, "", "")
		le := requireLocalError(t, err, exterrors.CodeConnectionMissingTarget)
		assert.Contains(t, le.Message, "target URL")
	})

	t.Run("CognitiveSearch requires --index", func(t *testing.T) {
		_, err := buildToolEntry(&projectConnection{
			Category: connections.ConnectionTypeCognitiveSearch,
			Name:     "search",
		}, "", "")
		requireLocalError(t, err, exterrors.CodeMissingIndex)
	})

	t.Run("CognitiveSearch builds azure_ai_search entry", func(t *testing.T) {
		entry, err := buildToolEntry(&projectConnection{
			ID:       "/subs/x/.../connections/search",
			Category: connections.ConnectionTypeCognitiveSearch,
			Name:     "search",
		}, "products", "")
		require.NoError(t, err)
		assert.Equal(t, "azure_ai_search", entry["type"])
		search := entry["azure_ai_search"].(map[string]any)
		indexes := search["indexes"].([]any)
		require.Len(t, indexes, 1)
		first := indexes[0].(map[string]any)
		assert.Equal(t, "products", first["index_name"])
		assert.Equal(t, "/subs/x/.../connections/search", first["project_connection_id"])
	})

	t.Run("RemoteA2A builds a2a_preview entry", func(t *testing.T) {
		entry, err := buildToolEntry(&projectConnection{
			ID:       "/subs/x/.../connections/my-a2a",
			Category: connections.ConnectionTypeRemoteA2A,
			Name:     "my-a2a",
		}, "", "")
		require.NoError(t, err)
		assert.Equal(t, "a2a_preview", entry["type"])
		assert.Equal(t, "my-a2a", entry["name"])
		assert.Equal(t, "/subs/x/.../connections/my-a2a", entry["project_connection_id"])
	})

	t.Run("GroundingWithCustomSearch requires --instance-name", func(t *testing.T) {
		_, err := buildToolEntry(&projectConnection{
			Category: connections.ConnectionTypeGroundingWithCustomSearch,
			Name:     "bing",
		}, "", "")
		requireLocalError(t, err, exterrors.CodeMissingInstanceName)
	})

	t.Run("GroundingWithCustomSearch builds web_search entry", func(t *testing.T) {
		entry, err := buildToolEntry(&projectConnection{
			ID:       "/subs/x/.../connections/bing",
			Category: connections.ConnectionTypeGroundingWithCustomSearch,
			Name:     "bing",
		}, "", "docs-config")
		require.NoError(t, err)
		assert.Equal(t, "web_search", entry["type"])
		cfg := entry["custom_search_configuration"].(map[string]any)
		assert.Equal(t, "/subs/x/.../connections/bing", cfg["project_connection_id"])
		assert.Equal(t, "docs-config", cfg["instance_name"])
	})

	t.Run("unsupported category rejected", func(t *testing.T) {
		for _, cat := range []connections.ConnectionType{
			connections.ConnectionTypeApiKey,
			connections.ConnectionTypeCustomKeys,
			connections.ConnectionTypeAppInsights,
		} {
			_, err := buildToolEntry(&projectConnection{Category: cat, Name: "x"}, "", "")
			le := requireLocalError(t, err, exterrors.CodeUnsupportedConnectionCategory)
			assert.Contains(t, le.Message, string(cat),
				"expected category in message")
		}
	})
}

func TestDuplicateConnectionInTools(t *testing.T) {
	tools := []map[string]any{
		{"type": "mcp", "project_connection_id": "/conn/a"},
		{
			"type": "azure_ai_search",
			"azure_ai_search": map[string]any{
				"indexes": []any{
					map[string]any{"project_connection_id": "/conn/b", "index_name": "x"},
				},
			},
		},
		{
			"type": "web_search",
			"custom_search_configuration": map[string]any{
				"project_connection_id": "/conn/d", "instance_name": "inst",
			},
		},
		{"type": "a2a_preview", "project_connection_id": "/conn/f"},
	}
	assert.True(t, duplicateConnectionInTools(tools, "/conn/a"))
	assert.True(t, duplicateConnectionInTools(tools, "/conn/b"))
	assert.True(t, duplicateConnectionInTools(tools, "/conn/d"))
	assert.True(t, duplicateConnectionInTools(tools, "/conn/f"))
	assert.False(t, duplicateConnectionInTools(tools, "/conn/zzz"))
}

func TestFilterOutConnection(t *testing.T) {
	tools := []map[string]any{
		{"type": "mcp", "project_connection_id": "/conn/a", "name": "a"},
		{"type": "code_interpreter", "name": "ci"}, // built-in carries through
		{"type": "mcp", "project_connection_id": "/conn/b", "name": "b"},
		{
			"type": "azure_ai_search",
			"name": "s",
			"azure_ai_search": map[string]any{
				"indexes": []any{
					map[string]any{"project_connection_id": "/conn/c"},
				},
			},
		},
		{
			"type": "web_search",
			"name": "ws",
			"custom_search_configuration": map[string]any{
				"project_connection_id": "/conn/d", "instance_name": "inst",
			},
		},
		{"type": "a2a_preview", "name": "a2a", "project_connection_id": "/conn/f"},
	}
	got, removed := filterOutConnection(tools, "/conn/a")
	assert.True(t, removed)
	assert.Len(t, got, 5)

	// Removing missing connection: removed=false, slice unchanged in length.
	got2, removed2 := filterOutConnection(tools, "/conn/zzz")
	assert.False(t, removed2)
	assert.Len(t, got2, 6)

	// Removing nested search connection.
	got3, removed3 := filterOutConnection(tools, "/conn/c")
	assert.True(t, removed3)
	assert.Len(t, got3, 5)

	// Removing web_search (custom_search_configuration nested).
	got4, removed4 := filterOutConnection(tools, "/conn/d")
	assert.True(t, removed4)
	assert.Len(t, got4, 5)

	// Removing a2a_preview (top-level project_connection_id).
	got6, removed6 := filterOutConnection(tools, "/conn/f")
	assert.True(t, removed6)
	assert.Len(t, got6, 5)
}

func TestShortConnectionName(t *testing.T) {
	assert.Equal(t, "my-mcp", shortConnectionName("/subs/x/connections/my-mcp"))
	assert.Equal(t, "plain", shortConnectionName("plain"))
	assert.Equal(t, "", shortConnectionName(""))
}

func TestBuildToolboxMcpURL(t *testing.T) {
	got := buildToolboxMcpURL("https://acct.services.ai.azure.com/api/projects/p", "research", "3")
	assert.Equal(t,
		"https://acct.services.ai.azure.com/api/projects/p/toolboxes/research/versions/3/mcp?api-version=v1",
		got,
	)

	// Service-supplied version strings could in theory contain unsafe URL chars.
	// Both segments must be PathEscaped so downstream consumers can use the URL
	// without parsing surprises.
	escaped := buildToolboxMcpURL(
		"https://acct.services.ai.azure.com/api/projects/p",
		"research",
		"v 1/2", // space and slash require escaping
	)
	assert.Contains(t, escaped, "versions/v%201%2F2/mcp")
}

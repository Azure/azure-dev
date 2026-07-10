// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// stubToolboxConnResolver is a connectionResolver test double that returns a fixed
// project connection so buildToolEntries can be exercised without a live project.
type stubToolboxConnResolver struct {
	id     string
	target string
}

func (s stubToolboxConnResolver) resolveConnection(
	_ context.Context, _, name string,
) (*projectConnection, error) {
	return &projectConnection{ID: s.id, Name: name, Target: s.target}, nil
}

func TestParseToolboxServiceConfig_ServiceLevel(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"description": "research tools",
		"tools": []any{
			map[string]any{"type": "web_search"},
			map[string]any{"type": "mcp", "connection": "github-mcp"},
		},
	})
	require.NoError(t, err)

	cfg, err := parseToolboxServiceConfig(&azdext.ServiceConfig{
		Name:                 "research",
		Host:                 aiToolboxHost,
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "research tools", cfg.Description)
	require.Len(t, cfg.Tools, 2)
	assert.Equal(t, "web_search", cfg.Tools[0]["type"])
	assert.Equal(t, "github-mcp", cfg.Tools[1]["connection"])
}

func TestBuildToolEntries_ResolvesConnectionRef(t *testing.T) {
	t.Parallel()

	tgt := &toolboxServiceTarget{
		resolver: stubToolboxConnResolver{id: "/sub/conn/github-mcp", target: "https://mcp.example.com"},
	}
	tools := []map[string]any{
		{"type": "web_search"},
		{"type": "mcp", "connection": "github-mcp"},
	}

	out, err := tgt.buildToolEntries(context.Background(), "https://proj.example.com", tools, nil)
	require.NoError(t, err)
	require.Len(t, out, 2)

	// Non-connection tool passes through unchanged.
	assert.Equal(t, "web_search", out[0]["type"])

	// Connection-backed tool gets project_connection_id + server_url; the connection
	// name key is dropped.
	assert.Equal(t, "/sub/conn/github-mcp", out[1]["project_connection_id"])
	assert.Equal(t, "https://mcp.example.com", out[1]["server_url"])
	_, hasConnection := out[1]["connection"]
	assert.False(t, hasConnection)
}

func TestExpandToolboxValue(t *testing.T) {
	t.Parallel()

	serviceConfig := &azdext.ServiceConfig{
		Environment: map[string]string{
			"MCP_URL": "https://resolved.example.com",
		},
	}
	environment, err := (&toolboxServiceTarget{}).environmentValues(
		t.Context(),
		serviceConfig,
	)
	require.NoError(t, err)
	in := map[string]any{
		"type":       "mcp",
		"server_url": "${MCP_URL}",
		"headers":    []any{"x-secret: ${{secrets.token}}"},
	}

	out, ok := expandToolboxValue(
		in,
		environment,
	).(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://resolved.example.com", out["server_url"])
	// Foundry ${{...}} passes through untouched.
	assert.Equal(t, []any{"x-secret: ${{secrets.token}}"}, out["headers"])
}

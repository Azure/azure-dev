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

func TestParseToolboxServiceConfig_Endpoint(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"endpoint": "${RESEARCH_TOOLBOX_ENDPOINT}",
	})
	require.NoError(t, err)

	cfg, err := parseToolboxServiceConfig(&azdext.ServiceConfig{
		Name:                 "research",
		Host:                 aiToolboxHost,
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "${RESEARCH_TOOLBOX_ENDPOINT}", cfg.Endpoint)
	assert.Empty(t, cfg.Tools)
}

func TestResolveReuseEndpoint_ExpandsVar(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"RESEARCH_TOOLBOX_ENDPOINT": "https://acct.services.ai.azure.com/api/projects/p/toolboxes/research/versions/3/mcp?api-version=v1",
	}
	cfg := &toolboxServiceConfig{Endpoint: "${RESEARCH_TOOLBOX_ENDPOINT}"}

	got, err := resolveReuseEndpoint("research", cfg, env)
	require.NoError(t, err)
	assert.Equal(t, env["RESEARCH_TOOLBOX_ENDPOINT"], got)
}

func TestResolveReuseEndpoint_PlainEndpoint(t *testing.T) {
	t.Parallel()

	cfg := &toolboxServiceConfig{Endpoint: "https://mcp.example.com/toolboxes/research/versions/1/mcp"}

	got, err := resolveReuseEndpoint("research", cfg, nil)
	require.NoError(t, err)
	assert.Equal(t, cfg.Endpoint, got)
}

func TestResolveReuseEndpoint_RejectsToolsWithEndpoint(t *testing.T) {
	t.Parallel()

	cfg := &toolboxServiceConfig{
		Endpoint: "https://mcp.example.com/toolboxes/research/versions/1/mcp",
		Tools:    []map[string]any{{"type": "web_search"}},
	}

	_, err := resolveReuseEndpoint("research", cfg, nil)
	require.Error(t, err)
}

func TestResolveReuseEndpoint_RejectsDescriptionWithEndpoint(t *testing.T) {
	t.Parallel()

	cfg := &toolboxServiceConfig{
		Endpoint:    "https://mcp.example.com/toolboxes/research/versions/1/mcp",
		Description: "reused tools",
	}

	_, err := resolveReuseEndpoint("research", cfg, nil)
	require.Error(t, err)
}

func TestResolveReuseEndpoint_RejectsEmptyResolved(t *testing.T) {
	t.Parallel()

	// ${MISSING} expands to empty when env does not define it.
	cfg := &toolboxServiceConfig{Endpoint: "${MISSING}"}

	_, err := resolveReuseEndpoint("research", cfg, map[string]string{})
	require.Error(t, err)
}

func TestPublishReuseEndpoint_WritesExpandedEndpoint(t *testing.T) {
	// No t.Parallel: stubToolboxEndpointEnv swaps a package-level seam.
	const wantURL = "https://acct.services.ai.azure.com/api/projects/p/toolboxes/research/versions/3/mcp?api-version=v1"

	calls := stubToolboxEndpointEnv(t)

	// A zero-value target proves the reuse path never builds a toolbox
	// client or creates a version (it holds no azd client or resolver).
	tgt := &toolboxServiceTarget{}
	cfg := &toolboxServiceConfig{Endpoint: "${RESEARCH_TOOLBOX_ENDPOINT}"}
	env := map[string]string{"RESEARCH_TOOLBOX_ENDPOINT": wantURL}

	res, err := tgt.publishReuseEndpoint(t.Context(), "research", cfg, env, nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, *calls, 1)
	assert.Equal(t, "research", (*calls)[0].name)
	assert.Equal(t, wantURL, (*calls)[0].value)
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

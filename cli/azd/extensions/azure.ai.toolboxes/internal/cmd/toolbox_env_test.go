// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"testing"

	"azure.ai.toolboxes/internal/foundry/connections"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Blank value rather than a key delete: there is no delete-key RPC.
func TestRunDeleteToolbox_ClearsEndpointEnv(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	envCalls := stubToolboxEndpointEnv(t)

	err := runDeleteToolbox(
		t.Context(), client, "tb", toolboxDeleteFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.deleteCalls, 1)
	require.Equal(t, []toolboxEnvCall{{name: "tb", value: "", projectScope: "https://e/"}}, *envCalls)
}

func TestRunDeleteToolbox_NotFoundAttemptsMarkerCleanup(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	envCalls := stubToolboxEndpointEnv(t)

	err := runDeleteToolbox(
		t.Context(), client, "tb", toolboxDeleteFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Equal(t, []toolboxEnvCall{{name: "tb", value: "", projectScope: "https://e/"}}, *envCalls)
}

func TestRunDeleteToolboxVersion_DoesNotTouchEnv(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "5"}}
	envCalls := stubToolboxEndpointEnv(t)

	err := runDeleteToolboxVersion(
		t.Context(), client, "tb",
		toolboxDeleteFlags{version: "3", force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.deleteVersionCalls, 1)
	assert.Empty(t, *envCalls)
}

func TestRunToolboxCreateWith_EnvSyncErrorPropagates(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	resolver := newStubConnectionResolver()
	resolver.byName["mcp"] = &projectConnection{
		ID: "/c/mcp", Category: connections.ConnectionTypeRemoteTool, Name: "mcp", Target: "https://mcp.example.com",
	}
	sentinel := errors.New("env write failed")
	envCalls := stubToolboxEndpointEnvErr(t, sentinel)
	inputPath := t.TempDir() + "/create.yaml"
	require.NoError(t, os.WriteFile(inputPath, []byte("description: tb\nconnections:\n  - name: mcp\n"), 0o600))

	err := runToolboxCreateWith(
		t.Context(), client, resolver, "https://e/", "tb",
		toolboxCreateFlags{fromFile: inputPath}, toolboxFlags{output: "json"},
	)
	require.ErrorIs(t, err, sentinel)
	require.Len(t, client.createVersionCalls, 1)
	require.Len(t, *envCalls, 1)
}

func TestRunDeleteToolbox_EnvSyncErrorDoesNotHideDeletion(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{Name: "tb", DefaultVersion: "1"}}
	sentinel := errors.New("env clear failed")
	stubToolboxEndpointEnvErr(t, sentinel)

	err := runDeleteToolbox(
		t.Context(), client, "tb", toolboxDeleteFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.deleteCalls, 1)
}

func TestIsNoAzdEnvironment(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"unavailable", status.Error(codes.Unavailable, "connection refused"), true},
		{"no default env", status.Error(codes.Unknown, "default environment not found"), true},
		{"no project", status.Error(codes.Unknown, "no project exists; run `azd init`"), true},
		{"other unknown", status.Error(codes.Unknown, "something else broke"), false},
		{"plain error", errors.New("not a grpc status"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNoAzdEnvironment(tt.err))
		})
	}
}

func TestToolboxProjectEndpoint(t *testing.T) {
	t.Parallel()
	require.Equal(t,
		"https://account.services.ai.azure.com/api/projects/project",
		toolboxProjectEndpoint(
			"https://account.services.ai.azure.com/api/projects/project/toolboxes/tools/versions/1/mcp?api-version=v1",
		),
	)
	require.Empty(t, toolboxProjectEndpoint("https://example.com/mcp"))
}

func TestClearToolboxMarkers(t *testing.T) {
	values := map[string]string{
		"TOOLBOX_TOOLS_MCP_ENDPOINT":     "https://example/mcp",
		"TOOLBOX_TOOLS_PROJECT_ENDPOINT": "https://example/project",
	}
	err := clearToolboxMarkers("tools", func(key, value string) error {
		values[key] = value
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, values["TOOLBOX_TOOLS_MCP_ENDPOINT"])
	require.Empty(t, values["TOOLBOX_TOOLS_PROJECT_ENDPOINT"])
}

func TestShouldClearToolboxMarkers(t *testing.T) {
	t.Parallel()
	const current = "https://account.services.ai.azure.com/api/projects/current"
	require.False(t, shouldClearToolboxMarkers(current+"/", "", current))
	require.True(t, shouldClearToolboxMarkers(
		"",
		current+"/toolboxes/tools/versions/1/mcp",
		current,
	))
	require.False(t, shouldClearToolboxMarkers(
		current,
		"https://account.services.ai.azure.com/api/projects/other/toolboxes/tools/versions/1/mcp",
		current,
	))
	require.False(t, shouldClearToolboxMarkers("", "", current))
}

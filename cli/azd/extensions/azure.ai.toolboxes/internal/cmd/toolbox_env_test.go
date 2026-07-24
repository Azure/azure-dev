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
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	envCalls := stubToolboxEndpointEnv(t)

	err := runDeleteToolbox(
		t.Context(), client, "tb",
		toolboxDeleteFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.deleteCalls, 1)

	require.Len(t, *envCalls, 1)
	assert.Equal(t, "tb", (*envCalls)[0].name)
	assert.Empty(t, (*envCalls)[0].value, "delete must blank the endpoint value")
}

// Only whole-toolbox delete clears the env var, not version delete.
func TestRunDeleteToolboxVersion_DoesNotTouchEnv(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "5",
	}}
	envCalls := stubToolboxEndpointEnv(t)

	err := runDeleteToolboxVersion(
		t.Context(), client, "tb",
		toolboxDeleteFlags{version: "3", force: true}, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.deleteVersionCalls, 1)
	assert.Empty(t, *envCalls, "version delete must not update the endpoint env var")
}

// A failure writing the env var on create is surfaced to the caller.
func TestRunToolboxCreateWith_EnvSyncErrorPropagates(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	resolver := newStubConnectionResolver()
	resolver.byName["mcp"] = &projectConnection{
		ID: "/c/mcp", Category: connections.ConnectionTypeRemoteTool, Name: "mcp",
		Target: "https://mcp.example.com",
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
	require.Len(t, client.createVersionCalls, 1, "toolbox is created before the env write")
	require.Len(t, *envCalls, 1)
}

// A failure clearing the env var on delete is surfaced to the caller.
func TestRunDeleteToolbox_EnvSyncErrorPropagates(t *testing.T) {
	client := newMockToolboxClient("https://e/")
	client.getResults["tb"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "tb", DefaultVersion: "1",
	}}
	sentinel := errors.New("env clear failed")
	stubToolboxEndpointEnvErr(t, sentinel)

	err := runDeleteToolbox(
		t.Context(), client, "tb",
		toolboxDeleteFlags{force: true}, toolboxFlags{output: "json"},
	)
	require.ErrorIs(t, err, sentinel)
	require.Len(t, client.deleteCalls, 1, "toolbox is deleted before the env clear")
}

func TestIsNoAzdEnvironment(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"unavailable", status.Error(codes.Unavailable, "connection refused"), true},
		// The host returns a status-less sentinel that grpc maps to Unknown.
		{"no default env", status.Error(codes.Unknown, "default environment not found"), true},
		{"no project", status.Error(codes.Unknown, "no project exists; to create a new project, run `azd init`"), true},
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

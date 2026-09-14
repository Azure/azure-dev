// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"testing"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/connections"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunToolboxDeployWithCreatesVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{
		Name:        "support-tools",
		Description: "Support tools",
		Connections: []definition.ConnectionReference{{Name: "search", Index: "tickets"}},
		Skills:      []definition.SkillReference{{Name: "triage"}},
		Metadata:    map[string]string{"owner": "support"},
	}))

	client := newMockToolboxClient("https://example.test/project")
	resolver := newStubConnectionResolver()
	resolver.byName["search"] = &projectConnection{
		ID: "/connections/search", Category: connections.ConnectionTypeCognitiveSearch, Name: "search",
	}
	envCalls := stubToolboxEndpointEnv(t)

	err := runToolboxDeployWith(
		t.Context(), client, resolver, client.Endpoint(), path, toolboxFlags{output: "json"},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Equal(t, "support-tools", client.createVersionCalls[0].name)
	assert.Equal(t, "Support tools", client.createVersionCalls[0].req.Description)
	assert.Equal(t, map[string]string{"owner": "support"}, client.createVersionCalls[0].req.Metadata)
	assert.Len(t, client.createVersionCalls[0].req.Tools, 1)
	assert.Len(t, client.createVersionCalls[0].req.Skills, 1)
	require.Len(t, *envCalls, 1)
	assert.Equal(t, "https://example.test/project", (*envCalls)[0].projectScope)
}

func TestRunToolboxDeployWithExplicitEndpointDoesNotSyncEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{
		Name:  "support-tools",
		Tools: []map[string]any{{"type": "web_search"}},
	}))

	const endpoint = "https://project-b.services.ai.azure.com/api/projects/project-b"
	client := newMockToolboxClient(endpoint)
	envCalls := stubToolboxEndpointEnv(t)

	err := runToolboxDeployWith(
		t.Context(), client, newStubConnectionResolver(), endpoint, path,
		toolboxFlags{output: "json", projectEndpoint: endpoint},
	)
	require.NoError(t, err)
	require.Len(t, client.createVersionCalls, 1)
	assert.Empty(t, *envCalls)
}

func TestRunToolboxDeployWithRequiresName(t *testing.T) {
	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{
		Tools: []map[string]any{{"type": "web_search"}},
	}))

	err := runToolboxDeployWith(
		t.Context(), newMockToolboxClient("https://example.test"),
		newStubConnectionResolver(), "https://example.test", path,
		toolboxFlags{output: "json"},
	)
	requireLocalError(t, err, exterrors.CodeInvalidToolboxName)
}

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

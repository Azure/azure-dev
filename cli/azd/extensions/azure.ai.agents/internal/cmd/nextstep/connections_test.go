// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestAssembleState_SplitConnectionsOnly(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"search-conn": {
					Name: "search-conn",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"category": "CognitiveSearch",
						"target":   "https://search.example",
					}),
				},
				"bing-conn": {
					Name: "bing-conn",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"category": "ApiKey",
						"target":   "https://api.bing.example",
					}),
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Empty(t, errs)
	require.True(t, state.HasConnections)
	require.Empty(t, state.ConnectionLoadErrors)
	require.Len(t, state.Connections, 2)
	assert.Equal(t, "bing-conn", state.Connections[0].Name)
	assert.Equal(t, "bing-conn", state.Connections[0].ServiceName)
	assert.Equal(t, "ApiKey | https://api.bing.example", state.Connections[0].Detail)
	assert.Equal(t, "search-conn", state.Connections[1].Name)
	assert.Equal(t, "CognitiveSearch | https://search.example", state.Connections[1].Detail)
}

func TestAssembleState_ConnectionUsesPayloadName(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"azure-search": {
					Name: "azure-search",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"name":     "payload-name",
						"category": "CognitiveSearch",
						"target":   "https://search.example",
					}),
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Empty(t, errs)
	require.Len(t, state.Connections, 1)
	assert.Equal(t, "payload-name", state.Connections[0].Name)
	assert.Equal(t, "azure-search", state.Connections[0].ServiceName)
}

func TestAssembleState_DisabledConnectionSkipsRefErrors(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		configValues: map[string]*structpb.Value{
			"off-conn/condition": structpb.NewBoolValue(false),
		},
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"off-conn": {
					Name: "off-conn",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"$ref": "./missing-connection.yaml",
					}),
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Empty(t, errs)
	require.Empty(t, state.ConnectionLoadErrors)
	require.False(t, state.HasConnections)
	assert.Empty(t, state.Connections)
}

func TestAssembleState_ResolvedConnectionConditionUsesRootField(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProjectFile(t, root, "connection.yaml", `
category: ApiKey
target: https://connection.example
condition: false
`)
	src := &fakeSource{
		envName: "dev",
		configValues: map[string]*structpb.Value{
			"conn/condition": structpb.NewBoolValue(true),
		},
		project: &azdext.ProjectConfig{
			Path: root,
			Services: map[string]*azdext.ServiceConfig{
				"conn": {
					Name: "conn",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"$ref": "./connection.yaml",
					}),
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.NotEmpty(t, errs)
	require.Len(t, state.ConnectionLoadErrors, 1)
	require.True(t, state.HasConnections)
	require.Len(t, state.Connections, 1)
	assert.Contains(t, state.ConnectionLoadErrors[0], "resolved $ref")
	assert.Contains(t, state.ConnectionLoadErrors[0], "put condition beside host in azure.yaml")
}

func TestAssembleState_InvalidConnectionConditionIsLoadError(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		configValues: map[string]*structpb.Value{
			"bad-conn/condition": structpb.NewStringValue("${"),
		},
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"bad-conn": {
					Name: "bad-conn",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"category": "ApiKey",
						"target":   "https://bad.example",
					}),
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.NotEmpty(t, errs)
	require.False(t, state.HasConnections)
	require.Len(t, state.ConnectionLoadErrors, 1)
	assert.Contains(t, state.ConnectionLoadErrors[0], `connection service "bad-conn"`)
	assert.Contains(t, state.ConnectionLoadErrors[0], "invalid deployment condition")
}

func TestAssembleState_ActiveConnectionRefErrorIsLoadError(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"broken-conn": {
					Name: "broken-conn",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"$ref": "./missing-connection.yaml",
					}),
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.NotEmpty(t, errs)
	require.False(t, state.HasConnections)
	require.Len(t, state.ConnectionLoadErrors, 1)
	assert.Contains(t, state.ConnectionLoadErrors[0], `connection service "broken-conn"`)
	assert.Contains(t, state.ConnectionLoadErrors[0], "resolve $ref")
}

func TestAssembleState_ConnectionTargetKeepsVarRef(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"search-conn": {
					Name: "search-conn",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"category":    "CognitiveSearch",
						"target":      "${SEARCH_URL}",
						"credentials": map[string]any{"key": "super-secret"},
					}),
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Empty(t, errs)
	require.Len(t, state.Connections, 1)
	assert.Equal(t, "CognitiveSearch | ${SEARCH_URL}", state.Connections[0].Detail)
	assert.NotContains(t, state.Connections[0].Detail, "super-secret")
}

func TestAssembleState_IgnoresUnsupportedAgentConnectionSources(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	serviceDir := filepath.Join(root, "src", "echo")
	require.NoError(t, os.MkdirAll(serviceDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(serviceDir, "agent.manifest.yaml"),
		[]byte(`
resources:
  - name: manifest-connection
    kind: connection
    category: ApiKey
    target: https://manifest.example
`),
		0o600,
	))

	agent := newAgentService(t, map[string]any{
		"kind": "hostedAgent",
		"connections": []any{
			map[string]any{
				"name":     "object-connection",
				"category": "ApiKey",
				"target":   "https://object.example",
			},
		},
	})
	agent.RelativePath = "src/echo"
	agent.Config = mustStruct(t, map[string]any{
		"kind": "hostedAgent",
		"connections": []any{
			map[string]any{
				"name":     "nested-connection",
				"category": "ApiKey",
				"target":   "https://nested.example",
			},
		},
	})

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: root,
			Services: map[string]*azdext.ServiceConfig{
				"echo": agent,
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Empty(t, errs)
	assert.False(t, state.HasConnections)
	assert.Empty(t, state.Connections)
	assert.Empty(t, state.ConnectionLoadErrors)
}

func TestFormatConnectionDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		category string
		target   string
		want     string
	}{
		{"both populated", "AzureOpenAI", "https://x.openai.azure.com/", "AzureOpenAI | https://x.openai.azure.com/"},
		{"only category", "AzureOpenAI", "", "AzureOpenAI"},
		{"only target", "", "https://x.openai.azure.com/", "https://x.openai.azure.com/"},
		{"both empty", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, formatConnectionDetail(tc.category, tc.target))
		})
	}
}

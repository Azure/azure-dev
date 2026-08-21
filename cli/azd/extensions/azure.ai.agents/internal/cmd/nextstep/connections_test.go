// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
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

func TestAssembleState_ConnectionUsesServiceKeyAsName(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"azure-search": {
					Name: "azure-search",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"name":     "ignored-body-name",
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
	assert.Equal(t, "azure-search", state.Connections[0].Name)
}

func TestAssembleState_DisabledConnectionIsSkipped(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		configValues: map[string]*structpb.Value{
			"off-conn/condition": structpb.NewBoolValue(false),
		},
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"live-conn": {
					Name: "live-conn",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"category": "ApiKey",
						"target":   "https://live.example",
					}),
				},
				"off-conn": {
					Name: "off-conn",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"category":    "ApiKey",
						"target":      "https://off.example",
						"credentials": map[string]any{"key": "super-secret"},
					}),
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Empty(t, errs)
	require.Empty(t, state.ConnectionLoadErrors)
	require.Len(t, state.Connections, 1)
	assert.Equal(t, "live-conn", state.Connections[0].Name)
	assert.NotContains(t, state.Connections[0].Detail, "super-secret")
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

func TestAssembleState_InvalidBundledAgentConditionIsLoadError(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		configValues: map[string]*structpb.Value{
			"agent/condition": structpb.NewStringValue("${"),
		},
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"agent": {
					Name: "agent",
					Host: agentHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"kind": "hostedAgent",
						"connections": []any{
							map[string]any{
								"name":     "search",
								"category": "ApiKey",
								"target":   "https://search.example",
							},
						},
					}),
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.NotEmpty(t, errs)
	require.False(t, state.HasConnections)
	require.Len(t, state.ConnectionLoadErrors, 1)
	assert.Contains(t, state.ConnectionLoadErrors[0], `agent service "agent"`)
	assert.Contains(t, state.ConnectionLoadErrors[0], "deployment condition")
}

func TestAssembleState_InvalidManifestAgentConditionIsLoadError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeManifest(t, root, "src/echo", `
template:
  kind: containerAgent
  name: echo
resources:
  - name: search
    kind: connection
    category: ApiKey
    target: https://search.example
`)
	src := &fakeSource{
		envName: "dev",
		configValues: map[string]*structpb.Value{
			"echo/condition": structpb.NewStringValue("${"),
		},
		project: &azdext.ProjectConfig{
			Path: root,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {
					Name:         "echo",
					Host:         agentHost,
					RelativePath: "src/echo",
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.NotEmpty(t, errs)
	require.False(t, state.HasConnections)
	require.Len(t, state.ConnectionLoadErrors, 1)
	assert.Contains(t, state.ConnectionLoadErrors[0], `agent service "echo"`)
	assert.Contains(t, state.ConnectionLoadErrors[0], "deployment condition")
}

func TestAssembleState_ActiveConnectionRefErrorIsLoadError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: root,
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

func TestAssembleState_ConnectionSourcePrecedence(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeManifest(t, root, "src/echo", `
template:
  kind: containerAgent
  name: echo
resources:
  - name: shared-conn
    kind: connection
    category: BingLLMSearch
    target: https://manifest.example
  - name: manifest-only
    kind: connection
    category: ApiKey
    target: https://manifest-only.example
`)

	agent := newAgentService(t, map[string]any{
		"kind": "hostedAgent",
		"connections": []any{
			map[string]any{
				"name":     "shared-conn",
				"category": "ApiKey",
				"target":   "https://bundled.example",
			},
			map[string]any{
				"name":     "bundled-only",
				"category": "RemoteTool",
				"target":   "https://bundled-only.example",
			},
		},
	})
	agent.RelativePath = "src/echo"

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: root,
			Services: map[string]*azdext.ServiceConfig{
				"echo": agent,
				"shared-conn": {
					Name: "shared-conn",
					Host: connectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"category": "CognitiveSearch",
						"target":   "https://split.example",
					}),
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Empty(t, errs)
	require.True(t, state.HasConnections)
	require.Len(t, state.Connections, 3)

	byName := map[string]ResourceRef{}
	for _, ref := range state.Connections {
		byName[ref.Name] = ref
	}
	assert.Equal(t, "CognitiveSearch | https://split.example", byName["shared-conn"].Detail)
	assert.Equal(t, "shared-conn", byName["shared-conn"].ServiceName)
	assert.Equal(t, "RemoteTool | https://bundled-only.example", byName["bundled-only"].Detail)
	assert.Equal(t, "echo", byName["bundled-only"].ServiceName)
	assert.Equal(t, "ApiKey | https://manifest-only.example", byName["manifest-only"].Detail)
	assert.Equal(t, "echo", byName["manifest-only"].ServiceName)
}

func TestAssembleState_BundledWinsOverManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeManifest(t, root, "src/echo", `
template:
  kind: containerAgent
  name: echo
resources:
  - name: shared-conn
    kind: connection
    category: BingLLMSearch
    target: https://manifest.example
`)

	agent := newAgentService(t, map[string]any{
		"kind": "hostedAgent",
		"connections": []any{
			map[string]any{
				"name":     "shared-conn",
				"category": "ApiKey",
				"target":   "https://bundled.example",
			},
		},
	})
	agent.RelativePath = "src/echo"

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
	require.Len(t, state.Connections, 1)
	assert.Equal(t, "ApiKey | https://bundled.example", state.Connections[0].Detail)
}

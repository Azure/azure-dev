// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const manifestThreeKinds = `
template:
  kind: containerAgent
  name: hello
resources:
  - name: gpt-4o
    kind: model
    id: azureml://registries/azure-openai/models/gpt-4o/versions/2024-08-06
  - name: web-search
    kind: toolbox
    tools:
      - id: tool-1
  - name: bing-conn
    kind: connection
    category: BingLLMSearch
    target: https://api.bing.microsoft.com/
    authType: ApiKey
`

const manifestNoResources = `
template:
  kind: containerAgent
  name: hello
`

const manifestModelsOnly = `
resources:
  - name: gpt-4o-mini
    kind: model
    id: azureml://registries/azure-openai/models/gpt-4o-mini/versions/2024-07-18
`

// writeManifest writes data to <projectRoot>/<rel>/agent.manifest.yaml,
// creating intermediate directories as needed.
func writeManifest(t *testing.T, projectRoot, rel, data string) {
	t.Helper()
	dir := filepath.Join(projectRoot, rel)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	path := filepath.Join(dir, "agent.manifest.yaml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
}

func TestAssembleState_ManifestWalker_AllThreeKinds(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeManifest(t, projectRoot, "src/echo", manifestThreeKinds)

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "src/echo"},
			},
		},
	}

	state, _ := assembleState(context.Background(), src)

	assert.True(t, state.HasModels)
	assert.True(t, state.HasToolboxes)
	assert.True(t, state.HasConnections)

	require.Len(t, state.ModelRefs, 1)
	assert.Equal(t, "gpt-4o", state.ModelRefs[0].Name)
	assert.Equal(t, "echo", state.ModelRefs[0].ServiceName)
	assert.Contains(t, state.ModelRefs[0].Detail, "gpt-4o")

	require.Len(t, state.Toolboxes, 1)
	assert.Equal(t, "web-search", state.Toolboxes[0].Name)
	assert.Equal(t, "echo", state.Toolboxes[0].ServiceName)
	assert.Empty(t, state.Toolboxes[0].Detail)

	require.Len(t, state.Connections, 1)
	assert.Equal(t, "bing-conn", state.Connections[0].Name)
	assert.Equal(t, "echo", state.Connections[0].ServiceName)
	assert.Equal(t, "BingLLMSearch | https://api.bing.microsoft.com/", state.Connections[0].Detail)
}

func TestAssembleState_UnifiedSplitResources(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"ai-project": {
					Name: "ai-project",
					Host: aiProjectHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"deployments": []any{
							map[string]any{
								"name": "gpt-4o",
								"model": map[string]any{
									"name": "gpt-4o",
								},
							},
						},
					}),
				},
				"search-conn": {
					Name: "search-conn",
					Host: aiConnectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"category": "CognitiveSearch",
						"target":   "https://search.example",
						"authType": "ApiKey",
					}),
				},
				"research-tools": {
					Name: "research-tools",
					Host: aiToolboxHost,
				},
				"echo": {
					Name: "echo",
					Host: agentHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"kind":      "hosted",
						"name":      "echo-agent",
						"toolboxes": []any{"research-tools"},
						"env": map[string]any{
							"TOOLBOX_ENDPOINT": "${TOOLBOX_RESEARCH_TOOLS_MCP_ENDPOINT}",
						},
					}),
				},
			},
		},
		values: map[string]string{
			"dev/" + projectEndpointVar: "https://example",
		},
	}

	state, errs := assembleState(t.Context(), src)

	require.Empty(t, errs)
	require.Len(t, state.ModelRefs, 1)
	assert.Equal(t, "gpt-4o", state.ModelRefs[0].Name)
	assert.Equal(t, "ai-project", state.ModelRefs[0].ServiceName)

	require.Len(t, state.Connections, 1)
	assert.Equal(t, "search-conn", state.Connections[0].Name)
	assert.False(t, state.Connections[0].ManagedByDeploy)
	assert.Equal(
		t,
		"CognitiveSearch | https://search.example",
		state.Connections[0].Detail,
	)

	require.Len(t, state.Toolboxes, 1)
	assert.Equal(t, "research-tools", state.Toolboxes[0].Name)
	assert.True(t, state.Toolboxes[0].ManagedByDeploy)

	require.Len(t, state.MissingToolboxEndpoints, 1)
	assert.Equal(
		t,
		"research-tools",
		state.MissingToolboxEndpoints[0].Name,
	)
	assert.True(
		t,
		state.MissingToolboxEndpoints[0].ManagedByDeploy,
	)
	assert.Empty(t, state.MissingManualVars)
}

func TestAssembleState_RecordsSplitResourceErrors(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"broken-conn": {
					Name: "broken-conn",
					Host: aiConnectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"$ref": "./missing-connection.yaml",
					}),
				},
				"echo": {
					Name: "echo",
					Host: agentHost,
					AdditionalProperties: mustStruct(t, map[string]any{
						"kind": "hosted",
						"name": "echo-agent",
					}),
				},
			},
		},
		values: map[string]string{
			"dev/" + projectEndpointVar: "https://example",
		},
	}

	state, errs := assembleState(t.Context(), src)

	require.NotEmpty(t, errs)
	require.Len(t, state.ConnectionLoadErrors, 1)
	assert.Contains(
		t,
		state.ConnectionLoadErrors[0],
		"missing-connection.yaml",
	)
	assert.False(t, state.HasConnections)
}

func TestAssembleState_ManifestWalker_RootRelativePath(t *testing.T) {
	t.Parallel()

	for _, rel := range []string{"", "."} {
		t.Run(fmt.Sprintf("rel=%q", rel), func(t *testing.T) {
			t.Parallel()

			projectRoot := t.TempDir()
			writeManifest(t, projectRoot, rel, manifestModelsOnly)

			src := &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{
					Path: projectRoot,
					Services: map[string]*azdext.ServiceConfig{
						"echo": {Name: "echo", Host: agentHost, RelativePath: rel},
					},
				},
			}

			state, errs := assembleState(context.Background(), src)

			require.Empty(t, errs)
			assert.True(t, state.HasModels)
			require.Len(t, state.ModelRefs, 1)
			assert.Equal(t, "gpt-4o-mini", state.ModelRefs[0].Name)
		})
	}
}

func TestAssembleState_ManifestWalker_MissingManifestNoError(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	// Service exists in azure.yaml but its directory has no manifest file
	// at all. Walker must degrade silently.
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "src/echo"), 0o750))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "src/echo"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)

	for _, err := range errs {
		assert.NotContains(t, err.Error(), "manifest")
	}
	assert.False(t, state.HasModels)
	assert.False(t, state.HasToolboxes)
	assert.False(t, state.HasConnections)
	assert.Nil(t, state.ModelRefs)
	assert.Nil(t, state.Toolboxes)
	assert.Nil(t, state.Connections)
}

func TestAssembleState_ManifestWalker_RejectsTraversal(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(projectRoot, 0o750))
	require.NoError(t, os.MkdirAll(outside, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "agent.manifest.yaml"), []byte(manifestThreeKinds), 0o600))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "../outside"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)

	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "must not contain '..'")
	assert.False(t, state.HasModels)
	assert.False(t, state.HasToolboxes)
	assert.False(t, state.HasConnections)
}

func TestAssembleState_ManifestWalker_ReportsMalformedManifest(
	t *testing.T,
) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeManifest(t, projectRoot, "src/echo", "::: this is not valid yaml :::")

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "src/echo"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)

	require.NotEmpty(t, errs)
	require.Len(t, state.ModelLoadErrors, 1)
	require.Len(t, state.ToolboxLoadErrors, 1)
	require.Len(t, state.ConnectionLoadErrors, 1)
	assert.Contains(t, state.ModelLoadErrors[0], "load resources for echo")
	assert.False(t, state.HasModels)
	assert.False(t, state.HasToolboxes)
	assert.False(t, state.HasConnections)
}

func TestAssembleState_ManifestWalker_NoResourcesKey(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeManifest(t, projectRoot, "src/echo", manifestNoResources)

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "src/echo"},
			},
		},
	}

	state, _ := assembleState(context.Background(), src)
	assert.False(t, state.HasModels)
	assert.False(t, state.HasToolboxes)
	assert.False(t, state.HasConnections)
}

func TestAssembleState_ManifestWalker_AggregatesAcrossServices(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeManifest(t, projectRoot, "src/a", manifestModelsOnly)
	writeManifest(t, projectRoot, "src/b", manifestThreeKinds)

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"a": {Name: "a", Host: agentHost, RelativePath: "src/a"},
				"b": {Name: "b", Host: agentHost, RelativePath: "src/b"},
			},
		},
	}

	state, _ := assembleState(context.Background(), src)
	assert.True(t, state.HasModels)
	assert.True(t, state.HasToolboxes)
	assert.True(t, state.HasConnections)

	require.Len(t, state.ModelRefs, 2)
	// Sorted by Name ascending: gpt-4o (from "b") < gpt-4o-mini (from "a").
	assert.Equal(t, "gpt-4o", state.ModelRefs[0].Name)
	assert.Equal(t, "b", state.ModelRefs[0].ServiceName)
	assert.Equal(t, "gpt-4o-mini", state.ModelRefs[1].Name)
	assert.Equal(t, "a", state.ModelRefs[1].ServiceName)
}

func TestAssembleState_ManifestWalker_DedupSameServiceSameName(t *testing.T) {
	t.Parallel()

	const dupManifest = `
resources:
  - name: gpt-4o
    kind: model
    id: first
  - name: gpt-4o
    kind: model
    id: second
`
	projectRoot := t.TempDir()
	writeManifest(t, projectRoot, "src/echo", dupManifest)

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "src/echo"},
			},
		},
	}

	state, _ := assembleState(context.Background(), src)
	require.Len(t, state.ModelRefs, 1)
	// First occurrence wins; subsequent dup is skipped silently.
	assert.Equal(t, "first", state.ModelRefs[0].Detail)
}

func TestAssembleState_ManifestWalker_PrefersYamlOverYml(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "src/echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "src/echo", "agent.manifest.yaml"),
		[]byte(manifestModelsOnly),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "src/echo", "agent.manifest.yml"),
		[]byte(manifestThreeKinds),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "src/echo"},
			},
		},
	}

	state, _ := assembleState(context.Background(), src)
	// .yaml winner has models only, no toolboxes / connections.
	assert.True(t, state.HasModels)
	assert.False(t, state.HasToolboxes)
	assert.False(t, state.HasConnections)
	require.Len(t, state.ModelRefs, 1)
	assert.Equal(t, "gpt-4o-mini", state.ModelRefs[0].Name)
}

func TestAssembleState_ManifestWalker_IgnoresAgentYamlOnly(t *testing.T) {
	t.Parallel()

	// agent.yaml (not agent.manifest.yaml) describes the container; it is
	// not a manifest. The walker must NOT mistake it for one even when the
	// content happens to parse: a service with only agent.yaml should
	// surface no resources.
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "src/echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "src/echo", "agent.yaml"),
		[]byte(manifestThreeKinds),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "src/echo"},
			},
		},
	}

	state, _ := assembleState(context.Background(), src)
	assert.False(t, state.HasModels)
	assert.False(t, state.HasToolboxes)
	assert.False(t, state.HasConnections)
}

func TestConnectionDetail(t *testing.T) {
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
			assert.Equal(
				t,
				tc.want,
				connectionDetail(tc.category, tc.target),
			)
		})
	}
}

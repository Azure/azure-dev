// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// mustStruct builds a structpb.Struct from a Go map, failing the test on
// any value that cannot be represented. Used to seed a service's
// azure.yaml `config:` block the way the gRPC project snapshot delivers it.
func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	require.NoError(t, err)
	return s
}

// configThreeKinds is an azure.yaml `config:` block declaring one model
// deployment, one toolbox, and one connection — the config analogue of a
// fully-populated agent.
func configThreeKinds(t *testing.T) *structpb.Struct {
	t.Helper()
	return mustStruct(t, map[string]any{
		"deployments": []any{
			map[string]any{
				"name": "gpt-4o",
				"model": map[string]any{
					"name":    "gpt-4o",
					"format":  "OpenAI",
					"version": "2024-08-06",
				},
			},
		},
		"toolboxes": []any{
			map[string]any{"name": "web-search"},
		},
		"connections": []any{
			map[string]any{
				"name":     "bing-conn",
				"category": "BingLLMSearch",
				"target":   "https://api.bing.microsoft.com/",
			},
		},
	})
}

// configModelsOnly declares a single model deployment and nothing else.
func configModelsOnly(t *testing.T) *structpb.Struct {
	t.Helper()
	return mustStruct(t, map[string]any{
		"deployments": []any{
			map[string]any{
				"name": "gpt-4o-mini",
				"model": map[string]any{
					"name":    "gpt-4o-mini",
					"format":  "OpenAI",
					"version": "2024-07-18",
				},
			},
		},
	})
}

func TestAssembleState_ServiceResources_AllThreeKinds(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "src/echo", Config: configThreeKinds(t)},
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
	assert.Equal(t, "gpt-4o (2024-08-06)", state.ModelRefs[0].Detail)

	require.Len(t, state.Toolboxes, 1)
	assert.Equal(t, "web-search", state.Toolboxes[0].Name)
	assert.Equal(t, "echo", state.Toolboxes[0].ServiceName)
	assert.Empty(t, state.Toolboxes[0].Detail)

	require.Len(t, state.Connections, 1)
	assert.Equal(t, "bing-conn", state.Connections[0].Name)
	assert.Equal(t, "echo", state.Connections[0].ServiceName)
	assert.Equal(t, "BingLLMSearch | https://api.bing.microsoft.com/", state.Connections[0].Detail)
}

func TestAssembleState_ServiceResources_RootRelativePath(t *testing.T) {
	t.Parallel()

	for _, rel := range []string{"", "."} {
		t.Run("rel="+rel, func(t *testing.T) {
			t.Parallel()

			src := &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{
					Path: t.TempDir(),
					Services: map[string]*azdext.ServiceConfig{
						"echo": {Name: "echo", Host: agentHost, RelativePath: rel, Config: configModelsOnly(t)},
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

func TestAssembleState_ServiceResources_NoConfigNoError(t *testing.T) {
	t.Parallel()

	// Service exists in azure.yaml but carries no `config:` block at all.
	// The parser must degrade silently.
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "src/echo"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)

	for _, err := range errs {
		assert.NotContains(t, err.Error(), "config")
	}
	assert.False(t, state.HasModels)
	assert.False(t, state.HasToolboxes)
	assert.False(t, state.HasConnections)
	assert.Nil(t, state.ModelRefs)
	assert.Nil(t, state.Toolboxes)
	assert.Nil(t, state.Connections)
}

func TestAssembleState_ServiceResources_EmptyConfigNoResources(t *testing.T) {
	t.Parallel()

	// A `config:` block that carries container settings but no resource
	// arrays surfaces zero resources.
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"echo": {
					Name: "echo", Host: agentHost, RelativePath: "src/echo",
					Config: mustStruct(t, map[string]any{"startupCommand": "python main.py"}),
				},
			},
		},
	}

	state, _ := assembleState(context.Background(), src)
	assert.False(t, state.HasModels)
	assert.False(t, state.HasToolboxes)
	assert.False(t, state.HasConnections)
}

func TestAssembleState_ServiceResources_MalformedConfigNoError(t *testing.T) {
	t.Parallel()

	// A `config:` block whose resource arrays have the wrong shape (here
	// `deployments` is a scalar instead of a list) fails to unmarshal into
	// the slim projection. The parser must skip the service silently —
	// no panic, no resources, no error surfaced to the caller.
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"echo": {
					Name: "echo", Host: agentHost, RelativePath: "src/echo",
					Config: mustStruct(t, map[string]any{"deployments": "not-a-list"}),
				},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	for _, err := range errs {
		assert.NotContains(t, err.Error(), "config")
	}
	assert.False(t, state.HasModels)
	assert.False(t, state.HasToolboxes)
	assert.False(t, state.HasConnections)
	assert.Nil(t, state.ModelRefs)
}

func TestAssembleState_ServiceResources_IgnoresNonAgentServices(t *testing.T) {
	t.Parallel()

	// A non-agent service may carry a `config:` block that happens to have
	// a toolboxes-shaped key; the resource walk must only consider
	// azure.ai.agent services.
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"web": {
					Name: "web", Host: "containerapp", RelativePath: "src/web",
					Config: configThreeKinds(t),
				},
			},
		},
	}

	state, _ := assembleState(context.Background(), src)
	assert.False(t, state.HasModels)
	assert.False(t, state.HasToolboxes)
	assert.False(t, state.HasConnections)
}

func TestAssembleState_ServiceResources_AggregatesAcrossServices(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"a": {Name: "a", Host: agentHost, RelativePath: "src/a", Config: configModelsOnly(t)},
				"b": {Name: "b", Host: agentHost, RelativePath: "src/b", Config: configThreeKinds(t)},
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

func TestAssembleState_ServiceResources_DedupSameServiceSameName(t *testing.T) {
	t.Parallel()

	dupConfig := mustStruct(t, map[string]any{
		"deployments": []any{
			map[string]any{"name": "gpt-4o", "model": map[string]any{"name": "first"}},
			map[string]any{"name": "gpt-4o", "model": map[string]any{"name": "second"}},
		},
	})

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "src/echo", Config: dupConfig},
			},
		},
	}

	state, _ := assembleState(context.Background(), src)
	require.Len(t, state.ModelRefs, 1)
	// First occurrence wins; subsequent dup is skipped silently.
	assert.Equal(t, "first", state.ModelRefs[0].Detail)
}

func TestModelDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		model   string
		version string
		want    string
	}{
		{"name and version", "gpt-4.1", "2025-04-14", "gpt-4.1 (2025-04-14)"},
		{"only name", "gpt-4.1", "", "gpt-4.1"},
		{"only version", "", "2025-04-14", "2025-04-14"},
		{"both empty", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := agentDeployment{}
			d.Model.Name = tc.model
			d.Model.Version = tc.version
			assert.Equal(t, tc.want, modelDetail(d))
		})
	}
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
			assert.Equal(t, tc.want, connectionDetail(tc.category, tc.target))
		})
	}
}

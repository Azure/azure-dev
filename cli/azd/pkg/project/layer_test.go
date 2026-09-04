// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectConfigCopyRuntimeStateMatchesLayersByName(t *testing.T) {
	t.Parallel()

	dispatcher := ext.NewEventDispatcher[ServiceLifecycleEventArgs]()
	source := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "first", Services: map[string]*ServiceConfig{"api": {EventDispatcher: dispatcher}}},
		{Name: "second", Services: map[string]*ServiceConfig{"worker": {}}},
	}}
	target := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "second", Services: map[string]*ServiceConfig{"worker": {}}},
		{Name: "first", Services: map[string]*ServiceConfig{"api": {}}},
	}}

	source.CopyRuntimeStateTo(target)

	require.Same(t, dispatcher, target.Layers[1].Services["api"].EventDispatcher)
}

func TestParseProjectLayers(t *testing.T) {
	t.Parallel()

	projectConfig, err := Parse(t.Context(), `name: layered-project
layers:
  - name: application
    infra:
      - name: app-infra
        path: ./infra/app
        provider: bicep
    services:
      api:
        project: ./src/api
        host: containerapp
        language: js
`)

	require.NoError(t, err)
	require.Len(t, projectConfig.Layers, 1)
	assert.Equal(t, "application", projectConfig.Layers[0].Name)
	require.Len(t, projectConfig.Layers[0].Infra, 1)
	assert.Equal(t, "app-infra", projectConfig.Layers[0].Infra[0].Name)
	assert.Equal(t, provisioning.Bicep, projectConfig.Layers[0].Infra[0].Provider)
	require.Contains(t, projectConfig.Layers[0].Services, "api")
	assert.Equal(t, "api", projectConfig.Layers[0].Services["api"].Name)
	assert.Equal(t, "application", projectConfig.Layers[0].Infra[0].Layer)
}

func TestParseProjectLayersRejectsMixedFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		infra string
	}{
		{name: "configured infra", infra: "infra:\n  provider: bicep"},
		{name: "empty infra", infra: "infra: {}"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(t.Context(), fmt.Sprintf(`name: mixed-project
%s
layers:
  - name: application
    services:
      api:
        host: containerapp
        image: example/api:latest
`, test.infra))

			require.ErrorContains(t, err, "'layers' cannot be combined with top-level 'infra' or 'services'")
		})
	}
}

func TestParseProjectLayersRejectsInvalidContainers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "empty layer",
			yaml:    "name: test-project\nlayers:\n  - name: application\n",
			wantErr: "must contain infrastructure or services",
		},
		{
			name: "duplicate service",
			yaml: `name: test-project
layers:
  - name: first
    services:
      api:
        host: containerapp
        image: example/api:latest
  - name: second
    services:
      api:
        host: containerapp
        image: example/api:latest
`,
			wantErr: "service 'api' is defined in both layers",
		},
		{
			name: "duplicate infrastructure entry",
			yaml: `name: test-project
layers:
  - name: first
    infra:
      - name: shared
        provider: terraform
        path: infra/first
  - name: second
    infra:
      - name: shared
        provider: terraform
        path: infra/second
`,
			wantErr: "infrastructure entry 'shared' is defined in both layers",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(t.Context(), test.yaml)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestSaveProjectLayersPreservesV2Shape(t *testing.T) {
	t.Parallel()

	projectConfig, err := Parse(t.Context(), `name: layered-project
layers:
  - name: application
    infra:
      - name: app-infra
        path: ./infra/app
        provider: bicep
    services:
      api:
        project: ./src/api
        host: containerapp
        language: js
`)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "azure.yaml")
	require.NoError(t, Save(t.Context(), projectConfig, path))
	contents, err := os.ReadFile(path)
	require.NoError(t, err)

	yaml := string(contents)
	require.Contains(t, yaml, "/schemas/alpha/azure.yaml.json")
	require.Contains(t, yaml, "layers:")
	require.Contains(t, yaml, "- name: application")
	require.Contains(t, yaml, "infra:")
	require.Contains(t, yaml, "- provider: bicep")
	require.Contains(t, yaml, "services:")
	require.Contains(t, yaml, "api:")
	require.NotContains(t, yaml, "layer: application")
	require.Equal(t, 1, strings.Count(yaml, "layers:"))
}

func TestSaveProjectLayersPreservesEmptyLayers(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{
		Name:   "layered-project",
		Layers: LayerConfigs{},
	}
	path := filepath.Join(t.TempDir(), "azure.yaml")
	require.NoError(t, Save(t.Context(), projectConfig, path))

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(contents), "layers: []")

	reloaded, err := Load(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, ProjectFormatLayersV2, reloaded.Format())
	require.Empty(t, reloaded.Layers)
}

func TestValidateLayerGraph_AcceptsV2Project(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Layers: []*LayerConfig{
		{
			Name: "foundry",
			Infra: []provisioning.Options{
				{Name: "foundry-account", Provider: provisioning.Bicep},
				{Name: "foundry-project", Provider: "microsoft.foundry", DependsOn: []string{"foundry-account"}},
			},
			Services: map[string]*ServiceConfig{"ai-project": {Name: "ai-project"}},
		},
		{
			Name: "agents",
			Infra: []provisioning.Options{
				{Name: "agent-resources", Provider: provisioning.Bicep, DependsOn: []string{"foundry-project"}},
			},
			Services: map[string]*ServiceConfig{
				"writer-agent": {Name: "writer-agent", Uses: []string{"ai-project"}},
			},
		},
	}}

	require.NoError(t, ValidateLayerGraph(projectConfig))
}

func TestValidateLayerGraph_RejectsLayerCycle(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "a", Infra: []provisioning.Options{{Name: "a-infra", DependsOn: []string{"b-infra"}}}},
		{Name: "b", Infra: []provisioning.Options{{Name: "b-infra", DependsOn: []string{"a-infra"}}}},
	}}

	err := ValidateLayerGraph(projectConfig)

	require.ErrorContains(t, err, "circular dependency")
}

func TestValidateLayerGraph_RejectsIntraLayerInfrastructureCycle(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Layers: []*LayerConfig{
		{
			Name: "application",
			Infra: []provisioning.Options{
				{Name: "api", DependsOn: []string{"worker"}},
				{Name: "worker", DependsOn: []string{"api"}},
			},
		},
	}}

	err := ValidateLayerGraph(projectConfig)

	require.ErrorContains(t, err, "circular dependency detected at infrastructure layer")
}

func TestValidateLayerGraph_RejectsUnknownInfraDependency(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "application", Infra: []provisioning.Options{{Name: "application", DependsOn: []string{"missing"}}}},
	}}

	err := ValidateLayerGraph(projectConfig)

	require.ErrorContains(t, err, "depends on unknown infrastructure layer")
}

func TestValidateLayerGraph_RejectsDuplicateOutputOwner(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "a", Infra: []provisioning.Options{{Name: "a", Outputs: map[string]string{"OUTPUT_A": "SHARED"}}}},
		{Name: "b", Infra: []provisioning.Options{{Name: "b", Outputs: map[string]string{"OUTPUT_B": "SHARED"}}}},
	}}

	err := ValidateLayerGraph(projectConfig)

	require.ErrorContains(t, err, "owned by both infrastructure layers")
}

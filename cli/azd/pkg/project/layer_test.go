// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
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
		name     string
		topLevel string
	}{
		{name: "configured infra", topLevel: "infra:\n  provider: bicep"},
		{name: "services", topLevel: "services:\n  worker:\n    host: containerapp\n    image: example/worker:latest"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			yaml := fmt.Sprintf("name: mixed-project\n%s\n"+
				"layers:\n"+
				"  - name: application\n"+
				"    services:\n"+
				"      api:\n"+
				"        host: containerapp\n"+
				"        image: example/api:latest\n", test.topLevel)
			_, err := Parse(t.Context(), yaml)

			require.ErrorContains(t, err, "'layers' cannot be combined with top-level 'infra' or 'services'")
		})
	}
}

func TestParseProjectLayersAllowsEmptyTopLevelInfra(t *testing.T) {
	t.Parallel()

	// This is a really small edge case, but just documenting it here to establish that it was considered
	// and it's not a big enough deal to worry about at this time - we just ignore it and use the layers they've
	// configured.
	_, err := Parse(t.Context(), `name: layered-project
# OH NO - AN EMPTY LITERAL!
infra: {}
layers:
  - name: application
    services:
      api:
        host: containerapp
        image: example/api:latest
`)

	require.NoError(t, err)
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

func TestSaveProjectLayersRejectsMixedFormatsBeforeWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ProjectConfig)
	}{
		{
			name: "top-level services",
			mutate: func(config *ProjectConfig) {
				config.Services = map[string]*ServiceConfig{"api": {Name: "api"}}
			},
		},
		{
			name: "top-level infra",
			mutate: func(config *ProjectConfig) {
				config.Infra = provisioning.Options{Provider: provisioning.Bicep, Path: "infra"}
			},
		},
		{
			name: "top-level infra layers",
			mutate: func(config *ProjectConfig) {
				config.Infra = provisioning.Options{Layers: []provisioning.Options{
					{Name: "shared", Provider: provisioning.Bicep, Path: "infra"},
				}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "azure.yaml")
			projectConfig := &ProjectConfig{
				Name:   "layered-project",
				Layers: LayerConfigs{},
			}
			require.NoError(t, Save(t.Context(), projectConfig, path))

			before, err := os.ReadFile(path)
			require.NoError(t, err)
			test.mutate(projectConfig)

			err = Save(t.Context(), projectConfig, path)
			require.ErrorContains(t, err, "'layers' cannot be combined with top-level 'infra' or 'services'")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestProjectLayersAlphaSchema(t *testing.T) {
	t.Parallel()

	rawSchema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "schemas", "alpha", "azure.yaml.json"))
	require.NoError(t, err)
	var schemaDocument map[string]any
	require.NoError(t, json.Unmarshal(rawSchema, &schemaDocument))

	const resourceURI = "mem://azure.yaml.json"
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource(resourceURI, schemaDocument))
	repositoryRoot := filepath.Join("..", "..", "..", "..")
	jsonGlob := filepath.Join(repositoryRoot, "cli", "azd", "extensions", "*", "schemas", "*.json")
	extensionSchemas, err := filepath.Glob(jsonGlob)
	require.NoError(t, err)
	for _, schemaPath := range extensionSchemas {
		rawExtensionSchema, err := os.ReadFile(schemaPath)
		require.NoError(t, err)
		var extensionSchema map[string]any
		require.NoError(t, json.Unmarshal(rawExtensionSchema, &extensionSchema))
		relativePath, err := filepath.Rel(repositoryRoot, schemaPath)
		require.NoError(t, err)
		require.NoError(t, compiler.AddResource(
			"https://raw.githubusercontent.com/Azure/azure-dev/main/"+filepath.ToSlash(relativePath),
			extensionSchema,
		))
	}
	schema, err := compiler.Compile(resourceURI)
	require.NoError(t, err)

	layer := map[string]any{
		"name": "application",
		"infra": []any{map[string]any{
			"name": "app-infra", "provider": "bicep", "path": "./infra/app",
		}},
		"services": map[string]any{
			"api": map[string]any{"host": "containerapp", "project": "./src/api"},
		},
	}
	require.NoError(t, schema.Validate(map[string]any{
		"name":   "layered-project",
		"layers": []any{layer},
	}))

	for _, incompatibleProperty := range []string{"infra", "services"} {
		projectDocument := map[string]any{
			"name":               "layered-project",
			"layers":             []any{layer},
			incompatibleProperty: map[string]any{},
		}
		require.Error(t, schema.Validate(projectDocument), incompatibleProperty)
	}
}

func TestProjectFormatPreservesExplicitEmptyLayerCollections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config *ProjectConfig
		want   ProjectFormat
	}{
		{name: "flat", config: &ProjectConfig{}, want: ProjectFormatFlat},
		{name: "empty infra layers", config: &ProjectConfig{
			Infra: provisioning.Options{Layers: []provisioning.Options{}},
		}, want: ProjectFormatInfraV1},
		{name: "empty project layers", config: &ProjectConfig{
			Layers: LayerConfigs{},
		}, want: ProjectFormatLayersV2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, test.config.Format())
		})
	}
}

func TestExplicitEmptyInfraLayersUsesV1Format(t *testing.T) {
	t.Parallel()

	config, err := Parse(t.Context(), "name: test-project\ninfra:\n  layers: []\n")
	require.NoError(t, err)
	require.Equal(t, ProjectFormatInfraV1, config.Format())
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

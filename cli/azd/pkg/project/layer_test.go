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
	"github.com/braydonk/yaml"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectConfigCopyRuntimeStateMatchesLayersByName(t *testing.T) {
	t.Parallel()

	dispatcher := ext.NewEventDispatcher[ServiceLifecycleEventArgs]()
	source := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "first-layer", Services: map[string]*ServiceConfig{"api": {EventDispatcher: dispatcher}}},
		{Name: "second-layer", Services: map[string]*ServiceConfig{"worker": {}}},
	}}
	target := &ProjectConfig{Layers: []*LayerConfig{
		{Name: "second-layer", Services: map[string]*ServiceConfig{"worker": {}}},
		{Name: "first-layer", Services: map[string]*ServiceConfig{"api": {}}},
	}}

	source.CopyRuntimeStateTo(target)

	require.Same(t, dispatcher, target.Layers[1].Services["api"].EventDispatcher)
}

func TestParseProjectLayers(t *testing.T) {
	t.Parallel()

	projectConfig, err := Parse(t.Context(), "name: layered-project\n"+
		"layers:\n"+
		"  - name: application-layer\n"+
		"    infra:\n"+
		"      - name: app-infra\n"+
		"        path: ./infra/app\n"+
		"        provider: bicep\n"+
		"    services:\n"+
		"      api:\n"+
		"        project: ./src/api\n"+
		"        host: containerapp\n"+
		"        language: js\n")

	require.NoError(t, err)
	require.Len(t, projectConfig.Layers, 1)
	assert.Equal(t, "application-layer", projectConfig.Layers[0].Name)
	require.Len(t, projectConfig.Layers[0].Infra, 1)
	assert.Equal(t, "app-infra", projectConfig.Layers[0].Infra[0].Name)
	assert.Equal(t, provisioning.Bicep, projectConfig.Layers[0].Infra[0].Provider)
	require.Contains(t, projectConfig.Layers[0].Services, "api")
	assert.Equal(t, "api", projectConfig.Layers[0].Services["api"].Name)
}

func TestParseProjectLayersRejectsInfraDependsOn(t *testing.T) {
	t.Parallel()

	_, err := Parse(t.Context(), `name: layered-project
layers:
  - name: application
    infra:
      - name: api
        provider: bicep
        path: infra/api
        dependsOn: [foundation]
`)

	require.EqualError(t, err,
		`layer "application" infrastructure entry "api" cannot declare dependsOn; `+
			`declare dependencies on the project layer instead`)
}

func TestParseProjectLayersRejectsEmptyEntryNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "infrastructure",
			yaml: `name: layered-project
layers:
  - name: application
    infra:
      - provider: bicep
        path: infra/api
`,
			wantErr: "infrastructure entry name cannot be empty",
		},
		{
			name: "service",
			yaml: `name: layered-project
layers:
  - name: application
    services:
      "":
        host: containerapp
        image: example/api:latest
`,
			wantErr: "service name cannot be empty",
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

func TestValidateLayerDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		layers  LayerConfigs
		wantErr string
	}{
		{
			name: "deep chain",
			layers: LayerConfigs{
				{Name: "foundation-layer"},
				{Name: "application-layer", DependsOn: []string{"foundation-layer"}},
				{Name: "frontend-layer", DependsOn: []string{"application-layer"}},
			},
		},
		{
			name:    "unknown layer",
			layers:  LayerConfigs{{Name: "application-layer", DependsOn: []string{"missing-layer"}}},
			wantErr: `layer "application-layer" depends on unknown layer "missing-layer"`,
		},
		{
			name:    "self dependency",
			layers:  LayerConfigs{{Name: "application-layer", DependsOn: []string{"application-layer"}}},
			wantErr: `layer "application-layer" cannot depend on itself`,
		},
		{
			name: "duplicate dependency",
			layers: LayerConfigs{
				{Name: "foundation-layer"},
				{Name: "application-layer", DependsOn: []string{"foundation-layer", "foundation-layer"}},
			},
			wantErr: `layer "application-layer" depends on layer "foundation-layer" more than once`,
		},
		{
			name: "deep cycle",
			layers: LayerConfigs{
				{Name: "foundation-layer", DependsOn: []string{"frontend-layer"}},
				{Name: "application-layer", DependsOn: []string{"foundation-layer"}},
				{Name: "frontend-layer", DependsOn: []string{"application-layer"}},
			},
			wantErr: `circular dependency detected at layer "foundation-layer"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateLayerDependencies(test.layers)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, test.wantErr)
		})
	}
}

func TestValidateLayerDependencyCycles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		layers  LayerConfigs
		wantErr string
	}{
		{
			name: "no dependencies",
			layers: LayerConfigs{
				{Name: "foundation"},
				{Name: "application"},
			},
		},
		{
			name: "acyclic graph",
			layers: LayerConfigs{
				{Name: "foundation"},
				{Name: "data", DependsOn: []string{"foundation"}},
				{Name: "application", DependsOn: []string{"foundation", "data"}},
			},
		},
		{
			name: "two layer cycle",
			layers: LayerConfigs{
				{Name: "foundation", DependsOn: []string{"application"}},
				{Name: "application", DependsOn: []string{"foundation"}},
			},
			wantErr: `circular dependency detected at layer "foundation"`,
		},
		{
			name: "deep cycle",
			layers: LayerConfigs{
				{Name: "foundation", DependsOn: []string{"frontend"}},
				{Name: "application", DependsOn: []string{"foundation"}},
				{Name: "frontend", DependsOn: []string{"application"}},
			},
			wantErr: `circular dependency detected at layer "foundation"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			//t.Parallel()
			err := validateLayerDependencyCycles(test.layers)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, test.wantErr)
		})
	}
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
				"  - name: application-layer\n"+
				"    services:\n"+
				"      api:\n"+
				"        host: containerapp\n"+
				"        image: example/api:latest\n", test.topLevel)
			_, err := Parse(t.Context(), yaml)

			require.ErrorContains(t, err, "'layers' cannot be combined with top-level 'infra' or 'services'")
		})
	}
}

func TestParseProjectLayersRejectsResources(t *testing.T) {
	t.Parallel()

	_, err := Parse(t.Context(), "name: layered-project\n"+
		"layers:\n"+
		"  - name: application-layer\n"+
		"    services:\n"+
		"      api:\n"+
		"        host: containerapp\n"+
		"        image: example/api:latest\n"+
		"resources:\n"+
		"  storage:\n"+
		"    type: storage\n")

	require.ErrorContains(t, err, "'layers' cannot be combined with top-level 'resources'")
}

func TestParseProjectLayersAllowsEmptyTopLevelInfra(t *testing.T) {
	t.Parallel()

	// This is a really small edge case, but just documenting it here to establish that it was considered
	// and it's not a big enough deal to worry about at this time - we just ignore it and use the layers they've
	// configured.
	_, err := Parse(t.Context(), "name: layered-project\n"+
		"# OH NO - AN EMPTY LITERAL!\n"+
		"infra: {}\n"+
		"layers:\n"+
		"  - name: application-layer\n"+
		"    services:\n"+
		"      api:\n"+
		"        host: containerapp\n"+
		"        image: example/api:latest\n")

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
			yaml:    "name: test-project\nlayers:\n  - name: application-layer\n",
			wantErr: "must contain infrastructure or services",
		},
		{
			name: "duplicate service",
			yaml: "name: test-project\n" +
				"layers:\n" +
				"  - name: first-layer\n" +
				"    services:\n" +
				"      api:\n" +
				"        host: containerapp\n" +
				"        image: example/api:latest\n" +
				"  - name: second-layer\n" +
				"    services:\n" +
				"      api:\n" +
				"        host: containerapp\n" +
				"        image: example/api:latest\n",
			wantErr: "service 'api' is defined in both layers",
		},
		{
			name: "duplicate infrastructure entry",
			yaml: "name: test-project\n" +
				"layers:\n" +
				"  - name: first-layer\n" +
				"    infra:\n" +
				"      - name: shared\n" +
				"        provider: terraform\n" +
				"        path: infra/first\n" +
				"  - name: second-layer\n" +
				"    infra:\n" +
				"      - name: shared\n" +
				"        provider: terraform\n" +
				"        path: infra/second\n",
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

	projectConfig, err := Parse(t.Context(), "name: layered-project\n"+
		"layers:\n"+
		"  - name: application-layer\n"+
		"    infra:\n"+
		"      - name: app-infra\n"+
		"        path: ./infra/app\n"+
		"        provider: bicep\n"+
		"    services:\n"+
		"      api:\n"+
		"        project: ./src/api\n"+
		"        host: containerapp\n"+
		"        language: js\n")
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "azure.yaml")
	require.NoError(t, Save(t.Context(), projectConfig, path))
	contents, err := os.ReadFile(path)
	require.NoError(t, err)

	yaml := string(contents)
	require.Contains(t, yaml, "/schemas/alpha/azure.yaml.json")
	require.Contains(t, yaml, "layers:")
	require.Contains(t, yaml, "- name: application-layer")
	require.Contains(t, yaml, "infra:")
	require.Contains(t, yaml, "- provider: bicep")
	require.Contains(t, yaml, "services:")
	require.Contains(t, yaml, "api:")
	require.NotContains(t, yaml, "layer: application-layer")
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
					{Name: "shared-layer", Provider: provisioning.Bicep, Path: "infra"},
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
		"name": "application-layer",
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
	require.Error(t, schema.Validate(map[string]any{
		"name": "layered-project",
		"layers": []any{map[string]any{
			"name": "application-layer",
			"infra": []any{map[string]any{
				"name":      "app-infra",
				"provider":  "bicep",
				"path":      "./infra/app",
				"dependsOn": []any{"foundation"},
			}},
		}},
	}), "nested infra dependsOn")

	// bicep and terraform require a 'path' attribute
	for _, provider := range []string{"bicep", "terraform"} {
		require.Error(t, schema.Validate(map[string]any{
			"name": "layered-project",
			"layers": []any{map[string]any{
				"name": "application-layer",
				"infra": []any{map[string]any{
					"name":     "app-infra",
					"provider": provider,
				}},
			}},
		}), provider)
	}

	// Custom providers don't. If they need a path, they can define one in their provider-specific config.
	require.NoError(t, schema.Validate(map[string]any{
		"name": "layered-project",
		"layers": []any{map[string]any{
			"name": "application-layer",
			"infra": []any{map[string]any{
				"name":     "foundry",
				"provider": "microsoft.foundry",
			}},
		}},
	}))

	for _, test := range []struct {
		property string
		value    any
	}{
		{property: "infra", value: []any{}},
		{property: "services", value: map[string]any{}},
	} {
		projectDocument := map[string]any{
			"name": "layered-project",
			"layers": []any{map[string]any{
				"name":        "application-layer",
				test.property: test.value,
			}},
		}
		require.Error(t, schema.Validate(projectDocument), test.property)
	}

	for _, incompatibleProperty := range []string{"infra", "resources", "services"} {
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

	projectConfig := map[string]any{
		"name": "test-project",
		"infra": map[string]any{
			"path":   "infra",
			"layers": []any{},
		},
	}
	projectYaml, err := yaml.Marshal(projectConfig)
	require.NoError(t, err)
	require.Contains(t, string(projectYaml), "layers: []")

	config, err := Parse(t.Context(), string(projectYaml))
	require.NoError(t, err)

	// The presence of infra.layers identifies the legacy v1 format, even when the list is empty.
	require.Equal(t, ProjectFormatInfraV1, config.Format())
	require.Empty(t, config.Infra.Layers)

	// A zero-length legacy layer list falls back to the root infra entry.
	entries := config.InfrastructureConfigs()
	require.Len(t, entries, 1)
	require.Equal(t, "infra", entries[0].Path)
}

func TestProjectConfigAccessorsPreserveNonV2Formats(t *testing.T) {
	t.Parallel()

	service := &ServiceConfig{Name: "api"}
	projectConfig := &ProjectConfig{
		Services: map[string]*ServiceConfig{"api": service},
		Infra: provisioning.Options{
			Provider: provisioning.Bicep,
			Layers: []provisioning.Options{
				{Name: "network-layer", Provider: provisioning.Terraform},
				{Name: "application-layer", Provider: provisioning.Bicep},
			},
		},
	}

	require.Equal(t, ProjectFormatInfraV1, projectConfig.Format())
	require.Same(t, service, projectConfig.ServiceConfigs()["api"])
	require.Equal(t, projectConfig.Infra.Layers, projectConfig.InfrastructureConfigs())
}

func TestSaveProjectInfraV1PreservesFormat(t *testing.T) {
	t.Parallel()

	const projectYaml = "name: test-project\n" +
		"infra:\n" +
		"  provider: bicep\n" +
		"  layers:\n" +
		"    - name: network-layer\n" +
		"      path: infra/network\n" +
		"      module: network\n" +
		"    - name: application-layer\n" +
		"      provider: terraform\n" +
		"      path: infra/application\n" +
		"services:\n" +
		"  api:\n" +
		"    host: appservice\n" +
		"    language: python\n" +
		"    project: src/api\n"

	projectConfig, err := Parse(t.Context(), projectYaml)
	require.NoError(t, err)
	require.Equal(t, ProjectFormatInfraV1, projectConfig.Format())

	projectFile := filepath.Join(t.TempDir(), "azure.yaml")
	require.NoError(t, Save(t.Context(), projectConfig, projectFile))

	rawConfig, err := LoadConfig(t.Context(), projectFile)
	require.NoError(t, err)
	require.NoError(t, rawConfig.Set("metadata.compatibilityTest", true))
	require.NoError(t, SaveConfig(t.Context(), rawConfig, projectFile))

	reloaded, err := Load(t.Context(), projectFile)
	require.NoError(t, err)
	require.Equal(t, ProjectFormatInfraV1, reloaded.Format())
	require.Equal(t, provisioning.Bicep, reloaded.Infra.Provider)
	require.Len(t, reloaded.Infra.Layers, 2)
	require.Equal(t, "network-layer", reloaded.Infra.Layers[0].Name)
	require.Equal(t, provisioning.Terraform, reloaded.Infra.Layers[1].Provider)
	require.Contains(t, reloaded.Services, "api")

	contents, err := os.ReadFile(projectFile)
	require.NoError(t, err)
	require.Contains(t, string(contents), "schemas/v1.0/azure.yaml.json")
	require.NotContains(t, string(contents), "\nlayers:")
}

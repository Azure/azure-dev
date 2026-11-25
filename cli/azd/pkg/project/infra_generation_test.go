// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type testInfrastructureGenerator struct {
	generate func(context.Context, *v1beta.InfraGenerateRequest) (*v1beta.InfraGenerateResponse, error)
}

func TestGenerationOutputPathSanitizesDefaultDirectory(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"data-store", "infra/data-store"},
		{"data store", "infra/data store"},
		{`data<>:"/\|?*`, "infra/data_________"},
		{"data\x00\n", "infra/data__"},
		{"data. ", "infra/data"},
		{"../data", "infra/.._data"},
		{"..", "infra/_"},
		{"", "infra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, generationOutputPath(provisioning.Options{Name: tt.name}))
		})
	}

	require.Equal(t, "custom/output", generationOutputPath(provisioning.Options{
		Name: "data?", Path: "custom/output",
	}))
}

func TestGenerationOutputPathCleansExplicitPath(t *testing.T) {
	tests := map[string]string{
		"./infra/data": "infra/data",
		"infra/data/":  "infra/data",
		"infra//data":  "infra/data",
		"../data":      "../data",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, want, generationOutputPath(provisioning.Options{Name: "data", Path: input}))
		})
	}
}

func TestInfraGenerationAcceptsUncleanProjectRelativePath(t *testing.T) {
	source := strings.Replace(generationProject, "      dependsOn: [existing]",
		"      path: ./infra/data/\n      dependsOn: [existing]", 1)
	config := generationTestConfig(t, source)
	generator := &testInfrastructureGenerator{generate: func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		require.Equal(t, "infra/data", request.OutputPath)
		return replacementResponse(), nil
	}}

	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{Force: true})
	require.NoError(t, err)
}

func TestGenerationOutputPathWindowsDeviceNames(t *testing.T) {
	for _, name := range []string{"con", "LPT1.logs"} {
		want := "infra/" + name
		if runtime.GOOS == "windows" {
			want = "infra/_" + name
		}
		require.Equal(t, want, generationOutputPath(provisioning.Options{Name: name}))
	}
}

func (g *testInfrastructureGenerator) Generate(
	ctx context.Context, request *v1beta.InfraGenerateRequest,
) (*v1beta.InfraGenerateResponse, error) {
	return g.generate(ctx, request)
}

func generationTestManager(
	t *testing.T, provider *testInfrastructureGenerator, config *ProjectConfig,
) *InfraGenerationManager {
	t.Helper()
	container := ioc.NewNestedContainer(nil)
	require.NoError(t, container.RegisterNamedTransient(InfraGenerationProviderKey("resources"),
		func() InfrastructureGenerator { return provider }))
	azdCtx, err := azdcontext.NewAzdContextFromWd(config.Path)
	require.NoError(t, err)
	return NewInfraGenerationManager(NewImportManager(nil), container, azdCtx)
}

const generationProject = `# Keep project comment
name: example
infra:
  layers:
    - name: existing
      provider: bicep
      path: infra/existing
    # Keep entry comment
    - name: data
      provider: resources # Keep provider comment
      dependsOn: [existing]
      hooks:
        postprovision:
          run: echo done
      config:
        files:
          type: storage
`

const namedFlatGenerationProject = `name: example
infra:
  name: data
  provider: resources # Keep provider comment
  config:
    type: storage
`

func generationTestConfig(t *testing.T, source string) *ProjectConfig {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte(source), 0o600))
	config, err := Parse(t.Context(), source)
	require.NoError(t, err)
	config.Path = dir
	return config
}

func replacementResponse() *v1beta.InfraGenerateResponse {
	return &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
		Provider: "terraform", Path: "infra/data",
		Config: &structpb.Struct{Fields: map[string]*structpb.Value{
			"backend": structpb.NewStringValue("custom"),
		}},
	}}
}

func TestInfraGenerationAppliesProviderEntryAfterExtensionWrites(t *testing.T) {
	config := generationTestConfig(t, generationProject)
	generator := &testInfrastructureGenerator{generate: func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		require.Equal(t, config.Path, request.ProjectPath)
		require.Equal(t, "data", request.Entry.Name)
		require.Equal(t, "infra/data", request.OutputPath)
		require.True(t, request.Options.Force)
		require.NoError(t, os.MkdirAll(filepath.Join(config.Path, request.OutputPath), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(config.Path, request.OutputPath, "main.tf"), []byte("source"), 0o600))
		return replacementResponse(), nil
	}}
	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{Force: true})
	require.NoError(t, err)
	require.Equal(t, provisioning.ProviderKind("terraform"), config.Infra.Layers[1].Provider)
	require.Equal(t, map[string]any{"backend": "custom"}, config.Infra.Layers[1].Config)
	require.Equal(t, []string{"existing"}, config.Infra.Layers[1].DependsOn)
	require.Contains(t, config.Infra.Layers[1].Hooks, "postprovision")
	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	for _, comment := range []string{"# Keep project comment", "# Keep entry comment", "# Keep provider comment"} {
		require.Contains(t, string(data), comment)
	}
	require.Contains(t, string(data), "path: infra/existing")
	require.NotContains(t, string(data), "type: storage")
	data, err = os.ReadFile(filepath.Join(config.Path, "infra/data/main.tf"))
	require.NoError(t, err)
	require.Equal(t, "source", string(data))
}

func TestInfraGenerationUpdatesAzureYml(t *testing.T) {
	dir := t.TempDir()
	projectFile := filepath.Join(dir, "azure.yml")
	require.NoError(t, os.WriteFile(projectFile, []byte(generationProject), 0o600))

	azdCtx, err := azdcontext.NewAzdContextFromWd(dir)
	require.NoError(t, err)
	config, err := Load(t.Context(), azdCtx.ProjectPath())
	require.NoError(t, err)

	called := false
	generator := &testInfrastructureGenerator{generate: func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		called = true
		require.Equal(t, dir, request.ProjectPath)
		return replacementResponse(), nil
	}}

	err = generationTestManager(t, generator, config).GenerateExtensions(t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, provisioning.Terraform, config.Infra.Layers[1].Provider)
	data, err := os.ReadFile(projectFile)
	require.NoError(t, err)
	saved, err := Parse(t.Context(), string(data))
	require.NoError(t, err)
	require.Equal(t, provisioning.Terraform, saved.Infra.Layers[1].Provider)
	require.Contains(t, string(data), "# Keep entry comment")
	require.NoFileExists(t, filepath.Join(dir, "azure.yaml"))
}

func TestInfraGenerationKeepsResolvedProjectPathWhenAzureYamlAppears(t *testing.T) {
	dir := t.TempDir()
	projectFile := filepath.Join(dir, "azure.yml")
	require.NoError(t, os.WriteFile(projectFile, []byte(generationProject), 0o600))

	azdCtx, err := azdcontext.NewAzdContextFromWd(dir)
	require.NoError(t, err)
	config, err := Load(t.Context(), azdCtx.ProjectPath())
	require.NoError(t, err)

	otherFile := filepath.Join(dir, "azure.yaml")
	const otherContent = "name: separate-project\n"
	generator := &testInfrastructureGenerator{generate: func(
		context.Context, *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		require.NoError(t, os.WriteFile(otherFile, []byte(otherContent), 0o600))
		return replacementResponse(), nil
	}}

	err = generationTestManager(t, generator, config).GenerateExtensions(t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.NoError(t, err)
	data, err := os.ReadFile(projectFile)
	require.NoError(t, err)
	saved, err := Parse(t.Context(), string(data))
	require.NoError(t, err)
	require.Equal(t, provisioning.Terraform, saved.Infra.Layers[1].Provider)
	data, err = os.ReadFile(otherFile)
	require.NoError(t, err)
	require.Equal(t, otherContent, string(data))
}

func TestInfraGenerationRequiresExplicitOptions(t *testing.T) {
	config := generationTestConfig(t, generationProject)
	err := generationTestManager(t, &testInfrastructureGenerator{}, config).GenerateExtensions(t.Context(), config, nil)
	require.ErrorContains(t, err, "non-nil options")
}

func TestInfraGenerationRejectsInvalidReplacement(t *testing.T) {
	tests := []struct {
		name     string
		response *v1beta.InfraGenerateResponse
	}{
		{"nil response", nil},
		{"missing entry", &v1beta.InfraGenerateResponse{}},
		{"empty provider", &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{}}},
		{"renamed entry", &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
			Provider: "bicep", Name: "other",
		}}},
		{"unsafe path", &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
			Provider: "bicep", Path: "../out",
		}}},
		{"unsafe module", &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
			Provider: "bicep", Module: "/out",
		}}},
		{"runtime option", &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
			Provider: "bicep", IgnoreDeploymentState: true,
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := generationTestConfig(t, generationProject)
			generator := &testInfrastructureGenerator{generate: func(
				context.Context, *v1beta.InfraGenerateRequest,
			) (*v1beta.InfraGenerateResponse, error) {
				return tt.response, nil
			}}
			err := generationTestManager(t, generator, config).GenerateExtensions(
				t.Context(), config, &v1beta.InfraGenerateOptions{})
			require.Error(t, err)
			data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, generationProject, string(data))
			require.Equal(t, provisioning.ProviderKind("resources"), config.Infra.Layers[1].Provider)
		})
	}
}

func TestInfraGenerationFailureLeavesConfigurationUnchanged(t *testing.T) {
	config := generationTestConfig(t, generationProject)
	failure := errors.New("extension write failed")
	generator := &testInfrastructureGenerator{generate: func(
		context.Context, *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		return nil, failure
	}}
	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.ErrorIs(t, err, failure)
	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, generationProject, string(data))
}

func TestInfraGenerationRejectsChangedEntry(t *testing.T) {
	config := generationTestConfig(t, generationProject)
	changed := strings.Replace(generationProject, "type: storage", "type: edited", 1)
	generator := &testInfrastructureGenerator{generate: func(
		context.Context, *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		require.NoError(t, os.WriteFile(filepath.Join(config.Path, "azure.yaml"), []byte(changed), 0o600))
		return replacementResponse(), nil
	}}
	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.ErrorContains(t, err, "changed during generation")
	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, changed, string(data))
}

func TestInfraGenerationPreservesUnrelatedEdits(t *testing.T) {
	config := generationTestConfig(t, generationProject)
	generator := &testInfrastructureGenerator{generate: func(
		context.Context, *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		changed := strings.Replace(generationProject, "name: example", "name: edited", 1)
		require.NoError(t, os.WriteFile(filepath.Join(config.Path, "azure.yaml"), []byte(changed), 0o600))
		return replacementResponse(), nil
	}}
	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(data), "name: edited")
}

func TestInfraGenerationCancellationDoesNotReplaceEntry(t *testing.T) {
	config := generationTestConfig(t, generationProject)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	generator := &testInfrastructureGenerator{generate: func(
		context.Context, *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		cancel()
		return replacementResponse(), nil
	}}
	err := generationTestManager(t, generator, config).GenerateExtensions(ctx, config, &v1beta.InfraGenerateOptions{})
	require.ErrorIs(t, err, context.Canceled)
	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, generationProject, string(data))
}

func TestInfraGenerationRequiresAvailableGenerator(t *testing.T) {
	config := generationTestConfig(t, generationProject)
	azdCtx := azdcontext.NewAzdContextWithDirectory(config.Path)
	err := NewInfraGenerationManager(NewImportManager(nil), ioc.NewNestedContainer(nil), azdCtx).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.ErrorContains(t, err, "infra-generation-provider capability")
}

func TestInfraGenerationMethodsRequireMatchingProjectMode(t *testing.T) {
	t.Parallel()

	extensionConfig := generationTestConfig(t, generationProject)
	manager := generationTestManager(t, &testInfrastructureGenerator{}, extensionConfig)
	_, err := manager.GenerateLegacy(t.Context(), extensionConfig)
	require.ErrorContains(t, err, "cannot be used with extension providers")

	legacyConfig := &ProjectConfig{}
	err = manager.GenerateExtensions(t.Context(), legacyConfig, &v1beta.InfraGenerateOptions{})
	require.ErrorContains(t, err, "requires at least one extension provider")
}

func TestInfraGenerationSupportsFlatAndProjectLayerEntries(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{"flat", "name: example\ninfra:\n  provider: resources\n"},
		{"project layer", "name: example\nlayers:\n  - name: platform\n    infra:\n" +
			"      - name: data\n        provider: resources\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := generationTestConfig(t, tt.source)
			generator := &testInfrastructureGenerator{generate: func(
				context.Context, *v1beta.InfraGenerateRequest,
			) (*v1beta.InfraGenerateResponse, error) {
				return replacementResponse(), nil
			}}
			err := generationTestManager(t, generator, config).GenerateExtensions(
				t.Context(), config, &v1beta.InfraGenerateOptions{})
			require.NoError(t, err)
			require.Equal(t, provisioning.Terraform, config.InfrastructureConfigs()[0].Provider)
			data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
			require.NoError(t, err)
			saved, err := Parse(t.Context(), string(data))
			require.NoError(t, err)
			require.Equal(t, provisioning.Terraform, saved.InfrastructureConfigs()[0].Provider)
		})
	}
}

func TestInfraGenerationSupportsNamedFlatEntry(t *testing.T) {
	config := generationTestConfig(t, namedFlatGenerationProject)
	called := false
	generator := &testInfrastructureGenerator{generate: func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		called = true
		require.Equal(t, "data", request.Entry.Name)
		require.Equal(t, "infra/data", request.OutputPath)
		require.NotNil(t, request.Entry.Config)
		require.Equal(t, map[string]any{"type": "storage"}, request.Entry.Config.AsMap())
		return replacementResponse(), nil
	}}

	infraGenerationManager := generationTestManager(t, generator, config)

	err := infraGenerationManager.GenerateExtensions(t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, "data", config.Infra.Name)
	require.Equal(t, provisioning.Terraform, config.Infra.Provider)
	require.Equal(t, filepath.Join("infra", "data"), config.Infra.Path)
	require.Equal(t, map[string]any{"backend": "custom"}, config.Infra.Config)

	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(data), "# Keep provider comment")
	saved, err := Parse(t.Context(), string(data))
	require.NoError(t, err)
	require.Equal(t, config.Infra, saved.Infra)
}

func TestInfraGenerationSupportsUnnamedFlatEntry(t *testing.T) {
	const source = `name: example
infra:
  provider: resources # Keep provider comment
  config:
    files:
      type: storage
      containers: [uploads]
`
	config := generationTestConfig(t, source)
	called := false
	generator := &testInfrastructureGenerator{generate: func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		called = true
		require.Empty(t, request.Entry.Name)
		require.Equal(t, "infra", request.OutputPath)
		require.NotNil(t, request.Entry.Config)
		require.Equal(t, map[string]any{
			"files": map[string]any{"type": "storage", "containers": []any{"uploads"}},
		}, request.Entry.Config.AsMap())
		return &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
			Provider: "bicep", Path: request.OutputPath, Module: "main",
		}}, nil
	}}

	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.NoError(t, err)
	require.True(t, called)
	require.Empty(t, config.Infra.Name)
	require.Equal(t, provisioning.Bicep, config.Infra.Provider)
	require.Equal(t, "infra", config.Infra.Path)
	require.Equal(t, "main", config.Infra.Module)
	require.Nil(t, config.Infra.Config)

	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(data), "# Keep provider comment")
	saved, err := Parse(t.Context(), string(data))
	require.NoError(t, err)
	require.Equal(t, ProjectFormatFlat, saved.Format())
	require.Equal(t, config.Infra, saved.Infra)
}

func TestInfraGenerationRejectsChangedNamedFlatEntry(t *testing.T) {
	config := generationTestConfig(t, namedFlatGenerationProject)
	changed := strings.Replace(namedFlatGenerationProject, "type: storage", "type: queue", 1)
	generator := &testInfrastructureGenerator{generate: func(
		context.Context, *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		require.NoError(t, os.WriteFile(filepath.Join(config.Path, "azure.yaml"), []byte(changed), 0o600))
		return replacementResponse(), nil
	}}

	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.ErrorContains(t, err, "changed during generation")
	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, changed, string(data))
	require.Equal(t, provisioning.ProviderKind("resources"), config.Infra.Provider)
}

func TestInfraGenerationSupportsRootEntryWithEmptyInfraLayers(t *testing.T) {
	const source = `name: example
infra:
  name: data
  provider: resources
  layers: []
`
	config := generationTestConfig(t, source)
	require.Equal(t, ProjectFormatInfraV1, config.Format())
	generator := &testInfrastructureGenerator{generate: func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		require.Equal(t, "data", request.Entry.Name)
		return replacementResponse(), nil
	}}

	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.NoError(t, err)
	require.Equal(t, "data", config.Infra.Name)
	require.Equal(t, provisioning.Terraform, config.Infra.Provider)
	require.NotNil(t, config.Infra.Layers)
	require.Empty(t, config.Infra.Layers)

	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	saved, err := Parse(t.Context(), string(data))
	require.NoError(t, err)
	require.Equal(t, ProjectFormatInfraV1, saved.Format())
	require.Equal(t, config.Infra, saved.Infra)
}

func TestInfraGenerationRejectsLayerMovedToTopLevel(t *testing.T) {
	const source = `name: example
infra:
  layers:
    - name: data
      provider: resources
      config:
        type: storage
`
	const moved = `name: example
infra:
  name: data
  provider: resources
  config:
    type: storage
`
	for _, tt := range []struct {
		name             string
		duringGeneration bool
	}{
		{name: "before generation"},
		{name: "during generation", duringGeneration: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := generationTestConfig(t, source)
			projectFile := filepath.Join(config.Path, "azure.yaml")
			if !tt.duringGeneration {
				require.NoError(t, os.WriteFile(projectFile, []byte(moved), 0o600))
			}

			called := false
			generator := &testInfrastructureGenerator{generate: func(
				context.Context, *v1beta.InfraGenerateRequest,
			) (*v1beta.InfraGenerateResponse, error) {
				called = true
				if tt.duringGeneration {
					require.NoError(t, os.WriteFile(projectFile, []byte(moved), 0o600))
				}
				return replacementResponse(), nil
			}}

			err := generationTestManager(t, generator, config).GenerateExtensions(
				t.Context(), config, &v1beta.InfraGenerateOptions{})
			require.Equal(t, tt.duringGeneration, called)
			require.Error(t, err)
			data, err := os.ReadFile(projectFile)
			require.NoError(t, err)
			require.Equal(t, moved, string(data))
			require.Equal(t, provisioning.ProviderKind("resources"), config.Infra.Layers[0].Provider)
		})
	}
}

func TestInfraGenerationRejectsMissingProjectLayerBeforeGeneration(t *testing.T) {
	const source = `name: example
layers:
  - name: platform
    infra:
      - name: data
        provider: resources
        config:
          type: storage
`
	const changed = `name: example
layers: []
infra:
  layers:
    - name: data
      provider: resources
      config:
        type: storage
`
	config := generationTestConfig(t, source)
	projectFile := filepath.Join(config.Path, "azure.yaml")
	require.NoError(t, os.WriteFile(projectFile, []byte(changed), 0o600))
	called := false
	generator := &testInfrastructureGenerator{generate: func(
		context.Context, *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		called = true
		return replacementResponse(), nil
	}}

	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.ErrorContains(t, err, `project layer "platform" was not found`)
	require.False(t, called)
	data, err := os.ReadFile(projectFile)
	require.NoError(t, err)
	require.Equal(t, changed, string(data))
}

func TestInfraGenerationDoesNotSavePartialReplacements(t *testing.T) {
	source := generationProject + "    - name: second\n      provider: resources\n"
	config := generationTestConfig(t, source)
	failure := errors.New("second generation failed")
	calls := 0
	generator := &testInfrastructureGenerator{generate: func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		calls++
		if request.Entry.Name == "second" {
			return nil, failure
		}
		return replacementResponse(), nil
	}}
	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.ErrorIs(t, err, failure)
	require.Equal(t, 2, calls)
	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, source, string(data))
	require.Equal(t, provisioning.ProviderKind("resources"), config.Infra.Layers[1].Provider)
}

func TestInfraGenerationValidatesAllInputsBeforeInvokingExtensions(t *testing.T) {
	source := generationProject + "    - name: second\n      provider: resources\n      path: ../outside\n"
	config := generationTestConfig(t, source)
	generator := &testInfrastructureGenerator{generate: func(
		context.Context, *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		t.Fatal("invalid input must be rejected before invoking any generator")
		return nil, nil
	}}
	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.ErrorContains(t, err, "project-relative")
}

func TestInfraGenerationRejectsDuplicateOutputPaths(t *testing.T) {
	tests := []struct {
		name   string
		source string
		path   string
		first  string
		second string
	}{
		{
			name: "explicit paths",
			source: "name: example\ninfra:\n  layers:\n" +
				"    - name: first\n      provider: resources\n      path: infra/shared\n" +
				"    - name: second\n      provider: resources\n      path: infra/shared\n",
			path: "infra/shared", first: "first", second: "second",
		},
		{
			name: "normalized explicit paths",
			source: "name: example\ninfra:\n  layers:\n" +
				"    - name: first\n      provider: resources\n      path: ./infra/shared/\n" +
				"    - name: second\n      provider: resources\n      path: infra/shared\n",
			path: "infra/shared", first: "first", second: "second",
		},
		{
			name: "sanitized names",
			source: "name: example\ninfra:\n  layers:\n" +
				"    - name: 'data?'\n      provider: resources\n" +
				"    - name: 'data*'\n      provider: resources\n",
			path: "infra/data_", first: "data?", second: "data*",
		},
		{
			name: "explicit and default paths",
			source: "name: example\ninfra:\n  layers:\n" +
				"    - name: data\n      provider: resources\n" +
				"    - name: other\n      provider: resources\n      path: infra/data\n",
			path: "infra/data", first: "data", second: "other",
		},
		{
			name: "project layers",
			source: "name: example\nlayers:\n" +
				"  - name: first-layer\n    infra:\n" +
				"      - name: first\n        provider: resources\n        path: infra/shared\n" +
				"  - name: second-layer\n    infra:\n" +
				"      - name: second\n        provider: resources\n        path: infra/shared\n",
			path: "infra/shared", first: "first", second: "second",
		},
	}

	for _, tt := range tests {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force=%t", tt.name, force), func(t *testing.T) {
				config := generationTestConfig(t, tt.source)
				require.NoError(t, config.Validate())
				calls := 0
				generator := &testInfrastructureGenerator{generate: func(
					_ context.Context, request *v1beta.InfraGenerateRequest,
				) (*v1beta.InfraGenerateResponse, error) {
					calls++
					response := replacementResponse()
					response.Entry.Path = request.OutputPath
					return response, nil
				}}

				err := generationTestManager(t, generator, config).GenerateExtensions(
					t.Context(), config, &v1beta.InfraGenerateOptions{Force: force})
				require.ErrorContains(t, err, tt.path)
				require.ErrorContains(t, err, tt.first)
				require.ErrorContains(t, err, tt.second)
				require.Zero(t, calls)
				data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
				require.NoError(t, err)
				require.Equal(t, tt.source, string(data))
			})
		}
	}
}

func TestInfraGenerationRejectsDanglingConfigurationAliases(t *testing.T) {
	source := strings.Replace(generationProject, "config:", "config: &resource_config", 1) +
		"metadata:\n  oldConfig: *resource_config\n"
	config := generationTestConfig(t, source)
	generator := &testInfrastructureGenerator{generate: func(
		context.Context, *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		return replacementResponse(), nil
	}}
	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.ErrorContains(t, err, "validating generated project")
	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, source, string(data))
}

func TestInfraGenerationRejectsStaleLoadedEntryBeforeWriting(t *testing.T) {
	config := generationTestConfig(t, generationProject)
	changed := strings.Replace(generationProject, "type: storage", "type: edited", 1)
	require.NoError(t, os.WriteFile(filepath.Join(config.Path, "azure.yaml"), []byte(changed), 0o600))
	generator := &testInfrastructureGenerator{generate: func(
		context.Context, *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		t.Fatal("stale entries must be rejected before invoking the generator")
		return nil, nil
	}}
	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.ErrorContains(t, err, "changed since the project was loaded")
}

func TestInfraGenerationSavesAllReplacementEntries(t *testing.T) {
	source := generationProject + "    - name: second\n      provider: resources\n"
	config := generationTestConfig(t, source)
	generator := &testInfrastructureGenerator{generate: func(
		_ context.Context, request *v1beta.InfraGenerateRequest,
	) (*v1beta.InfraGenerateResponse, error) {
		response := replacementResponse()
		response.Entry.Path = request.OutputPath
		return response, nil
	}}
	err := generationTestManager(t, generator, config).GenerateExtensions(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.NoError(t, err)
	for _, entry := range config.Infra.Layers[1:] {
		require.Equal(t, provisioning.Terraform, entry.Provider)
		require.Equal(t, "infra/"+entry.Name, entry.Path)
	}
}

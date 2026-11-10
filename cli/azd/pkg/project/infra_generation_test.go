// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type testInfrastructureGenerator struct {
	generate func(context.Context, *v1beta.InfraGenerateRequest) (*v1beta.InfraGenerateResponse, error)
}

func (g *testInfrastructureGenerator) Generate(
	ctx context.Context, request *v1beta.InfraGenerateRequest,
) (*v1beta.InfraGenerateResponse, error) {
	return g.generate(ctx, request)
}

func generationTestManager(t *testing.T, provider *testInfrastructureGenerator) *InfraGenerationManager {
	t.Helper()
	container := ioc.NewNestedContainer(nil)
	require.NoError(t, container.RegisterNamedTransient(InfraGenerationProviderKey("resources"),
		func() InfrastructureGenerator { return provider }))
	return NewInfraGenerationManager(NewImportManager(nil), container)
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
	result, err := generationTestManager(t, generator).Generate(
		t.Context(), config, &v1beta.InfraGenerateOptions{Force: true})
	require.NoError(t, err)
	require.Nil(t, result.Files, "core must not write extension files")
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

func TestInfraGenerationRequiresExplicitOptions(t *testing.T) {
	config := generationTestConfig(t, generationProject)
	_, err := generationTestManager(t, &testInfrastructureGenerator{}).Generate(t.Context(), config, nil)
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
			_, err := generationTestManager(t, generator).Generate(t.Context(), config, &v1beta.InfraGenerateOptions{})
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
	_, err := generationTestManager(t, generator).Generate(t.Context(), config, &v1beta.InfraGenerateOptions{})
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
	_, err := generationTestManager(t, generator).Generate(t.Context(), config, &v1beta.InfraGenerateOptions{})
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
	_, err := generationTestManager(t, generator).Generate(t.Context(), config, &v1beta.InfraGenerateOptions{})
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
	_, err := generationTestManager(t, generator).Generate(ctx, config, &v1beta.InfraGenerateOptions{})
	require.ErrorIs(t, err, context.Canceled)
	data, err := os.ReadFile(filepath.Join(config.Path, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, generationProject, string(data))
}

func TestInfraGenerationRequiresAvailableGenerator(t *testing.T) {
	config := generationTestConfig(t, generationProject)
	_, err := NewInfraGenerationManager(NewImportManager(nil), ioc.NewNestedContainer(nil)).Generate(
		t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.ErrorContains(t, err, "infra-generation-provider capability")
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
			_, err := generationTestManager(t, generator).Generate(t.Context(), config, &v1beta.InfraGenerateOptions{})
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
	_, err := generationTestManager(t, generator).Generate(t.Context(), config, &v1beta.InfraGenerateOptions{})
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
	_, err := generationTestManager(t, generator).Generate(t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.ErrorContains(t, err, "project-relative")
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
	_, err := generationTestManager(t, generator).Generate(t.Context(), config, &v1beta.InfraGenerateOptions{})
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
	_, err := generationTestManager(t, generator).Generate(t.Context(), config, &v1beta.InfraGenerateOptions{})
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
	_, err := generationTestManager(t, generator).Generate(t.Context(), config, &v1beta.InfraGenerateOptions{})
	require.NoError(t, err)
	for _, entry := range config.Infra.Layers[1:] {
		require.Equal(t, provisioning.Terraform, entry.Provider)
		require.Equal(t, "infra/"+entry.Name, entry.Path)
	}
}

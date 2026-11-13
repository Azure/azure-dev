// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azure.resources/internal/resources"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func generationRequest(t *testing.T) *v1beta.InfraGenerateRequest {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte("name: unchanged\n"), 0o600))
	config, err := structpb.NewStruct(map[string]any{
		"files": map[string]any{"type": "storage", "containers": []any{"uploads"}},
	})
	require.NoError(t, err)
	return &v1beta.InfraGenerateRequest{
		ProjectPath: dir, OutputPath: "infra/data", Options: &v1beta.InfraGenerateOptions{},
		Entry: &v1beta.ProvisioningOptions{Provider: "resources", Name: "data", Config: config},
	}
}

func TestGenerationWritesSourcesAndReturnsEntryWithoutMutatingProject(t *testing.T) {
	request := generationRequest(t)
	result, err := NewGenerationProvider().Generate(t.Context(), request)
	require.NoError(t, err)
	expected, err := resources.Generate("data", resources.Config{
		"files": {Type: resources.Storage, StorageContainers: []string{"uploads"}},
	})
	require.NoError(t, err)
	root, err := os.OpenRoot(request.ProjectPath)
	require.NoError(t, err)
	defer root.Close()
	for name, contents := range expected {
		actual, err := root.ReadFile(filepath.Join(request.OutputPath, name))
		require.NoError(t, err)
		require.Equal(t, contents, actual)
	}
	require.Equal(t, "bicep", result.Entry.Provider)
	require.Equal(t, request.OutputPath, result.Entry.Path)
	require.Equal(t, "main", result.Entry.Module)
	require.Equal(t, "data", result.Entry.Name)
	actual, err := os.ReadFile(filepath.Join(request.ProjectPath, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, "name: unchanged\n", string(actual))
	require.NoDirExists(t, filepath.Join(request.ProjectPath, ".azure"))
}

func TestGenerationExportsAIProjectLocationMapping(t *testing.T) {
	// Empty want values mean the parameter should be omitted.
	tests := []struct {
		name         string
		entryName    string
		resourceType string
		existing     bool
		want         string
	}{
		{
			name:         "unnamed",
			entryName:    "",
			resourceType: "ai.project",
			want:         "${AZURE_RESOURCES___AI_DEPLOYMENTS_LOCATION}",
		},
		{
			name:         "named",
			entryName:    "models",
			resourceType: "ai.project",
			want:         "${AZURE_RESOURCES_MODELS__AI_DEPLOYMENTS_LOCATION}",
		},
		{
			name:         "hyphens",
			entryName:    "ai--west-",
			resourceType: "ai.project",
			want:         "${AZURE_RESOURCES_AI__WEST___AI_DEPLOYMENTS_LOCATION}",
		},
		{
			name:         "existing project",
			entryName:    "models",
			resourceType: "ai.project",
			existing:     true,
		},
		{
			name:         "no project",
			entryName:    "models",
			resourceType: "storage",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := generationRequest(t)
			// For example, an entry named "models" maps AZURE_AI_DEPLOYMENTS_LOCATION to AZURE_RESOURCES_MODELS__AI_DEPLOYMENTS_LOCATION.
			request.Entry.Name = tt.entryName

			resource := map[string]any{"type": tt.resourceType}
			if tt.existing {
				resource["existing"] = true
			}
			var err error
			request.Entry.Config, err = structpb.NewStruct(map[string]any{"resource": resource})
			require.NoError(t, err)

			_, err = NewGenerationProvider().Generate(t.Context(), request)
			require.NoError(t, err)

			data, err := os.ReadFile(filepath.Join(request.ProjectPath, request.OutputPath, "main.parameters.json"))
			require.NoError(t, err)

			var file struct {
				Parameters map[string]struct {
					Value any `json:"value"`
				} `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(data, &file))

			if tt.want == "" {
				require.NotContains(t, file.Parameters, "aiDeploymentsLocation")
			} else {
				require.Contains(t, file.Parameters, "aiDeploymentsLocation")
				require.Equal(t, tt.want, file.Parameters["aiDeploymentsLocation"].Value)
			}
		})
	}
}

func TestGenerationRefusesExistingFilesWithoutForce(t *testing.T) {
	request := generationRequest(t)
	target := filepath.Join(request.ProjectPath, request.OutputPath, "main.bicep")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o700))
	require.NoError(t, os.WriteFile(target, []byte("existing"), 0o600))
	response, err := NewGenerationProvider().Generate(t.Context(), request)
	require.ErrorContains(t, err, "--force")
	require.Nil(t, response)
	root, err := os.OpenRoot(request.ProjectPath)
	require.NoError(t, err)
	defer root.Close()
	actual, err := root.ReadFile(filepath.Join(request.OutputPath, "main.bicep"))
	require.NoError(t, err)
	require.Equal(t, "existing", string(actual))
}

func TestGenerationForceOverwritesExistingFiles(t *testing.T) {
	request := generationRequest(t)
	target := filepath.Join(request.ProjectPath, request.OutputPath, "main.bicep")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o700))
	require.NoError(t, os.WriteFile(target, []byte("existing"), 0o600))
	request.Options.Force = true
	_, err := NewGenerationProvider().Generate(t.Context(), request)
	require.NoError(t, err)
	root, err := os.OpenRoot(request.ProjectPath)
	require.NoError(t, err)
	defer root.Close()
	actual, err := root.ReadFile(filepath.Join(request.OutputPath, "main.bicep"))
	require.NoError(t, err)
	require.NotEqual(t, "existing", string(actual))
}

func TestGenerationRejectsInvalidInputsBeforeWriting(t *testing.T) {
	tests := []struct {
		name   string
		change func(*v1beta.InfraGenerateRequest)
	}{
		{"missing options", func(r *v1beta.InfraGenerateRequest) { r.Options = nil }},
		{"missing config", func(r *v1beta.InfraGenerateRequest) { r.Entry.Config = nil }},
		{"wrong provider", func(r *v1beta.InfraGenerateRequest) { r.Entry.Provider = "other" }},
		{"parent path", func(r *v1beta.InfraGenerateRequest) { r.OutputPath = "../outside" }},
		{"absolute path", func(r *v1beta.InfraGenerateRequest) { r.OutputPath = "/outside" }},
		{"backslash path", func(r *v1beta.InfraGenerateRequest) { r.OutputPath = `infra\outside` }},
		{"invalid name", func(r *v1beta.InfraGenerateRequest) { r.Entry.Name = "../outside" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := generationRequest(t)
			tt.change(request)
			response, err := NewGenerationProvider().Generate(t.Context(), request)
			require.Error(t, err)
			require.Nil(t, response)
			require.NoDirExists(t, filepath.Join(request.ProjectPath, "infra"))
		})
	}
}

func TestGenerationRejectsSymlinkEscape(t *testing.T) {
	request := generationRequest(t)
	outside := t.TempDir()
	ostest.DirectoryLink(t, outside, filepath.Join(request.ProjectPath, "infra"))
	request.Options.Force = true
	response, err := NewGenerationProvider().Generate(t.Context(), request)
	require.Error(t, err)
	require.Nil(t, response)
	require.NoDirExists(t, filepath.Join(outside, "data"))
}

func TestGenerationReportsWriteFailure(t *testing.T) {
	request := generationRequest(t)
	require.NoError(t, os.WriteFile(filepath.Join(request.ProjectPath, "infra"), []byte("not a directory"), 0o600))
	response, err := NewGenerationProvider().Generate(t.Context(), request)
	require.Error(t, err)
	require.Nil(t, response)
}

func TestGenerationCancellationDoesNotWrite(t *testing.T) {
	request := generationRequest(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	response, err := NewGenerationProvider().Generate(ctx, request)
	require.True(t, errors.Is(err, context.Canceled))
	require.Nil(t, response)
	require.NoDirExists(t, filepath.Join(request.ProjectPath, "infra"))
}

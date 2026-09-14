// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func legacyDeploymentRequest(
	t *testing.T, path string, environment map[string]string,
) (*agent_api.CreateAgentRequest, map[string]string) {
	t.Helper()
	service := &azdext.ServiceConfig{Name: "research-agent", RelativePath: ".", Environment: environment}
	definition, hosted, _, err := LoadAgentDefinition(service, filepath.Dir(path))
	require.NoError(t, err)
	require.True(t, hosted)
	if definition.CodeConfiguration == nil {
		definition.CodeConfiguration = &agent_yaml.CodeConfiguration{Runtime: "python_3_13", EntryPoint: "main.py"}
	}
	prepared, err := prepareDeployRequest(service, definition,
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://example.com"}, nil)
	require.NoError(t, err)
	hostedDefinition, ok := prepared.request.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	return prepared.request, hostedDefinition.EnvironmentVariables
}

func TestPreviewStandaloneHostedAgentTagsAndCPU(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "agent.yaml")
	const original = `kind: hosted
name: research-agent
metadata:
  tags:
    - Streaming
resources:
  cpu: "0.25"
  memory: "0.5Gi"
`
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "agent.manifest.yaml"),
		[]byte("metadata:\n  tags:\n    - ManifestOnly\n"), 0o600))
	beforeRequest, _ := legacyDeploymentRequest(t, path, nil)
	reader := &previewAgentReader{result: deployedPreviewAgent(t, beforeRequest)}
	updated := strings.ReplaceAll(original, `"0.25"`, `"0.5"`)
	updated = strings.ReplaceAll(updated, "    - Streaming", "    - Streaming\n    - Test")
	require.NoError(t, os.WriteFile(path, []byte(updated), 0o600))
	options := DirectDeployOptions{DefinitionPath: path, ProjectEndpoint: "https://example.com"}

	result, err := previewStandaloneHostedAgent(t.Context(), options, nil,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	require.Equal(t, []DeployPreviewChangeGroup{
		{Group: "metadata", Changes: []DeployPreviewChange{
			{Field: "metadata.tags", Kind: "modify", Before: []any{"Streaming"}, After: []any{"Streaming", "Test"}},
		}},
		{Group: "resources", Changes: []DeployPreviewChange{
			{Field: "cpu", Kind: "modify", Before: "0.25", After: "0.5"},
		}},
	}, result.Changes)
	wire, err := json.Marshal(result)
	require.NoError(t, err)
	assert.Contains(t, string(wire), "ManifestOnly", "conflicting companion metadata must be compared, not ignored")
	require.Len(t, result.SourceConflicts, 1)

	deployRequest, _ := legacyDeploymentRequest(t, path, nil)
	assert.Equal(t, `["Streaming","Test"]`, deployRequest.Metadata["tags"])
	reader.result = deployedPreviewAgent(t, deployRequest)
	result, err = previewStandaloneHostedAgent(t.Context(), options, nil,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.False(t, result.HasChanges, "overridden manifest values do not change the deployed configuration")
	assert.Empty(t, result.Changes)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, updated, string(after), "preview must not rewrite the local definition")
}

func TestPreviewAgentServiceMetadataTags(t *testing.T) {
	t.Parallel()
	options := agentServicePreviewFixture(t)
	metadata, err := structpb.NewStruct(map[string]any{"tags": []any{"Streaming"}})
	require.NoError(t, err)
	options.Service.AdditionalProperties.Fields["metadata"] = structpb.NewStructValue(metadata)
	definition, _, _, err := LoadAgentDefinition(options.Service, options.ProjectRoot)
	require.NoError(t, err)
	env := map[string]string{"FOUNDRY_PROJECT_ENDPOINT": options.ProjectEndpoint}
	prepared, err := prepareDeployRequest(options.Service, definition, env, nil)
	require.NoError(t, err)
	reader := &previewAgentReader{result: deployedPreviewAgent(t, prepared.request)}
	metadata.Fields["tags"], err = structpb.NewValue([]any{"Streaming", "Test"})
	require.NoError(t, err)
	original := proto.CloneOf(options.Service)

	result, err := previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.Equal(t, []DeployPreviewChangeGroup{{
		Group: "metadata", Changes: []DeployPreviewChange{
			{Field: "metadata.tags", Kind: "modify", Before: []any{"Streaming"}, After: []any{"Streaming", "Test"}},
		},
	}}, result.Changes)
	assert.True(t, proto.Equal(original, options.Service), "preview must not mutate inline metadata")
	definition, _, _, err = LoadAgentDefinition(options.Service, options.ProjectRoot)
	require.NoError(t, err)
	prepared, err = prepareDeployRequest(options.Service, definition, env, nil)
	require.NoError(t, err)
	assert.Equal(t, `["Streaming","Test"]`, prepared.request.Metadata["tags"])

	reader.result = deployedPreviewAgent(t, prepared.request)
	result, err = previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.False(t, result.HasChanges)
}

func TestCompareAgentDeploymentTagChanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		before string
		after  string
		change *DeployPreviewChange
	}{
		{name: "add first tag", after: `["Test"]`,
			change: &DeployPreviewChange{Field: "metadata.tags", Kind: "add", After: []any{"Test"}}},
		{name: "add tag", before: `["Streaming"]`, after: `["Streaming","Test"]`,
			change: &DeployPreviewChange{Field: "metadata.tags", Kind: "modify",
				Before: []any{"Streaming"}, After: []any{"Streaming", "Test"}}},
		{name: "remove tag", before: `["Streaming","Test"]`, after: `["Streaming"]`,
			change: &DeployPreviewChange{Field: "metadata.tags", Kind: "modify",
				Before: []any{"Streaming", "Test"}, After: []any{"Streaming"}}},
		{name: "remove all tags", before: `["Streaming"]`,
			change: &DeployPreviewChange{Field: "metadata.tags", Kind: "remove", Before: []any{"Streaming"}}},
		{name: "empty list is no tags", before: `[]`},
		{name: "clear with empty list", before: `["Streaming"]`, after: `[]`,
			change: &DeployPreviewChange{Field: "metadata.tags", Kind: "remove", Before: []any{"Streaming"}}},
		{name: "reorder tags", before: `["Test","Streaming"]`, after: `["Streaming","Test"]`},
		{name: "duplicate tags", before: `["Test","Streaming","Test"]`, after: `["Streaming","Test"]`},
		{name: "unchanged legacy scalar", before: "Streaming,Test", after: "Streaming,Test"},
		{name: "changed legacy scalar", before: "Streaming", after: "Test",
			change: &DeployPreviewChange{Field: "metadata.tags", Kind: "modify", Before: "Streaming", After: "Test"}},
		{name: "comma within a tag is not a separator", before: `["A,B"]`, after: `["A","B"]`,
			change: &DeployPreviewChange{Field: "metadata.tags", Kind: "modify",
				Before: []any{"A,B"}, After: []any{"A", "B"}}},
		{name: "case sensitive", before: `["test"]`, after: `["Test"]`,
			change: &DeployPreviewChange{Field: "metadata.tags", Kind: "modify", Before: []any{"test"}, After: []any{"Test"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := deploymentPreviewRequest()
			if tt.before != "" {
				request.Metadata["tags"] = tt.before
			}
			existing := deployedPreviewAgent(t, request)
			delete(request.Metadata, "tags")
			if tt.after != "" {
				request.Metadata["tags"] = tt.after
			}
			result, err := compareAgentDeployment(request, existing, nil)
			require.NoError(t, err)
			if tt.change == nil {
				assert.False(t, result.HasChanges)
				assert.Empty(t, result.Changes)
				return
			}
			assert.True(t, result.HasChanges)
			assert.Equal(t, []DeployPreviewChangeGroup{{
				Group: "metadata", Changes: []DeployPreviewChange{*tt.change},
			}}, result.Changes)
		})
	}
}

func TestCompareAgentDeploymentTagsRedactURLs(t *testing.T) {
	t.Parallel()
	request := deploymentPreviewRequest()
	existing := deployedPreviewAgent(t, request)
	// #nosec G101 -- Synthetic URL credentials verify tag-value redaction.
	request.Metadata["tags"] = `["https://tag-user:tag-password@example.com/path?sig=tag-token#tag-fragment"]`
	result, err := compareAgentDeployment(request, existing, nil)
	require.NoError(t, err)
	assert.Equal(t, []DeployPreviewChangeGroup{{
		Group: "metadata", Changes: []DeployPreviewChange{{
			Field: "metadata.tags", Kind: "add", After: []any{"https://example.com/path"},
		}},
	}}, result.Changes)
	wire, err := json.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{"tag-user", "tag-password", "tag-token", "tag-fragment"} {
		assert.NotContains(t, string(wire), secret)
	}
}

func TestCompareAgentDeploymentContainerImageChange(t *testing.T) {
	t.Parallel()
	request := deploymentPreviewRequest()
	definition, ok := request.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	definition.CodeConfiguration = nil
	definition.ContainerConfiguration = &agent_api.ContainerConfigurationAPI{Image: "registry.example.com/agent:v1"}
	request.Definition = definition
	existing := deployedPreviewAgent(t, request)
	definition.ContainerConfiguration.Image = "registry.example.com/agent:v2"
	request.Definition = definition
	result, err := compareAgentDeployment(request, existing, nil)
	require.NoError(t, err)
	assert.Equal(t, []DeployPreviewChangeGroup{{
		Group: "containerImage", Changes: []DeployPreviewChange{{
			Field: "image", Kind: "modify", Before: "registry.example.com/agent:v1", After: "registry.example.com/agent:v2",
		}},
	}}, result.Changes)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestPreviewEffectiveEnvironmentAndTagChanges(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := writePreviewYAML(t, directory, "agent.yaml", map[string]any{
		"kind": "hosted", "name": "research-agent",
		"metadata": map[string]any{"tags": []any{"Test"}},
		"environment_variables": []any{
			map[string]any{"name": "KEEP", "value": "same"},
			map[string]any{"name": "CHANGE", "value": "selected-value"},
		},
	})
	writePreviewYAML(t, directory, "agent.manifest.yaml", map[string]any{
		"name": "research-agent", "metadata": map[string]any{"tags": []any{"Retired", "Streaming"}},
		"template": map[string]any{"kind": "hosted", "environment_variables": []any{
			map[string]any{"name": "KEEP", "value": "same"},
			map[string]any{"name": "CHANGE", "value": "lower-priority-value"},
			map[string]any{"name": "ANY_THING", "value": "manifest-only-value"},
		}},
	})
	request, environment := legacyDeploymentRequest(t, path, nil)
	assert.Equal(t, map[string]string{
		"KEEP": "same", "CHANGE": "selected-value", "ANY_THING": "manifest-only-value",
	}, environment, "real deployment must use the same named-variable merge as preview")
	assert.Equal(t, `["Test"]`, request.Metadata["tags"], "tag lists replace lower-priority lists")

	remote := deployedPreviewAgent(t, request)
	remote.Versions.Latest.Metadata["tags"] = `["Retired","Streaming"]`
	remoteDefinition, ok := remote.Versions.Latest.Definition.(map[string]any)
	require.True(t, ok)
	remoteDefinition["environment_variables"] = map[string]any{
		"KEEP": "same", "CHANGE": "remote-value", "REMOVE": "removed-value",
	}
	reader := &previewAgentReader{result: remote}
	options := DirectDeployOptions{DefinitionPath: path, ProjectEndpoint: "https://example.com"}
	result, err := previewStandaloneHostedAgent(t.Context(), options, nil,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.Equal(t, []DeployPreviewChangeGroup{
		{Group: "metadata", Changes: []DeployPreviewChange{
			{Field: "metadata.tags", Kind: "modify", Before: []any{"Retired", "Streaming"}, After: []any{"Test"}},
		}},
		{Group: "environmentVariables", Changes: []DeployPreviewChange{
			{Field: "ANY_THING", Kind: "add", After: "[redacted]", Sensitive: true},
			{Field: "CHANGE", Kind: "modify", Before: "[redacted]", After: "[redacted]", Sensitive: true},
			{Field: "REMOVE", Kind: "remove", Before: "[redacted]", Sensitive: true},
		}},
	}, result.Changes)
	for _, conflict := range result.SourceConflicts {
		for _, group := range conflict.Differences {
			for _, change := range group.Changes {
				assert.NotEqual(t, "ANY_THING", change.Field, "a unique inherited variable is not a conflict")
			}
		}
	}
	wire, err := json.Marshal(result)
	require.NoError(t, err)
	for _, value := range []string{"selected-value", "lower-priority-value", "manifest-only-value", "removed-value"} {
		assert.NotContains(t, string(wire), value)
	}

	reader.result = deployedPreviewAgent(t, request)
	result, err = previewStandaloneHostedAgent(t.Context(), options, nil,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.Empty(t, result.Changes)
	assert.False(t, result.HasChanges, "overridden local values are not remote deployment changes")
	assert.NotEmpty(t, result.SourceConflicts, "source precedence remains available for diagnostics")
}

func TestPreviewExplicitEnvironmentClear(t *testing.T) {
	t.Parallel()
	for _, empty := range []any{[]any{}, nil} {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			path := writePreviewYAML(t, directory, "agent.yaml", map[string]any{
				"kind": "hosted", "name": "research-agent", "environment_variables": empty,
			})
			writePreviewYAML(t, directory, "agent.manifest.yaml", map[string]any{
				"name": "research-agent", "template": map[string]any{
					"kind": "hosted", "environment_variables": []any{
						map[string]any{"name": "REMOVE", "value": "manifest-value"},
					},
				},
			})
			_, values := legacyDeploymentRequest(t, path, nil)
			assert.Empty(t, values)
			request, _ := legacyDeploymentRequest(t, path, map[string]string{"REMOVE": "remote-value"})
			reader := &previewAgentReader{result: deployedPreviewAgent(t, request)}
			result, err := previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
				DefinitionPath: path, ProjectEndpoint: "https://example.com",
			}, nil, func(string) (standaloneAgentReader, error) { return reader, nil })
			require.NoError(t, err)
			assert.Contains(t, result.Changes, DeployPreviewChangeGroup{
				Group: "environmentVariables", Changes: []DeployPreviewChange{
					{Field: "REMOVE", Kind: "remove", Before: "[redacted]", Sensitive: true},
				},
			})
		})
	}
}

func TestPreviewInlineEnvironmentRemainsAuthoritative(t *testing.T) {
	t.Parallel()
	options := agentServicePreviewFixture(t)
	options.Service.Environment = map[string]string{"KEEP": "selected"}
	writePreviewYAML(t, filepath.Join(options.ProjectRoot, "src"), "agent.manifest.yaml", map[string]any{
		"name": "research-agent", "template": map[string]any{
			"kind": "hosted", "environment_variables": []any{
				map[string]any{"name": "DO_NOT_RESURRECT", "value": "legacy"},
			},
		},
	})
	definition, _, _, err := LoadAgentDefinition(options.Service, options.ProjectRoot)
	require.NoError(t, err)
	prepared, err := prepareDeployRequest(options.Service, definition,
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": options.ProjectEndpoint}, nil)
	require.NoError(t, err)
	reader := &previewAgentReader{result: deployedPreviewAgent(t, prepared.request)}
	result, err := previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.Empty(t, result.Changes)
	assert.False(t, result.HasChanges)
}

func TestPreviewSourcePathsAreClean(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := writePreviewYAML(t, directory, "agent.yaml", map[string]any{
		"kind": "hosted", "name": "research-agent", "description": "selected",
	})
	manifestPath := writePreviewYAML(t, directory, "agent.manifest.yaml", map[string]any{
		"name": "research-agent", "description": "overridden", "template": map[string]any{"kind": "hosted"},
	})
	uncleanPath := directory + string(filepath.Separator) + "." + string(filepath.Separator) + "agent.yaml"
	request, _ := legacyDeploymentRequest(t, path, nil)
	reader := &previewAgentReader{result: deployedPreviewAgent(t, request)}
	result, err := previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
		DefinitionPath: uncleanPath, ProjectEndpoint: "https://example.com",
	}, nil, func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.Equal(t, []string{manifestPath, path}, result.Sources)
	require.Len(t, result.SourceConflicts, 1)
	assert.Equal(t, manifestPath, result.SourceConflicts[0].Source)
}

func TestPreviewEnvironmentMergeRejectsDuplicateNames(t *testing.T) {
	t.Parallel()
	path := writePreviewYAML(t, t.TempDir(), "agent.yaml", map[string]any{
		"kind": "hosted", "name": "research-agent", "environment_variables": []any{
			map[string]any{"name": "DUPLICATE", "value": "first"},
			map[string]any{"name": "DUPLICATE", "value": "second"},
		},
	})
	_, err := LoadAgentPreviewDefinition(path, nil)
	require.ErrorContains(t, err, "DUPLICATE")
}

func TestPreviewInlineTagRemoval(t *testing.T) {
	t.Parallel()
	options := agentServicePreviewFixture(t)
	metadata, err := structpb.NewStruct(map[string]any{"tags": []any{}})
	require.NoError(t, err)
	options.Service.AdditionalProperties.Fields["metadata"] = structpb.NewStructValue(metadata)
	definition, _, _, err := LoadAgentDefinition(options.Service, options.ProjectRoot)
	require.NoError(t, err)
	prepared, err := prepareDeployRequest(options.Service, definition,
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": options.ProjectEndpoint}, nil)
	require.NoError(t, err)
	reader := &previewAgentReader{result: deployedPreviewAgent(t, prepared.request)}
	reader.result.Versions.Latest.Metadata["tags"] = `["Retired"]`
	result, err := previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.Contains(t, result.Changes, DeployPreviewChangeGroup{Group: "metadata", Changes: []DeployPreviewChange{
		{Field: "metadata.tags", Kind: "remove", Before: []any{"Retired"}},
	}})
}

func TestPreviewNoOpImagesHaveNoImageNotes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"code", "prebuilt"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			options := agentServicePreviewFixture(t)
			if mode == "prebuilt" {
				delete(options.Service.AdditionalProperties.Fields, "codeConfiguration")
				options.Service.Image = "registry.example.com/agent:v1"
				options.Service.Docker = &azdext.DockerProjectOptions{ImagePassthrough: true}
			}
			definition, _, _, err := LoadAgentDefinition(options.Service, options.ProjectRoot)
			require.NoError(t, err)
			plan, err := planPreviewImage(definition, options.Service, "", nil)
			require.NoError(t, err)
			prepared, err := prepareDeployRequest(options.Service, definition,
				map[string]string{"FOUNDRY_PROJECT_ENDPOINT": options.ProjectEndpoint}, previewImageOption(plan))
			require.NoError(t, err)
			reader := &previewAgentReader{result: deployedPreviewAgent(t, prepared.request)}
			result, err := previewAgentService(t.Context(), options,
				func(string) (standaloneAgentReader, error) { return reader, nil })
			require.NoError(t, err)
			assert.False(t, result.HasImageChanges())
			assert.Equal(t, mode, result.Image.Mode)
			for _, note := range result.Notes {
				assert.NotContains(t, strings.ToLower(note), "image", "no-op image details should not reappear in notes")
			}
		})
	}
}

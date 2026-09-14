// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func writePreviewYAML(t *testing.T, directory, name string, value any) string {
	t.Helper()
	data, err := yaml.Marshal(value)
	require.NoError(t, err)
	path := filepath.Join(directory, name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestPreviewLegacySplitDefinitionAndManifest(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := writePreviewYAML(t, directory, "agent.yaml", map[string]any{
		"kind": "hosted", "name": "research-agent",
		"protocols": []any{map[string]any{"protocol": "responses", "version": "2.0.0"}},
		"resources": map[string]any{"cpu": "0.5", "memory": "1Gi"},
		"environment_variables": []any{
			map[string]any{"name": "LOG_LEVEL", "value": "debug"},
			map[string]any{"name": previewModelDeploymentKey, "value": "new-model"},
		},
	})
	manifestPath := writePreviewYAML(t, directory, "agent.manifest.yaml", map[string]any{
		"name": "research-agent", "description": "Description from the original manifest",
		"metadata": map[string]any{"tags": []any{"Streaming", "Test"}},
		"template": map[string]any{"kind": "hosted", "name": "research-agent"},
	})
	agentBefore, err := os.ReadFile(path)
	require.NoError(t, err)
	manifestBefore, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	remote := deploymentPreviewRequest()
	definition, ok := remote.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	definition.CPU, definition.Memory = "0.25", "0.5Gi"
	definition.ProtocolVersions = []agent_api.ProtocolVersionRecord{{Protocol: "responses", Version: "1.0.0"}}
	definition.EnvironmentVariables = map[string]string{"LOG_LEVEL": "info", previewModelDeploymentKey: "old-model"}
	remote.Definition = definition
	remote.Description = nil
	remote.Metadata = map[string]string{"enableVnextExperience": "true"}
	reader := &previewAgentReader{result: deployedPreviewAgent(t, remote)}

	result, err := previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
		DefinitionPath: path, ProjectEndpoint: "https://example.com",
	}, nil, func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.Equal(t, []string{manifestPath, path}, result.Sources)
	assert.Empty(t, result.SourceConflicts)
	require.Len(t, result.Changes, 5)
	assert.Equal(t, "metadata", result.Changes[0].Group)
	assert.Contains(t, result.Changes[0].Changes, DeployPreviewChange{
		Field: "metadata.tags", Kind: "add", After: []any{"Streaming", "Test"},
	})
	assert.Contains(t, result.Changes[0].Changes, DeployPreviewChange{
		Field: "description", Kind: "modify", Before: "", After: "Description from the original manifest",
	})
	assert.Equal(t, "protocols", result.Changes[1].Group)
	assert.Equal(t, "resources", result.Changes[2].Group)
	assert.Equal(t, "environmentVariables", result.Changes[3].Group)
	assert.Equal(t, "modelDeployment", result.Changes[4].Group)
	require.NotNil(t, result.Image)
	assert.Equal(t, "code", result.Image.Mode)
	assert.False(t, result.Image.Build)
	assert.False(t, result.Image.Push)
	assert.Equal(t, 1, reader.calls)
	agentAfter, err := os.ReadFile(path)
	require.NoError(t, err)
	manifestAfter, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	assert.Equal(t, agentBefore, agentAfter)
	assert.Equal(t, manifestBefore, manifestAfter)

	// The real standalone deploy loader now uses the same companion metadata.
	loaded, environment, err := prepareStandaloneHostedDefinition(path, nil)
	require.NoError(t, err)
	request, err := standaloneAgentRequest(loaded, environment)
	require.NoError(t, err)
	assert.Equal(t, `["Streaming","Test"]`, request.Metadata["tags"])
}

func TestPreviewManifestOnlyAndExplicitPrecedence(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	manifestPath := writePreviewYAML(t, directory, "agent.manifest.yaml", map[string]any{
		"name": "research-agent", "description": "Manifest description",
		"metadata": map[string]any{"tags": []any{"Manifest"}},
		"template": map[string]any{
			"kind": "hosted", "resources": map[string]any{"cpu": "1", "memory": "2Gi"},
			"protocols": []any{map[string]any{"protocol": "responses", "version": "2.0.0"}},
		},
	})
	reader := &previewAgentReader{err: &azcore.ResponseError{StatusCode: http.StatusNotFound}}
	result, err := previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
		DefinitionPath: manifestPath, ProjectEndpoint: "https://example.com",
	}, nil, func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.Equal(t, "create", result.Operation)
	assert.Equal(t, "research-agent", result.Name)
	assert.True(t, result.HasChanges)
	assert.Equal(t, []string{manifestPath}, result.Sources)
	require.NoFileExists(t, filepath.Join(directory, "agent.yaml"))

	agentPath := writePreviewYAML(t, directory, "agent.yaml", map[string]any{
		"kind": "hosted", "name": "research-agent", "resources": map[string]any{"cpu": "0.5", "memory": "1Gi"},
	})
	for _, selected := range []string{manifestPath, agentPath} {
		loaded, err := LoadAgentPreviewDefinition(selected, nil)
		require.NoError(t, err)
		if selected == manifestPath {
			assert.Equal(t, "1", loaded.Definition.Resources.Cpu)
		} else {
			assert.Equal(t, "0.5", loaded.Definition.Resources.Cpu)
		}
		result, err := previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
			DefinitionPath: selected, ProjectEndpoint: "https://example.com",
		}, nil, func(string) (standaloneAgentReader, error) { return reader, nil })
		require.NoError(t, err)
		require.Len(t, result.SourceConflicts, 1, "overlapping values must not disappear behind precedence")
		assert.NotEmpty(t, result.SourceConflicts[0].Differences)
	}
}

func TestLoadPreviewManifestParametersAndMetadata(t *testing.T) {
	t.Parallel()
	path := writePreviewYAML(t, t.TempDir(), "agent.manifest.yml", map[string]any{
		"name": "research-agent", "description": "Agent {{label}}",
		"metadata": map[string]any{"tags": []any{"Test"}},
		"parameters": map[string]any{"properties": map[string]any{
			"label": map[string]any{"type": "string", "default": "default-label"},
		}},
		"template": map[string]any{
			"kind": "hosted", "metadata": map[string]any{"owner": "team"},
			"environment_variables": []any{
				map[string]any{"name": previewModelDeploymentKey, "value": "{{MODEL}}"},
			},
		},
	})
	loaded, err := LoadAgentPreviewDefinition(path, map[string]string{"MODEL": "chosen-deployment"})
	require.NoError(t, err)
	assert.Equal(t, "Agent default-label", *loaded.Definition.Description)
	require.NotNil(t, loaded.Definition.Metadata)
	assert.Equal(t, []any{"Test"}, (*loaded.Definition.Metadata)["tags"])
	assert.Equal(t, "team", (*loaded.Definition.Metadata)["owner"])
	assert.Equal(t, "chosen-deployment", AgentEnvironment(loaded.Definition)[previewModelDeploymentKey])
}

func TestPreviewManifestUnknownsArePending(t *testing.T) {
	t.Parallel()
	path := writePreviewYAML(t, t.TempDir(), "agent.manifest.yaml", map[string]any{
		"name": "research-agent",
		"template": map[string]any{
			"kind": "hosted", "resources": map[string]any{"cpu": "{{cpu-not-configured}}", "memory": "1Gi"},
			"environment_variables": []any{map[string]any{
				"name": previewModelDeploymentKey, "value": "{{model-not-configured}}",
			}},
		},
	})
	reader := &previewAgentReader{result: deployedPreviewAgent(t, deploymentPreviewRequest())}
	result, err := previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
		DefinitionPath: path, ProjectEndpoint: "https://example.com",
	}, nil, func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	found := map[string]bool{}
	for _, group := range result.Changes {
		for _, change := range group.Changes {
			if change.Kind == "pending" {
				found[group.Group+"."+change.Field] = true
				assert.Nil(t, change.After)
			}
		}
	}
	assert.True(t, found["resources.cpu"])
	assert.True(t, found["modelDeployment."+previewModelDeploymentKey])
}

func TestPreviewLegacyProjectReadsBothFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	directory := filepath.Join(root, "src")
	require.NoError(t, os.Mkdir(directory, 0o700))
	writePreviewYAML(t, directory, "agent.yaml", map[string]any{
		"kind": "hosted", "name": "research-agent",
		"resources":          map[string]any{"cpu": "0.5", "memory": "1Gi"},
		"code_configuration": map[string]any{"runtime": "python_3_13", "entry_point": "main.py"},
	})
	writePreviewYAML(t, directory, "agent.manifest.yaml", map[string]any{
		"name": "research-agent", "metadata": map[string]any{"tags": []any{"Test"}},
		"template": map[string]any{"kind": "hosted", "name": "research-agent"},
	})
	reader := &previewAgentReader{err: &azcore.ResponseError{StatusCode: http.StatusNotFound}}
	result, err := previewAgentService(t.Context(), AgentServicePreviewOptions{
		Service:     &azdext.ServiceConfig{Name: "agent", Host: "azure.ai.agent", RelativePath: "src"},
		ProjectRoot: root, ProjectEndpoint: "https://example.com",
	}, func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	data, err := json.Marshal(result)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"metadata.tags"`)
	assert.Contains(t, string(data), `"Test"`)
}

func TestPreviewMalformedCompanionIsAnError(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := writePreviewYAML(t, directory, "agent.yaml", map[string]any{"kind": "hosted", "name": "agent"})
	require.NoError(t, os.WriteFile(filepath.Join(directory, "agent.manifest.yaml"), []byte("template: ["), 0o600))
	_, err := LoadAgentPreviewDefinition(path, nil)
	require.ErrorContains(t, err, "agent.manifest.yaml")
}

func TestPreviewManifestImageAndCodePriority(t *testing.T) {
	t.Parallel()
	path := writePreviewYAML(t, t.TempDir(), "agent.manifest.yaml", map[string]any{
		"name":     "research-agent",
		"template": map[string]any{"kind": "hosted", "image": "registry.example.com/agent:v2"},
	})
	reader := &previewAgentReader{err: &azcore.ResponseError{StatusCode: http.StatusNotFound}}
	result, err := previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
		DefinitionPath: path, ProjectEndpoint: "https://example.com",
	}, nil, func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.Equal(t, "prebuilt", result.Image.Mode)
	assert.Equal(t, "registry.example.com/agent:v2", result.Image.Image)
	assert.False(t, result.Image.Build)
	assert.False(t, result.Image.Push)
	require.Contains(t, result.Changes, DeployPreviewChangeGroup{Group: "containerImage", Changes: []DeployPreviewChange{
		{Field: "image", Kind: "add", After: "registry.example.com/agent:v2"},
	}})
	_, err = previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
		DefinitionPath: path, CodePath: t.TempDir(), ProjectEndpoint: "https://example.com",
	}, nil, func(string) (standaloneAgentReader, error) {
		t.Fatal("--code must not be silently ignored for a prebuilt image")
		return nil, nil
	})
	require.ErrorContains(t, err, "--code")

	plan, err := planPreviewImage(agent_yaml.ContainerAgent{
		Image:             "registry.example.com/agent:v2",
		CodeConfiguration: &agent_yaml.CodeConfiguration{Runtime: "python_3_13", EntryPoint: "main.py"},
	}, nil, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "code", plan.Mode)
}

func TestPreviewMatchingUnresolvedCompanionsDoNotConflict(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := writePreviewYAML(t, directory, "agent.yaml", map[string]any{
		"kind": "hosted", "name": "research-agent",
		"environment_variables": []any{map[string]any{
			"name": previewModelDeploymentKey, "value": "${AZD_TEST_UNSET_PREVIEW_MODEL_8549}",
		}},
	})
	writePreviewYAML(t, directory, "agent.manifest.yaml", map[string]any{
		"name": "research-agent", "template": map[string]any{
			"kind": "hosted",
			"environment_variables": []any{map[string]any{
				"name": previewModelDeploymentKey, "value": "{{AZD_TEST_UNSET_PREVIEW_MODEL_8549}}",
			}},
		},
	})
	reader := &previewAgentReader{result: deployedPreviewAgent(t, deploymentPreviewRequest())}
	result, err := previewStandaloneHostedAgent(t.Context(), DirectDeployOptions{
		DefinitionPath: path, ProjectEndpoint: "https://example.com",
	}, nil, func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.Empty(t, result.SourceConflicts, "equivalent unresolved inputs are pending, not conflicting")
}

func TestLoadLegacyNonHostedDefinitionPreservesKind(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePreviewYAML(t, root, "agent.yaml", map[string]any{"kind": "workflow", "name": "workflow-agent"})
	_, hosted, _, err := LoadAgentDefinition(&azdext.ServiceConfig{
		Name: "agent", Host: "azure.ai.agent", RelativePath: ".",
	}, root)
	require.NoError(t, err)
	assert.False(t, hosted, "hosted manifest support must not change non-hosted definition dispatch")
}

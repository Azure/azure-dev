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
	"google.golang.org/protobuf/proto"
)

func agentServicePreviewFixture(t *testing.T) AgentServicePreviewOptions {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0o700))
	definition := agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Name: "research-agent", Kind: agent_yaml.AgentKindHosted, Description: new("Research assistant"),
		},
		Language: "python",
		Protocols: []agent_yaml.ProtocolVersionRecord{
			{Protocol: "responses", Version: "2.0.0"},
		},
		CodeConfiguration: &agent_yaml.CodeConfiguration{Runtime: "python_3_13", EntryPoint: "main.py"},
		Resources:         &agent_yaml.ContainerResources{Cpu: "1", Memory: "2Gi"},
	}
	properties, err := AgentDefinitionToServiceProperties(definition, nil)
	require.NoError(t, err)
	return AgentServicePreviewOptions{
		Service: &azdext.ServiceConfig{
			Name: "service-key", Host: "azure.ai.agent", RelativePath: "src", Language: "python",
			AdditionalProperties: properties,
			Environment: map[string]string{
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "test-model",
				"API_KEY":                        "a-private-runtime-value",
			},
		},
		ProjectRoot: root, ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project",
		Environment: map[string]string{
			"UNRELATED_SECRET":       "never-inject-this",
			"AGENT_SERVICE_KEY_NAME": "stale-deployed-name",
		},
	}
}

func TestPreviewAgentServiceWouldCreateWithoutAgentFile(t *testing.T) {
	t.Parallel()
	options := agentServicePreviewFixture(t)
	original := proto.CloneOf(options.Service)
	reader := &previewAgentReader{err: &azcore.ResponseError{StatusCode: http.StatusNotFound}}
	result, err := previewAgentService(t.Context(), options, func(endpoint string) (standaloneAgentReader, error) {
		assert.Equal(t, options.ProjectEndpoint, endpoint)
		return reader, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "create", result.Operation)
	assert.True(t, result.HasChanges)
	assert.Equal(t, "research-agent", result.Name, "local desired name must win over stale deployment outputs")
	assert.Equal(t, "service-key", result.Service)
	assert.Equal(t, filepath.Join(options.ProjectRoot, "src"), result.SourcePath)
	assert.Empty(t, result.CurrentVersion)
	assert.Equal(t, 1, reader.calls)
	assert.Equal(t, "research-agent", reader.name)
	require.NoFileExists(t, filepath.Join(options.ProjectRoot, "agent.yaml"))
	require.NoFileExists(t, filepath.Join(options.ProjectRoot, "src", "agent.yaml"))
	assert.True(t, proto.Equal(original, options.Service), "preview must not mutate project configuration")
	require.Contains(t, result.Changes, DeployPreviewChangeGroup{
		Group: "resources", Changes: []DeployPreviewChange{
			{Field: "cpu", Kind: "add", After: "1"},
			{Field: "memory", Kind: "add", After: "2Gi"},
		},
	})
	require.Contains(t, result.Changes, DeployPreviewChangeGroup{
		Group: "modelDeployment", Changes: []DeployPreviewChange{
			{Field: "AZURE_AI_MODEL_DEPLOYMENT_NAME", Kind: "add", After: "test-model"},
		},
	})
	data, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "never-inject-this")
	assert.NotContains(t, string(data), "UNRELATED_SECRET")
	assert.NotContains(t, string(data), "a-private-runtime-value")
}

func TestPreviewAgentServiceMatchesRealDeployRequest(t *testing.T) {
	t.Parallel()
	options := agentServicePreviewFixture(t)
	definition, hosted, _, err := LoadAgentDefinition(options.Service, options.ProjectRoot)
	require.NoError(t, err)
	require.True(t, hosted)
	environment := map[string]string{"FOUNDRY_PROJECT_ENDPOINT": options.ProjectEndpoint}
	prepared, err := prepareDeployRequest(options.Service, definition, environment, nil)
	require.NoError(t, err)
	reader := &previewAgentReader{result: deployedPreviewAgent(t, prepared.request)}
	result, err := previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.False(t, result.HasChanges)
	assert.Empty(t, result.Changes)
	assert.Equal(t, "create_version", result.Operation)
	assert.Equal(t, "7", result.CurrentVersion)
}

func TestPreviewAgentServiceEmptyEnvironmentDoesNotInjectAzdValues(t *testing.T) {
	t.Parallel()
	options := agentServicePreviewFixture(t)
	options.Service.Environment = map[string]string{}
	reader := &previewAgentReader{err: &azcore.ResponseError{StatusCode: http.StatusNotFound}}
	result, err := previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	for _, group := range result.Changes {
		assert.NotEqual(t, "environmentVariables", group.Group)
		assert.NotEqual(t, "modelDeployment", group.Group)
	}
}

func TestPreviewAgentServicePendingModelDeployment(t *testing.T) {
	t.Parallel()
	options := agentServicePreviewFixture(t)
	options.Service.Environment["AZURE_AI_MODEL_DEPLOYMENT_NAME"] = ""
	options.PendingEnvironment = []string{"AZURE_AI_MODEL_DEPLOYMENT_NAME"}
	reader := &previewAgentReader{err: &azcore.ResponseError{StatusCode: http.StatusNotFound}}
	result, err := previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	require.Contains(t, result.Changes, DeployPreviewChangeGroup{
		Group: "modelDeployment", Changes: []DeployPreviewChange{
			{Field: "AZURE_AI_MODEL_DEPLOYMENT_NAME", Kind: "pending"},
		},
	})
}

func TestPreviewAgentServiceSourceValidation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*AgentServicePreviewOptions)
	}{
		{name: "missing override", change: func(options *AgentServicePreviewOptions) {
			options.CodePath = filepath.Join(options.ProjectRoot, "missing")
		}},
		{name: "path traversal", change: func(options *AgentServicePreviewOptions) {
			options.Service.RelativePath = ".."
		}},
		{name: "unsupported agent kind", change: func(options *AgentServicePreviewOptions) {
			delete(options.Service.AdditionalProperties.Fields, "kind")
		}},
		{name: "invalid definition", change: func(options *AgentServicePreviewOptions) {
			delete(options.Service.AdditionalProperties.Fields, "name")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := agentServicePreviewFixture(t)
			test.change(&options)
			result, err := previewAgentService(t.Context(), options, func(string) (standaloneAgentReader, error) {
				t.Fatal("invalid project inputs must fail before authentication or an Azure request")
				return nil, nil
			})
			require.Error(t, err)
			assert.Nil(t, result)
		})
	}
}

func TestPreviewAgentServiceExplicitSourceAndRemoteErrors(t *testing.T) {
	t.Parallel()
	options := agentServicePreviewFixture(t)
	options.CodePath = t.TempDir()
	reader := &previewAgentReader{err: &azcore.ResponseError{StatusCode: http.StatusForbidden}}
	result, err := previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.Error(t, err)
	assert.Nil(t, result, "access failures must not become a creation plan")

	reader.err = &azcore.ResponseError{StatusCode: http.StatusNotFound}
	result, err = previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.Equal(t, options.CodePath, result.SourcePath)
}

func TestPreviewAgentServiceDigitalWorkerCreation(t *testing.T) {
	t.Parallel()
	options := agentServicePreviewFixture(t)
	definition, _, _, err := LoadAgentDefinition(options.Service, options.ProjectRoot)
	require.NoError(t, err)
	definition.Protocols = []agent_yaml.ProtocolVersionRecord{{Protocol: "activity", Version: "2.0.0"}}
	options.Service.AdditionalProperties, err = AgentDefinitionToServiceProperties(definition, &ServiceTargetAgentConfig{
		Activity: &ActivitySettings{DigitalWorkerType: agent_api.DigitalWorkerTypeM365},
	})
	require.NoError(t, err)
	reader := &previewAgentReader{err: &azcore.ResponseError{StatusCode: http.StatusNotFound}}
	result, err := previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	for _, group := range result.Changes {
		if group.Group == "metadata" {
			assert.Contains(t, group.Changes,
				DeployPreviewChange{Field: "digital_worker_type", Kind: "add", After: "m365"})
			return
		}
	}
	t.Fatal("creation preview did not include metadata")
}

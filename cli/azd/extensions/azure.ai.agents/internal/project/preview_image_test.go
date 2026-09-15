// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"maps"
	"net/http"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanPreviewImage(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		definition  agent_yaml.ContainerAgent
		docker      *azdext.DockerProjectOptions
		environment map[string]string
		mode        string
		known       bool
		image       string
		wantError   bool
	}{
		{name: "code", definition: agent_yaml.ContainerAgent{
			CodeConfiguration: &agent_yaml.CodeConfiguration{Runtime: "python_3_13", EntryPoint: "main.py"},
		}, mode: "code", known: true},
		{name: "passthrough", definition: agent_yaml.ContainerAgent{Image: "registry.example.com/agent:v1"},
			docker: &azdext.DockerProjectOptions{ImagePassthrough: true}, mode: "prebuilt", known: true,
			image: "registry.example.com/agent:v1"},
		{name: "registry connection", definition: agent_yaml.ContainerAgent{
			Image: "registry.example.com/agent:v1", RegistryConnectionID: "private-registry",
		}, mode: "prebuilt", known: true, image: "registry.example.com/agent:v1"},
		{name: "known build tag", docker: &azdext.DockerProjectOptions{
			Image: "agent", Tag: "v2", Registry: "registry.example.com",
		}, mode: "build", known: true, image: "registry.example.com/agent:v2"},
		{name: "embedded tag wins", docker: &azdext.DockerProjectOptions{
			Image: "agent:v3", Tag: "v2", Registry: "registry.example.com",
		}, mode: "build", known: true, image: "registry.example.com/agent:v3"},
		{name: "env registry wins", docker: &azdext.DockerProjectOptions{
			Image: "other.example.com/agent:v3", Registry: "configured.example.com",
		}, environment: map[string]string{"AZURE_CONTAINER_REGISTRY_ENDPOINT": "env.example.com"},
			mode: "build", known: true, image: "env.example.com/agent:v3"},
		{name: "default timestamp is unknown", docker: &azdext.DockerProjectOptions{
			Image: "agent", Registry: "registry.example.com",
		}, mode: "build"},
		{name: "unprovisioned registry", docker: &azdext.DockerProjectOptions{Image: "agent", Tag: "v2"}, mode: "build"},
		{name: "generated name", docker: &azdext.DockerProjectOptions{Tag: "v1", Registry: "registry.example.com"},
			environment: map[string]string{"AZURE_ENV_NAME": "dev"}, mode: "build", known: true,
			image: "registry.example.com/project/agent-dev:v1"},
		{name: "configured image defaults to build", definition: agent_yaml.ContainerAgent{
			Image: "registry.example.com/prebuilt:v1",
		}, mode: "build"},
		{name: "legacy prebuilt marker", definition: agent_yaml.ContainerAgent{Image: "registry.example.com/agent:v1"},
			environment: map[string]string{"AZD_AGENT_SKIP_ACR": "true"}, mode: "prebuilt", known: true,
			image: "registry.example.com/agent:v1"},
		{name: "legacy marker preserves explicit passthrough", docker: &azdext.DockerProjectOptions{
			ImagePassthrough: true, Image: "registry.example.com/agent:v1",
		}, environment: map[string]string{"AZD_AGENT_SKIP_ACR": "true"}, mode: "prebuilt", known: true,
			image: "registry.example.com/agent:v1"},
		{name: "conflicting passthrough build", definition: agent_yaml.ContainerAgent{Image: "registry.example.com/agent:v1"},
			docker: &azdext.DockerProjectOptions{ImagePassthrough: true, RemoteBuild: true}, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			service := &azdext.ServiceConfig{Name: "agent", Docker: tt.docker}
			environment := map[string]string{"AZD_AGENT_SKIP_ACR": ""}
			maps.Copy(environment, tt.environment)
			plan, err := planPreviewImage(tt.definition, service, "project", environment)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.mode, plan.Mode)
			assert.Equal(t, tt.known, plan.Known)
			assert.Equal(t, tt.image, plan.Image)
			assert.Equal(t, tt.mode == "build", plan.Build)
			assert.Equal(t, tt.mode == "build", plan.Push)
		})
	}
}

func TestPreviewProjectContainerBuild(t *testing.T) {
	t.Parallel()
	options := agentServicePreviewFixture(t)
	delete(options.Service.AdditionalProperties.Fields, "codeConfiguration")
	options.Service.Docker = &azdext.DockerProjectOptions{Image: "registry.example.com/agent"}
	reader := &previewAgentReader{err: &azcore.ResponseError{StatusCode: http.StatusNotFound}}
	result, err := previewAgentService(t.Context(), options,
		func(string) (standaloneAgentReader, error) { return reader, nil })
	require.NoError(t, err)
	assert.True(t, result.Image.Build)
	assert.True(t, result.Image.Push)
	assert.False(t, result.Image.Known)
	require.Contains(t, result.Changes, DeployPreviewChangeGroup{Group: "containerImage", Changes: []DeployPreviewChange{
		{Field: "image", Kind: "pending"},
	}})
	assert.Equal(t, 1, reader.calls, "preview requires only reading the remote agent, not running a container tool")
}

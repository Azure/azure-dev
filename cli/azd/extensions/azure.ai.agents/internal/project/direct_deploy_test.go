// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareStandaloneHostedDefinitionDefaults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: research-agent
kind: hosted
language: python
toolbox:
  name: support-tools
protocols:
  - protocol: responses
    version: 2.0.0
`), 0o600))

	definition, environment, err := prepareStandaloneHostedDefinition(path, nil)
	require.NoError(t, err)
	require.NotNil(t, definition.CodeConfiguration)
	assert.Equal(t, "python_3_13", definition.CodeConfiguration.Runtime)
	assert.Equal(t, "main.py", definition.CodeConfiguration.EntryPoint)
	require.NotNil(t, definition.Resources)
	assert.Equal(t, DefaultCpu, definition.Resources.Cpu)
	assert.Equal(t, DefaultMemory, definition.Resources.Memory)
	assert.Equal(t, "support-tools", environment["TOOLBOX_NAME"])
}

func TestPrepareStandaloneHostedDefinitionResolvesEnvironment(t *testing.T) {
	t.Setenv("MODEL_NAME", "gpt-5-mini")
	path := filepath.Join(t.TempDir(), "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: research-agent
kind: hosted
language: python
environment_variables:
  - name: AZURE_AI_MODEL_DEPLOYMENT_NAME
    value: ${MODEL_NAME}
`), 0o600))

	_, environment, err := prepareStandaloneHostedDefinition(path, nil)
	require.NoError(t, err)
	assert.Equal(t, "gpt-5-mini", environment["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
}

func TestPrepareStandaloneHostedDefinitionRejectsImplicitCSharp(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: research-agent
kind: hosted
language: csharp
`), 0o600))

	_, _, err := prepareStandaloneHostedDefinition(path, nil)
	require.Error(t, err)
}

func TestPrepareStandaloneHostedAgentPreparesPackageAndCredential(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: research-agent
kind: hosted
language: python
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "main.py"), []byte("print('ready')\n"), 0o600))

	prepared, err := PrepareStandaloneHostedAgent(t.Context(), DirectDeployOptions{
		DefinitionPath:  path,
		CodePath:        directory,
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project",
	})
	require.NoError(t, err)
	require.NotNil(t, prepared)
	assert.NotEmpty(t, prepared.zipData)
	assert.NotEmpty(t, prepared.sha256Hex)
	assert.NotNil(t, prepared.credential)
}

func TestReconcileStandaloneEndpointWithDeployedDigitalWorker(t *testing.T) {
	t.Parallel()

	definition := agent_yaml.ContainerAgent{
		Protocols: []agent_yaml.ProtocolVersionRecord{{Protocol: "activity", Version: "2.0.0"}},
	}
	request := &agent_api.CreateAgentRequest{
		AgentEndpoint: &agent_api.AgentEndpoint{
			Protocols: []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolActivity},
			AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
				{Type: agent_api.AgentEndpointAuthSchemeBotServiceRbac},
			},
		},
	}
	existingAgent := &agent_api.AgentObject{DigitalWorkerType: agent_api.DigitalWorkerTypeM365}

	err := reconcileStandaloneEndpointWithDeployedAgent(request, ResolveActivityProfile(definition), existingAgent)

	require.NoError(t, err)
	require.Equal(t, []agent_api.AgentEndpointAuthorizationScheme{
		{Type: agent_api.AgentEndpointAuthSchemeBotServiceTenant},
	}, request.AgentEndpoint.AuthorizationSchemes)
}

func TestReconcileStandaloneEndpointPromotesNonActivityDefinitionForDeployedDigitalWorker(t *testing.T) {
	t.Parallel()

	request := &agent_api.CreateAgentRequest{
		AgentEndpoint: &agent_api.AgentEndpoint{
			Protocols: []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolResponses},
			AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
				{Type: agent_api.AgentEndpointAuthSchemeEntra},
			},
		},
	}
	existingAgent := &agent_api.AgentObject{DigitalWorkerType: agent_api.DigitalWorkerTypeM365}

	err := reconcileStandaloneEndpointWithDeployedAgent(request, ActivityProfile{}, existingAgent)

	require.NoError(t, err)
	require.Equal(t, []agent_api.AgentEndpointProtocol{
		agent_api.AgentEndpointProtocolResponses,
		agent_api.AgentEndpointProtocolActivity,
	}, request.AgentEndpoint.Protocols)
	require.Equal(t, []agent_api.AgentEndpointAuthorizationScheme{
		{Type: agent_api.AgentEndpointAuthSchemeEntra},
		{Type: agent_api.AgentEndpointAuthSchemeBotServiceTenant},
	}, request.AgentEndpoint.AuthorizationSchemes)
}

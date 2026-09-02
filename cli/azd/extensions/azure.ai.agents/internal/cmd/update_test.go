// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestEndpointUpdateResolvesActivitySettingsFromServiceRef(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	definitionsDir := filepath.Join(projectRoot, "definitions")
	require.NoError(t, os.MkdirAll(definitionsDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(definitionsDir, "agent.yaml"),
		[]byte(
			"kind: hosted\n"+
				"name: referenced-agent\n"+
				"protocols:\n"+
				"  - protocol: activity\n"+
				"    version: \"2.0.0\"\n"+
				"agentEndpoint:\n"+
				"  protocols:\n"+
				"    - activity\n"+
				"activity:\n"+
				"  digitalWorkerType: m365\n",
		),
		0o600,
	))

	props, err := structpb.NewStruct(map[string]any{"$ref": "./definitions/agent.yaml"})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "agent-service",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}

	require.NoError(t, project.ResolveServiceConfigInPlace(svc, projectRoot))
	agentDef, _, _, err := project.LoadAgentDefinition(svc, projectRoot)
	require.NoError(t, err)
	serviceConfig, err := project.LoadServiceTargetAgentConfig(svc)
	require.NoError(t, err)

	profile, err := project.ResolveActivityProfileWithSettings(agentDef, serviceConfig.Activity)
	require.NoError(t, err)
	require.Equal(t, project.ActivityUseCaseDigitalWorker, profile.UseCase)
}

func TestEnsureEndpointAuthSchemeForProfile_DigitalWorker(t *testing.T) {
	endpoint := &agent_api.AgentEndpoint{
		Protocols: []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolResponses},
		AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
			{Type: agent_api.AgentEndpointAuthSchemeEntra},
			{Type: agent_api.AgentEndpointAuthSchemeBotServiceRbac},
			{Type: agent_api.AgentEndpointAuthSchemeBotService},
		},
	}

	project.EnsureActivityEndpointAuthSchemeForProfile(endpoint, project.ActivityProfile{
		IsActivity: true,
		UseCase:    project.ActivityUseCaseDigitalWorker,
	})

	require.Contains(t, endpoint.Protocols, agent_api.AgentEndpointProtocolActivity)
	assert.Equal(t, []agent_api.AgentEndpointAuthorizationScheme{
		{Type: agent_api.AgentEndpointAuthSchemeEntra},
		{Type: agent_api.AgentEndpointAuthSchemeBotServiceTenant},
	}, endpoint.AuthorizationSchemes)
}

func TestEnsureEndpointAuthSchemeForProfile_Simple(t *testing.T) {
	endpoint := &agent_api.AgentEndpoint{
		Protocols: []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolResponses},
		AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
			{Type: agent_api.AgentEndpointAuthSchemeEntra},
			{Type: agent_api.AgentEndpointAuthSchemeBotServiceTenant},
		},
	}

	project.EnsureActivityEndpointAuthSchemeForProfile(endpoint, project.ActivityProfile{
		IsActivity: true,
		UseCase:    project.ActivityUseCaseSimple,
	})

	require.Contains(t, endpoint.Protocols, agent_api.AgentEndpointProtocolActivity)
	assert.Equal(t, []agent_api.AgentEndpointAuthorizationScheme{
		{Type: agent_api.AgentEndpointAuthSchemeEntra},
		{Type: agent_api.AgentEndpointAuthSchemeBotServiceRbac},
	}, endpoint.AuthorizationSchemes)
}

func TestEnsureEndpointAuthSchemeForProfile_NonActivityNoop(t *testing.T) {
	endpoint := &agent_api.AgentEndpoint{
		Protocols: []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolResponses},
		AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
			{Type: agent_api.AgentEndpointAuthSchemeEntra},
		},
	}

	project.EnsureActivityEndpointAuthSchemeForProfile(endpoint, project.ActivityProfile{})

	assert.Equal(t, []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolResponses}, endpoint.Protocols)
	assert.Equal(
		t,
		[]agent_api.AgentEndpointAuthorizationScheme{{Type: agent_api.AgentEndpointAuthSchemeEntra}},
		endpoint.AuthorizationSchemes,
	)
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

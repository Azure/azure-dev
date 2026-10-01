// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestEndpointUpdatePreservesStructuredServiceConfigErrors(t *testing.T) {
	tests := []struct {
		name           string
		definitionPath string
		config         map[string]any
		wantCode       string
		wantSuggestion string
	}{
		{
			name:           "definition path",
			definitionPath: "legacy-agent.yaml",
			wantCode:       exterrors.CodeUnsupportedAgentDefinitionPath,
			wantSuggestion: "unset AGENT_DEFINITION_PATH, then move the agent definition to " +
				"the azure.ai.agent service in azure.yaml, " +
				"or add an explicit root $ref on the service entry to a direct agent definition",
		},
		{
			name:     "nested config",
			config:   map[string]any{"kind": "hosted", "name": "legacy-agent"},
			wantCode: exterrors.CodeDeprecatedAgentServiceConfig,
			wantSuggestion: "move the agent definition to service-level properties in azure.yaml, " +
				"or add an explicit root $ref on the service entry to a direct agent definition",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AGENT_DEFINITION_PATH", tt.definitionPath)

			svc := &azdext.ServiceConfig{
				Name: "agent",
				Host: AiAgentHost,
			}
			if tt.config != nil {
				config, err := structpb.NewStruct(tt.config)
				require.NoError(t, err)
				svc.Config = config
			}

			client := newHelpersTestAzdClient(t, &helpersProjectServer{
				project: &azdext.ProjectConfig{
					Path: t.TempDir(),
					Services: map[string]*azdext.ServiceConfig{
						svc.Name: svc,
					},
				},
			}, &helpersPromptServer{})

			err := runEndpointUpdate(
				t.Context(),
				client,
				&endpointUpdateFlags{name: svc.Name},
				&azdext.ExtensionContext{NoPrompt: true},
			)

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, tt.wantCode, localErr.Code)
			require.Equal(t, tt.wantSuggestion, localErr.Suggestion)
		})
	}
}

func TestEndpointUpdateRejectsSupportedNonHostedKinds(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
	}{
		{
			name: "prompt",
			values: map[string]any{
				"kind": "prompt", "name": "prompt-agent", "model": "gpt-5-mini", "instructions": "Help.",
			},
		},
		{
			name: "voice",
			values: map[string]any{
				"kind": "voice", "name": "voice-agent", "model": map[string]any{"id": "gpt-realtime"},
			},
		},
		{
			name: "workflow",
			values: map[string]any{
				"kind": "workflow", "name": "workflow-agent",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props, err := structpb.NewStruct(tt.values)
			require.NoError(t, err)
			svc := &azdext.ServiceConfig{
				Name:                 tt.name,
				Host:                 AiAgentHost,
				AdditionalProperties: props,
			}
			client := newHelpersTestAzdClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(),
				Services: map[string]*azdext.ServiceConfig{
					svc.Name: svc,
				},
			}}, &helpersPromptServer{})

			err = runEndpointUpdate(
				t.Context(),
				client,
				&endpointUpdateFlags{name: svc.Name},
				&azdext.ExtensionContext{NoPrompt: true},
			)

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeUnsupportedAgentKind, localErr.Code)
			require.Contains(t, localErr.Message, "endpoint update")
			require.Contains(t, localErr.Suggestion, "only to hosted agents")
		})
	}
}

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
	agentDef, _, _, err := project.LoadHostedAgentDefinition(svc, projectRoot)
	require.NoError(t, err)
	serviceConfig, err := project.LoadServiceTargetAgentConfig(svc)
	require.NoError(t, err)

	profile, err := project.ResolveActivityProfileWithSettings(agentDef, serviceConfig.Activity)
	require.NoError(t, err)
	require.Equal(t, project.ActivityUseCaseDigitalWorker, profile.UseCase)
}

func TestEnsureEndpointAuthSchemeForProfile_DigitalWorkerPreservesExplicitRbac(t *testing.T) {
	endpoint := &agent_api.AgentEndpoint{
		Protocols: []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolResponses},
		AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
			{Type: agent_api.AgentEndpointAuthSchemeEntra},
			{Type: agent_api.AgentEndpointAuthSchemeBotServiceRbac},
		},
	}

	project.EnsureActivityEndpointAuthSchemeForProfile(endpoint, project.ActivityProfile{
		IsActivity: true,
		UseCase:    project.ActivityUseCaseDigitalWorker,
	})

	require.Contains(t, endpoint.Protocols, agent_api.AgentEndpointProtocolActivity)
	assert.Equal(t, []agent_api.AgentEndpointAuthorizationScheme{
		{Type: agent_api.AgentEndpointAuthSchemeEntra},
		{Type: agent_api.AgentEndpointAuthSchemeBotServiceRbac},
	}, endpoint.AuthorizationSchemes)
}

func TestEnsureEndpointAuthSchemeForProfile_SimplePreservesExplicitTenant(t *testing.T) {
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
		{Type: agent_api.AgentEndpointAuthSchemeBotServiceTenant},
	}, endpoint.AuthorizationSchemes)
}

func TestEnsureEndpointAuthSchemeForProfile_DigitalWorkerUsesServiceDefaultWhenOmitted(t *testing.T) {
	endpoint := &agent_api.AgentEndpoint{}

	project.EnsureActivityEndpointAuthSchemeForProfile(endpoint, project.ActivityProfile{
		IsActivity: true,
		UseCase:    project.ActivityUseCaseDigitalWorker,
	})

	assert.Contains(t, endpoint.Protocols, agent_api.AgentEndpointProtocolActivity)
	assert.Empty(t, endpoint.AuthorizationSchemes)
}

func TestEnsureEndpointAuthSchemeForProfile_SimpleUsesServiceDefaultWhenOmitted(t *testing.T) {
	endpoint := &agent_api.AgentEndpoint{}

	project.EnsureActivityEndpointAuthSchemeForProfile(endpoint, project.ActivityProfile{
		IsActivity: true,
		UseCase:    project.ActivityUseCaseSimple,
	})

	assert.Contains(t, endpoint.Protocols, agent_api.AgentEndpointProtocolActivity)
	assert.Empty(t, endpoint.AuthorizationSchemes)
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

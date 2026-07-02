// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestIsActivityProtocol(t *testing.T) {
	tests := []struct {
		name string
		ca   agent_yaml.ContainerAgent
		want bool
	}{
		{
			name: "container activity_protocol",
			ca: agent_yaml.ContainerAgent{
				Protocols: []agent_yaml.ProtocolVersionRecord{
					{Protocol: "activity_protocol", Version: "v1"},
				},
			},
			want: true,
		},
		{
			name: "endpoint activity protocol",
			ca: agent_yaml.ContainerAgent{
				AgentEndpoint: &agent_yaml.AgentEndpoint{
					Protocols: []string{"activity"},
				},
			},
			want: true,
		},
		{
			name: "activity_protocol with surrounding whitespace",
			ca: agent_yaml.ContainerAgent{
				Protocols: []agent_yaml.ProtocolVersionRecord{
					{Protocol: " activity_protocol ", Version: "v1"},
				},
			},
			want: true,
		},
		{
			name: "responses protocol only",
			ca: agent_yaml.ContainerAgent{
				Protocols: []agent_yaml.ProtocolVersionRecord{
					{Protocol: "responses", Version: "2.0.0"},
				},
				AgentEndpoint: &agent_yaml.AgentEndpoint{
					Protocols: []string{"responses"},
				},
			},
			want: false,
		},
		{
			name: "empty definition",
			ca:   agent_yaml.ContainerAgent{},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsActivityProtocol(tc.ca); got != tc.want {
				t.Errorf("IsActivityProtocol() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveActivityProfile(t *testing.T) {
	t.Run("activity agent resolves simple", func(t *testing.T) {
		ca := agent_yaml.ContainerAgent{
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "activity_protocol", Version: "v1"},
			},
		}
		got := ResolveActivityProfile(ca)
		if !got.IsActivity {
			t.Fatalf("expected IsActivity=true")
		}
		if got.UseCase != ActivityUseCaseSimple {
			t.Errorf("UseCase = %q, want %q", got.UseCase, ActivityUseCaseSimple)
		}
	})

	t.Run("non-activity agent resolves empty profile", func(t *testing.T) {
		ca := agent_yaml.ContainerAgent{
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			},
		}
		got := ResolveActivityProfile(ca)
		if got.IsActivity {
			t.Fatalf("expected IsActivity=false")
		}
		if got.UseCase != "" {
			t.Errorf("UseCase = %q, want empty", got.UseCase)
		}
	})
}

// TestActivityDeclarationSurvivesInitRoundTrip locks the behaviour that
// `azd ai agent init` preserves the activity-protocol declaration (container
// protocols + agent_endpoint) when it moves an agent definition into azure.yaml
// service properties. Without this, postdeploy's activity gate would never fire
// on an init-generated project. It guards against a future refactor silently
// dropping AgentEndpoint/Protocols from the inline round-trip.
func TestActivityDeclarationSurvivesInitRoundTrip(t *testing.T) {
	src := agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindHosted,
			Name: "echo28ju3pm",
		},
		Protocols: []agent_yaml.ProtocolVersionRecord{
			{Protocol: "activity_protocol", Version: "v1"},
		},
		AgentEndpoint: &agent_yaml.AgentEndpoint{
			Protocols: []string{"activity"},
			AuthorizationSchemes: []agent_yaml.AuthorizationScheme{
				{Type: "BotServiceRbac"},
			},
		},
	}

	props, err := AgentDefinitionToServiceProperties(src, nil)
	require.NoError(t, err)

	svc := &azdext.ServiceConfig{
		Name:                 "echo28ju3pm",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}

	got, isHosted, found, _, err := AgentDefinitionFromService(svc)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, isHosted)

	require.True(t, IsActivityProtocol(got), "activity declaration must survive the round-trip")
	require.True(t, ResolveActivityProfile(got).IsActivity)

	require.Len(t, got.Protocols, 1)
	require.Equal(t, "activity_protocol", got.Protocols[0].Protocol)
	require.NotNil(t, got.AgentEndpoint)
	require.Equal(t, []string{"activity"}, got.AgentEndpoint.Protocols)
	require.Len(t, got.AgentEndpoint.AuthorizationSchemes, 1)
	require.Equal(t, "BotServiceRbac", got.AgentEndpoint.AuthorizationSchemes[0].Type)
}

// TestActivityAgentEndpoint pins the endpoint declaration that `azd init` from
// local code injects for an activity_protocol agent. It must match the
// manifest-based shape (friendly "activity" protocol guarded by BotServiceRbac)
// so the two init paths produce identical azure.yaml and both satisfy deploy.
func TestActivityAgentEndpoint(t *testing.T) {
	ep := ActivityAgentEndpoint()
	require.NotNil(t, ep)
	require.Equal(t, []string{"activity"}, ep.Protocols)
	require.Len(t, ep.AuthorizationSchemes, 1)
	require.Equal(t, "BotServiceRbac", ep.AuthorizationSchemes[0].Type)

	// A definition assembled the way init_from_code does (protocol record +
	// injected endpoint) must be recognized as an activity agent.
	ca := agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindHosted, Name: "echo"},
		Protocols:       []agent_yaml.ProtocolVersionRecord{{Protocol: "activity_protocol", Version: "v1"}},
		AgentEndpoint:   ActivityAgentEndpoint(),
	}
	require.True(t, IsActivityProtocol(ca))
	require.Equal(t, ActivityUseCaseSimple, ResolveActivityProfile(ca).UseCase)
}

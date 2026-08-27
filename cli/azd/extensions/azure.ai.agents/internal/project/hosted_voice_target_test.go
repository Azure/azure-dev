// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestResolveHostedVoiceTarget(t *testing.T) {
	targetProps, err := AgentDefinitionToServiceProperties(agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindHosted, Name: "remote-target"},
	}, nil)
	require.NoError(t, err)
	target := &azdext.ServiceConfig{
		Name:                 "voice-target",
		Host:                 foundryAgentHost,
		AdditionalProperties: targetProps,
	}
	wrapper := &azdext.ServiceConfig{
		Name: "voice-wrapper",
		Host: foundryAgentHost,
		Uses: []string{"voice-target"},
	}
	projectEndpoint := "https://account.services.ai.azure.com/api/projects/project"
	env := map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT":                  projectEndpoint,
		"AGENT_VOICE_TARGET_NAME":                   "remote-target",
		"AGENT_VOICE_TARGET_VERSION":                "4",
		envkey.AgentProjectEndpoint("voice-target"): projectEndpoint,
	}

	resolved, err := resolveHostedVoiceTarget(
		wrapper,
		&agent_yaml.VoiceTargetAgent{Service: "voice-target", Version: "deployed"},
		map[string]*azdext.ServiceConfig{"voice-target": target},
		env,
		t.TempDir(),
	)
	require.NoError(t, err)
	require.Equal(t, "remote-target", resolved.AgentName)
	require.Equal(t, "4", resolved.AgentVersion)
}

func TestResolveHostedVoiceTargetRequiresUses(t *testing.T) {
	wrapper := &azdext.ServiceConfig{Name: "voice-wrapper", Host: foundryAgentHost}
	_, err := resolveHostedVoiceTarget(
		wrapper,
		&agent_yaml.VoiceTargetAgent{Service: "voice-target"},
		map[string]*azdext.ServiceConfig{},
		map[string]string{},
		t.TempDir(),
	)
	require.ErrorContains(t, err, "uses list")
}

func TestResolveHostedVoiceTargetSupportsLegacyEndpointMarker(t *testing.T) {
	targetProps, err := AgentDefinitionToServiceProperties(agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindHosted, Name: "remote-target"},
	}, nil)
	require.NoError(t, err)
	target := &azdext.ServiceConfig{Name: "voice-target", Host: foundryAgentHost, AdditionalProperties: targetProps}
	wrapper := &azdext.ServiceConfig{Name: "voice-wrapper", Host: foundryAgentHost, Uses: []string{"voice-target"}}
	projectEndpoint := "https://account.services.ai.azure.com/api/projects/project"
	resolved, err := resolveHostedVoiceTarget(
		wrapper,
		&agent_yaml.VoiceTargetAgent{Service: "voice-target"},
		map[string]*azdext.ServiceConfig{"voice-target": target},
		map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":    projectEndpoint,
			"AGENT_VOICE_TARGET_NAME":     "remote-target",
			"AGENT_VOICE_TARGET_VERSION":  "4",
			"AGENT_VOICE_TARGET_ENDPOINT": projectEndpoint + "/agents/remote-target/versions/4",
		},
		t.TempDir(),
	)
	require.NoError(t, err)
	require.Equal(t, projectEndpoint, resolved.ProjectEndpoint)
}

func TestResolveHostedVoiceTargetRejectsDifferentProject(t *testing.T) {
	targetProps, err := AgentDefinitionToServiceProperties(agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindHosted, Name: "remote-target"},
	}, nil)
	require.NoError(t, err)
	target := &azdext.ServiceConfig{Name: "voice-target", Host: foundryAgentHost, AdditionalProperties: targetProps}
	wrapper := &azdext.ServiceConfig{Name: "voice-wrapper", Host: foundryAgentHost, Uses: []string{"voice-target"}}
	_, err = resolveHostedVoiceTarget(
		wrapper,
		&agent_yaml.VoiceTargetAgent{Service: "voice-target"},
		map[string]*azdext.ServiceConfig{"voice-target": target},
		map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":                  "https://account.services.ai.azure.com/api/projects/current",
			"AGENT_VOICE_TARGET_NAME":                   "remote-target",
			"AGENT_VOICE_TARGET_VERSION":                "4",
			envkey.AgentProjectEndpoint("voice-target"): "https://account.services.ai.azure.com/api/projects/other",
		},
		t.TempDir(),
	)
	require.ErrorContains(t, err, "different Foundry project")
}

func TestValidateHostedVoiceTarget(t *testing.T) {
	err := validateHostedVoiceTargetVersion(&agent_api.AgentVersionObject{
		Name:    "remote-target",
		Version: "4",
		Status:  "active",
		Metadata: map[string]string{
			"voiceLiveCompatible":   "true",
			"bridgeProtocolVersion": "1.0",
		},
		Definition: map[string]any{
			"kind": "hosted",
			"protocol_versions": []any{map[string]any{
				"protocol": "invocations_ws",
				"version":  "1.0.0",
			}},
		},
	})
	require.NoError(t, err)
}

func TestValidateHostedVoiceTargetRejectsIncompatibleProtocol(t *testing.T) {
	err := validateHostedVoiceTargetVersion(&agent_api.AgentVersionObject{
		Status: "active",
		Metadata: map[string]string{
			"voiceLiveCompatible":   "true",
			"bridgeProtocolVersion": "1.0",
		},
		Definition: map[string]any{
			"kind": "hosted",
			"protocol_versions": []any{map[string]any{
				"protocol": "responses",
				"version":  "2.0.0",
			}},
		},
	})
	require.ErrorContains(t, err, "invocations_ws/1.0.0")
}

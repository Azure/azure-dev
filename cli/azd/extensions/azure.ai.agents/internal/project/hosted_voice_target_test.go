// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestFetchAndValidateHostedVoiceTarget(t *testing.T) {
	t.Parallel()
	target := &hostedVoiceTarget{AgentName: "target", AgentVersion: "7"}
	called := false
	err := fetchAndValidateHostedVoiceTarget(t.Context(), target, func(
		_ context.Context, name, version, apiVersion string,
	) (*agent_api.AgentVersionObject, error) {
		called = true
		require.Equal(t, "target", name)
		require.Equal(t, "7", version)
		require.Equal(t, agent_api.AgentEndpointAPIVersion, apiVersion)
		return compatibleHostedVoiceVersion(), nil
	})
	require.NoError(t, err)
	require.True(t, called)
}

func TestFetchAndValidateHostedVoiceTargetClassifiesAzureFailure(t *testing.T) {
	t.Parallel()
	err := fetchAndValidateHostedVoiceTarget(t.Context(), &hostedVoiceTarget{}, func(
		context.Context, string, string, string,
	) (*agent_api.AgentVersionObject, error) {
		return nil, &azcore.ResponseError{StatusCode: http.StatusForbidden}
	})
	var serviceErr *azdext.ServiceError
	require.ErrorAs(t, err, &serviceErr)
	require.Contains(t, serviceErr.Message, "getting hosted voice target version")
}

func TestFetchAndValidateHostedVoiceTargetClassifiesCompatibilityFailure(t *testing.T) {
	t.Parallel()
	err := fetchAndValidateHostedVoiceTarget(t.Context(), &hostedVoiceTarget{}, func(
		context.Context, string, string, string,
	) (*agent_api.AgentVersionObject, error) {
		version := compatibleHostedVoiceVersion()
		version.Status = "failed"
		return version, nil
	})
	var localErr *azdext.LocalError
	require.True(t, errors.As(err, &localErr))
	require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
}

func TestHostedVoiceTargetMarkers(t *testing.T) {
	t.Parallel()
	target := &hostedVoiceTarget{AgentName: "target", AgentVersion: "7"}
	require.Equal(t, "target", hostedVoiceTargetName(target))
	require.Equal(t, "7", hostedVoiceTargetVersion(target))
	require.Empty(t, hostedVoiceTargetName(nil))
	require.Empty(t, hostedVoiceTargetVersion(nil))
}

func compatibleHostedVoiceVersion() *agent_api.AgentVersionObject {
	return &agent_api.AgentVersionObject{
		Name:    "target",
		Version: "7",
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
	}
}

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

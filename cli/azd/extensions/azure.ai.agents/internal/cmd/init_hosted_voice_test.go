// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/stretchr/testify/require"
)

func TestHostedVoiceManifestTarget(t *testing.T) {
	t.Parallel()
	metadata := map[string]any{
		"voiceLiveCompatible":   "true",
		"bridgeProtocolVersion": "1.0",
	}
	manifest := &agent_yaml.AgentManifest{Template: agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindHosted, Name: "voice-target", Metadata: &metadata,
		},
		Protocols: []agent_yaml.ProtocolVersionRecord{{Protocol: "invocations_ws", Version: "1.0.0"}},
	}}
	target, compatible, err := hostedVoiceManifestTarget(manifest)
	require.NoError(t, err)
	require.True(t, compatible)
	require.Equal(t, "voice-target", target.Name)
}

func TestHostedVoiceManifestTargetRejectsIncompatibleManifest(t *testing.T) {
	t.Parallel()
	metadata := map[string]any{
		"voiceLiveCompatible":   "true",
		"bridgeProtocolVersion": "2.0",
	}
	manifest := &agent_yaml.AgentManifest{Template: agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindHosted, Name: "voice-target", Metadata: &metadata,
		},
		Protocols: []agent_yaml.ProtocolVersionRecord{{Protocol: "invocations_ws", Version: "1.0.0"}},
	}}
	_, compatible, err := hostedVoiceManifestTarget(manifest)
	require.NoError(t, err)
	require.False(t, compatible)
}

func TestHostedVoiceManifestTargetRejectsGenericInvocationsWS(t *testing.T) {
	t.Parallel()
	manifest := &agent_yaml.AgentManifest{Template: agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindHosted, Name: "generic"},
		Protocols:       []agent_yaml.ProtocolVersionRecord{{Protocol: "invocations_ws", Version: "1.0.0"}},
	}}
	_, compatible, err := hostedVoiceManifestTarget(manifest)
	require.NoError(t, err)
	require.False(t, compatible)
}

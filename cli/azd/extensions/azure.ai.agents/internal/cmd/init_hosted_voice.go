// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"gopkg.in/yaml.v3"
)

func hostedVoiceManifestTarget(manifest *agent_yaml.AgentManifest) (*agent_yaml.ContainerAgent, bool, error) {
	if manifest == nil {
		return nil, false, nil
	}
	templateYAML, err := yaml.Marshal(manifest.Template)
	if err != nil {
		return nil, false, fmt.Errorf("marshaling hosted voice manifest template: %w", err)
	}
	var target agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(templateYAML, &target); err != nil {
		return nil, false, nil
	}
	if target.Kind != agent_yaml.AgentKindHosted {
		return nil, false, nil
	}
	compatibleProtocol := false
	for _, protocol := range target.Protocols {
		if protocol.Protocol == "invocations_ws" && protocol.Version == "1.0.0" {
			compatibleProtocol = true
			break
		}
	}
	if !compatibleProtocol || target.Metadata == nil {
		return nil, false, nil
	}
	metadata := *target.Metadata
	voiceCompatibleValue, voiceCompatibleIsString := metadata["voiceLiveCompatible"].(string)
	bridgeVersionValue, bridgeVersionIsString := metadata["bridgeProtocolVersion"].(string)
	if !voiceCompatibleIsString || !bridgeVersionIsString {
		return nil, false, nil
	}
	voiceCompatible := strings.EqualFold(strings.TrimSpace(voiceCompatibleValue), "true")
	bridgeVersion := strings.TrimSpace(bridgeVersionValue)
	if !voiceCompatible || bridgeVersion != "1.0" {
		return nil, false, nil
	}
	return &target, true, nil
}

func applyHostedVoiceManifestKind(flags *initFlags, manifest *agent_yaml.AgentManifest) error {
	explicitHostedVoice := flags != nil && strings.EqualFold(flags.kind, kindFlagHostedVoice)
	_, compatible, err := hostedVoiceManifestTarget(manifest)
	if err != nil {
		return err
	}
	if explicitHostedVoice && !compatible {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			"the detected manifest is not compatible with Hosted Voice",
			"use a hosted manifest with invocations_ws/1.0.0, voiceLiveCompatible=true, and bridgeProtocolVersion=1.0",
		)
	}
	if compatible {
		flags.kind = kindFlagHostedVoice
	}
	return nil
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

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
	voiceCompatible := strings.EqualFold(strings.TrimSpace(fmt.Sprint(metadata["voiceLiveCompatible"])), "true")
	bridgeVersion := strings.TrimSpace(fmt.Sprint(metadata["bridgeProtocolVersion"]))
	if !voiceCompatible || bridgeVersion != "1.0" {
		return nil, false, nil
	}
	return &target, true, nil
}

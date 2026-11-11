// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"slices"
	"strings"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

// isA2AOnlyRunTarget checks container protocols, not remote endpoint enablement.
func isA2AOnlyRunTarget(def *agent_yaml.ContainerAgent) bool {
	return protocolOnlyRunTarget(def) == "a2a"
}

func protocolOnlyRunTarget(def *agent_yaml.ContainerAgent) string {
	if def == nil || len(def.Protocols) == 0 {
		return ""
	}
	protocol := strings.TrimSpace(def.Protocols[0].Protocol)
	if protocol != "a2a" && protocol != nextstep.ProtocolVoiceConversationEngine {
		return ""
	}
	for _, declared := range def.Protocols {
		if strings.TrimSpace(declared.Protocol) != protocol {
			return ""
		}
	}
	return protocol
}

// resolveRunClientProfile keeps A2A-only containers out of Activity client setup.
func resolveRunClientProfile(def *agent_yaml.ContainerAgent) (activityRunProfile, bool) {
	if protocolOnlyRunTarget(def) != "" {
		return activityRunProfile{}, true
	}
	return resolveActivityRunProfile(def), false
}

// withResolvedRunTarget preserves the run target if best-effort state reloading fails or disagrees.
func withResolvedRunTarget(state *nextstep.State, serviceName, protocol string) *nextstep.State {
	if protocol == "" {
		return state
	}
	resolved := nextstep.State{}
	if state != nil {
		resolved = *state
	}
	resolved.Services = slices.Clone(resolved.Services)
	for i := range resolved.Services {
		if resolved.Services[i].Name == serviceName || serviceName == "" && len(resolved.Services) == 1 {
			resolved.Services[i].Protocol = protocol
			resolved.Services[i].MultiProtocol = false
			return &resolved
		}
	}
	resolved.Services = append(resolved.Services, nextstep.ServiceState{
		Name: serviceName, Protocol: protocol,
	})
	return &resolved
}

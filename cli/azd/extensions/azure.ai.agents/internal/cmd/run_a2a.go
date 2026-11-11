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
	return isProtocolOnlyRunTarget(def, "a2a")
}

func isProtocolOnlyRunTarget(def *agent_yaml.ContainerAgent, protocol string) bool {
	if def == nil {
		return false
	}
	for _, declared := range def.Protocols {
		if strings.TrimSpace(declared.Protocol) != protocol {
			return false
		}
	}
	return len(def.Protocols) > 0
}

// resolveRunClientProfile keeps A2A-only containers out of Activity client setup.
func resolveRunClientProfile(def *agent_yaml.ContainerAgent) (activityRunProfile, bool) {
	if isA2AOnlyRunTarget(def) || isProtocolOnlyRunTarget(def, "voice_conversation_engine") {
		return activityRunProfile{}, true
	}
	return resolveActivityRunProfile(def), false
}

// withResolvedRunTarget preserves the run target if best-effort state reloading fails or disagrees.
func withResolvedRunTarget(state *nextstep.State, serviceName string, a2aOnly bool) *nextstep.State {
	if !a2aOnly {
		return state
	}
	resolved := nextstep.State{}
	if state != nil {
		resolved = *state
	}
	resolved.Services = slices.Clone(resolved.Services)
	for i := range resolved.Services {
		if resolved.Services[i].Name == serviceName || serviceName == "" && len(resolved.Services) == 1 {
			resolved.Services[i].Protocol = nextstep.ProtocolA2A
			resolved.Services[i].MultiProtocol = false
			return &resolved
		}
	}
	resolved.Services = append(resolved.Services, nextstep.ServiceState{
		Name: serviceName, Protocol: nextstep.ProtocolA2A,
	})
	return &resolved
}

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"slices"
	"strings"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

func protocolOnlyRunTarget(def *agent_yaml.ContainerAgent) string {
	if def == nil || len(def.Protocols) == 0 {
		return ""
	}
	conversationEngine, onlyVoiceProtocols, onlyA2A := false, true, true
	for _, declared := range def.Protocols {
		protocol := strings.TrimSpace(declared.Protocol)
		onlyA2A = onlyA2A && protocol == nextstep.ProtocolA2A
		switch protocol {
		case nextstep.ProtocolVoiceConversationEngine:
			conversationEngine = true
		case nextstep.ProtocolInvocationsWS:
		default:
			onlyVoiceProtocols = false
		}
	}
	if conversationEngine && onlyVoiceProtocols {
		return nextstep.ProtocolVoiceConversationEngine
	}
	if onlyA2A {
		return nextstep.ProtocolA2A
	}
	return ""
}

// resolveRunClientProfile suppresses clients for A2A-only and CE/WebSocket-only targets.
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

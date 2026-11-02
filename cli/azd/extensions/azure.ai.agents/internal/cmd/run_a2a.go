// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"slices"
	"strings"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

// isA2AOnlyRunTarget suppresses Inspector only when every advertised container
// protocol is A2A. Mixed definitions retain their existing local-client route.
func isA2AOnlyRunTarget(def *agent_yaml.ContainerAgent) bool {
	if def == nil {
		return false
	}
	for _, protocol := range def.Protocols {
		if strings.TrimSpace(protocol.Protocol) != "a2a" {
			return false
		}
	}
	return len(def.Protocols) > 0
}

// Container protocols determine the local client, independently of hosted
// AgentEndpoint enablement. A2A-only containers must not get activity setup.
func resolveRunClientProfile(def *agent_yaml.ContainerAgent) (activityRunProfile, bool) {
	if isA2AOnlyRunTarget(def) {
		return activityRunProfile{}, true
	}
	return resolveActivityRunProfile(def), false
}

// withResolvedRunTarget preserves the already-resolved A2A-only run target when
// best-effort next-step assembly cannot reload it or reads conflicting protocols.
// Copy Services so the assembled snapshot and unrelated services stay unchanged.
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

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

// ActivityUseCase identifies the Teams hosting/auth model an activity-protocol
// agent targets. Phase 1 supports only the "simple" model (an Azure Bot bound to
// the agent instance identity). The digital-worker model is a Phase 2 addition.
type ActivityUseCase string

const (
	// ActivityUseCaseSimple is the default single-tenant Teams bot model whose
	// msaAppId is the agent instance identity client id.
	ActivityUseCaseSimple ActivityUseCase = "simple"
	// ActivityUseCaseDigitalWorker is the blueprint + federated-identity model
	// (Phase 2). Not yet resolved by ResolveActivityProfile.
	ActivityUseCaseDigitalWorker ActivityUseCase = "digital_worker"
)

// ActivityProfile summarizes the activity-protocol characteristics of a hosted
// agent definition. It is the single gate that keeps all Teams/bot-specific
// behaviour off the path of non-activity agents: when IsActivity is false the
// native provision/deploy flow is completely unchanged.
type ActivityProfile struct {
	// IsActivity reports whether the agent opts into the Activity protocol.
	IsActivity bool
	// UseCase is the resolved Teams hosting model. Only meaningful when
	// IsActivity is true. Phase 1 always resolves ActivityUseCaseSimple.
	UseCase ActivityUseCase
}

// IsActivityProtocol reports whether a hosted agent definition opts into the
// Activity protocol, either through a container-level activity_protocol entry or
// an agent_endpoint that advertises the friendly "activity" protocol.
func IsActivityProtocol(ca agent_yaml.ContainerAgent) bool {
	for _, p := range ca.Protocols {
		if agent_api.AgentProtocol(strings.TrimSpace(p.Protocol)) == agent_api.AgentProtocolActivityProtocol {
			return true
		}
	}
	if ca.AgentEndpoint != nil {
		for _, p := range ca.AgentEndpoint.Protocols {
			if agent_api.AgentEndpointProtocol(strings.TrimSpace(p)) == agent_api.AgentEndpointProtocolActivity {
				return true
			}
		}
	}
	return false
}

// ResolveActivityProfile derives the ActivityProfile for a hosted agent
// definition. Phase 1 always resolves the simple use case for activity agents;
// digital-worker detection is a Phase 2 addition.
func ResolveActivityProfile(ca agent_yaml.ContainerAgent) ActivityProfile {
	if !IsActivityProtocol(ca) {
		return ActivityProfile{}
	}
	return ActivityProfile{IsActivity: true, UseCase: ActivityUseCaseSimple}
}

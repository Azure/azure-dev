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
// behavior off the path of non-activity agents: when IsActivity is false the
// native provision/deploy flow is completely unchanged.
type ActivityProfile struct {
	// IsActivity reports whether the agent opts into the Activity protocol.
	IsActivity bool
	// UseCase is the resolved Teams hosting model. Only meaningful when
	// IsActivity is true. Phase 1 always resolves ActivityUseCaseSimple.
	UseCase ActivityUseCase
}

// IsActivityProtocol reports whether a hosted agent definition opts into the
// Activity protocol, either through a container-level activity entry or
// an agent_endpoint that advertises the friendly "activity" protocol.
func IsActivityProtocol(ca agent_yaml.ContainerAgent) bool {
	for _, p := range ca.Protocols {
		if agent_api.IsActivityProtocolName(agent_api.AgentProtocol(strings.TrimSpace(p.Protocol))) {
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

// ActivityAgentEndpoint returns the agent_endpoint declaration an activity agent
// requires: the friendly "activity" protocol guarded by the BotServiceRbac
// authorization scheme. `azd init` uses it to make an activity agent scaffolded
// from local code carry the exact same declaration as one initialized from a
// manifest, so the generated azure.yaml is identical and `azd deploy` provisions
// the Azure Bot connector. Phase 1 covers the simple use case.
func ActivityAgentEndpoint() *agent_yaml.AgentEndpoint {
	return &agent_yaml.AgentEndpoint{
		Protocols: []string{string(agent_api.AgentEndpointProtocolActivity)},
		AuthorizationSchemes: []agent_yaml.AuthorizationScheme{
			{Type: string(agent_api.AgentEndpointAuthSchemeBotServiceRbac)},
		},
	}
}

// ComposeActivityAgentEndpoint folds the Activity endpoint requirements into an
// agent's agent_endpoint declaration instead of overwriting it, so the Activity
// protocol can coexist with the other protocols the agent speaks
// (responses/invocations/...). Activity is not exclusive: the platform models
// every protocol as a sibling per-protocol entry on the same endpoint, and the
// endpoint carries a list of protocols and a list of authorization schemes. This
// helper therefore (1) advertises every selected protocol on the endpoint,
// normalizing the legacy "activity_protocol" spelling to the canonical
// "activity", and (2) ensures the BotServiceRbac scheme Activity requires is
// present without dropping any scheme already declared. For a pure-activity
// agent the result is identical to ActivityAgentEndpoint(): protocols=["activity"]
// guarded by BotServiceRbac. No-op inputs (nil existing endpoint) start fresh.
func ComposeActivityAgentEndpoint(
	existing *agent_yaml.AgentEndpoint,
	protocols []agent_yaml.ProtocolVersionRecord,
) *agent_yaml.AgentEndpoint {
	ep := existing
	if ep == nil {
		ep = &agent_yaml.AgentEndpoint{}
	}

	// Advertise every selected protocol on the endpoint (dedup, preserve order),
	// normalizing activity_protocol -> activity so the endpoint carries the
	// canonical wire value.
	seen := make(map[string]bool, len(ep.Protocols))
	for _, p := range ep.Protocols {
		seen[strings.TrimSpace(p)] = true
	}
	for _, p := range protocols {
		name := strings.TrimSpace(p.Protocol)
		if name == "" {
			continue
		}
		if agent_api.IsActivityProtocolName(agent_api.AgentProtocol(name)) {
			name = string(agent_api.AgentEndpointProtocolActivity)
		}
		if seen[name] {
			continue
		}
		ep.Protocols = append(ep.Protocols, name)
		seen[name] = true
	}

	// Ensure the BotServiceRbac scheme Activity requires is present, keeping any
	// scheme already declared for the other protocols.
	for _, s := range ep.AuthorizationSchemes {
		if s.Type == string(agent_api.AgentEndpointAuthSchemeBotServiceRbac) {
			return ep
		}
	}
	ep.AuthorizationSchemes = append(ep.AuthorizationSchemes, agent_yaml.AuthorizationScheme{
		Type: string(agent_api.AgentEndpointAuthSchemeBotServiceRbac),
	})
	return ep
}

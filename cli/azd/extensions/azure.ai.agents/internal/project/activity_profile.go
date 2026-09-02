// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"slices"
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
	// ActivityUseCaseDigitalWorker is the blueprint + federated-identity model.
	ActivityUseCaseDigitalWorker ActivityUseCase = "digital_worker"
)

var supportedDigitalWorkerAccessBoundaries = map[string]struct{}{
	"read.1on1.developers":   {},
	"write.1on1.developers":  {},
	"read.group.developers":  {},
	"write.group.developers": {},
}

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

// ResolveDeployedActivityProfile reconciles local authoring intent with the
// service-side Digital Worker classification, which is authoritative after
// deployment.
func ResolveDeployedActivityProfile(
	local ActivityProfile,
	digitalWorkerType agent_api.DigitalWorkerType,
) (ActivityProfile, error) {
	switch digitalWorkerType {
	case agent_api.DigitalWorkerTypeM365:
		return ActivityProfile{IsActivity: true, UseCase: ActivityUseCaseDigitalWorker}, nil
	case "":
		if local.UseCase == ActivityUseCaseDigitalWorker {
			return ActivityProfile{}, fmt.Errorf(
				"agent is configured with activity.digitalWorkerType=m365 but the deployed version " +
					"has no digital_worker_type",
			)
		}
		return local, nil
	default:
		return ActivityProfile{}, fmt.Errorf(
			"deployed agent has unsupported digital_worker_type %q", digitalWorkerType,
		)
	}
}

func digitalWorkerTypeMismatchSuggestion() string {
	return "delete and recreate the agent so its immutable digital_worker_type matches " +
		"activity.digitalWorkerType"
}

// EnsureActivityEndpointAuthSchemeForProfile preserves explicitly authored
// authorization schemes. Simple Activity agents receive BotServiceRbac when no
// scheme is supplied, while Digital Workers rely on the service default.
func EnsureActivityEndpointAuthSchemeForProfile(
	endpoint *agent_api.AgentEndpoint,
	profile ActivityProfile,
) {
	if endpoint == nil || !profile.IsActivity {
		return
	}

	ensureActivityEndpointProtocol(endpoint)
	if profile.UseCase == ActivityUseCaseDigitalWorker {
		return
	}

	EnsureActivityEndpointAuthScheme(endpoint, agent_api.AgentEndpointAuthSchemeBotServiceRbac)
}

// EnsureActivityEndpointAuthScheme ensures the Activity protocol is advertised and
// applies the supplied default authorization scheme only when the endpoint omits all schemes.
func EnsureActivityEndpointAuthScheme(
	endpoint *agent_api.AgentEndpoint,
	schemeType agent_api.AgentEndpointAuthorizationSchemeType,
) {
	ensureActivityEndpointProtocol(endpoint)

	if len(endpoint.AuthorizationSchemes) == 0 {
		endpoint.AuthorizationSchemes = append(
			endpoint.AuthorizationSchemes,
			agent_api.AgentEndpointAuthorizationScheme{Type: schemeType},
		)
	}
}

func ensureActivityEndpointProtocol(endpoint *agent_api.AgentEndpoint) {
	if !slices.Contains(endpoint.Protocols, agent_api.AgentEndpointProtocolActivity) {
		endpoint.Protocols = append(endpoint.Protocols, agent_api.AgentEndpointProtocolActivity)
	}
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

// ResolveActivityProfile preserves the simple default for callers that do not
// have service-level Activity settings.
func ResolveActivityProfile(ca agent_yaml.ContainerAgent) ActivityProfile {
	if !IsActivityProtocol(ca) {
		return ActivityProfile{}
	}
	return ActivityProfile{IsActivity: true, UseCase: ActivityUseCaseSimple}
}

// ResolveActivityProfileWithSettings derives the Digital Worker type configured
// on the azd service. A missing type preserves the existing simple Activity
// behavior. Publish fields are validated after command-line overrides are applied.
func ResolveActivityProfileWithSettings(
	ca agent_yaml.ContainerAgent,
	settings *ActivitySettings,
) (ActivityProfile, error) {
	profile := ResolveActivityProfile(ca)
	if settings == nil {
		return profile, nil
	}
	if strings.TrimSpace(string(settings.DigitalWorkerType)) == "" {
		return profile, nil
	}

	if !profile.IsActivity {
		return ActivityProfile{}, fmt.Errorf(
			"activity.digitalWorkerType requires an Activity-protocol hosted agent",
		)
	}

	switch settings.DigitalWorkerType {
	case agent_api.DigitalWorkerTypeM365:
		return ActivityProfile{IsActivity: true, UseCase: ActivityUseCaseDigitalWorker}, nil
	default:
		return ActivityProfile{}, fmt.Errorf(
			"activity.digitalWorkerType must be %q when specified", agent_api.DigitalWorkerTypeM365,
		)
	}
}

// ResolveActivityProfileForDeploy derives the Activity profile and eagerly
// validates publish settings when deploying an agent from azure.yaml.
func ResolveActivityProfileForDeploy(
	ca agent_yaml.ContainerAgent,
	settings *ActivitySettings,
) (ActivityProfile, error) {
	profile, err := ResolveActivityProfileWithSettings(ca, settings)
	if err != nil || settings == nil || settings.Publish == nil {
		return profile, err
	}

	if profile.UseCase != ActivityUseCaseDigitalWorker {
		if settings.Publish.OptionalPermissionScopes != nil || settings.Publish.AccessBoundaries != nil {
			return ActivityProfile{}, fmt.Errorf(
				"activity.publish.optionalPermissionScopes and activity.publish.accessBoundaries require " +
					"activity.digitalWorkerType=m365",
			)
		}
		return profile, nil
	}

	if err := ValidateDigitalWorkerPublishConfig(settings.Publish); err != nil {
		return ActivityProfile{}, err
	}
	return profile, nil
}

// ValidateDigitalWorkerPublishConfig validates fields that are sent only for
// Microsoft 365 Digital Worker publication.
func ValidateDigitalWorkerPublishConfig(publish *ActivityPublishConfig) error {
	publishScope := strings.TrimSpace(publish.PublishScope)
	if publishScope != "" && !strings.EqualFold(publishScope, "tenant") {
		return fmt.Errorf(
			"activity.publish.publishScope must be tenant for digital_worker (shared is not supported)",
		)
	}
	for i, permission := range publish.OptionalPermissionScopes {
		if strings.TrimSpace(permission.ResourceAppID) == "" {
			return fmt.Errorf("activity.publish.optionalPermissionScopes[%d].resourceAppId is required", i)
		}
		if len(permission.Scopes) == 0 {
			return fmt.Errorf("activity.publish.optionalPermissionScopes[%d].scopes must not be empty", i)
		}
		for j, scope := range permission.Scopes {
			if strings.TrimSpace(scope) == "" {
				return fmt.Errorf(
					"activity.publish.optionalPermissionScopes[%d].scopes[%d] must not be empty", i, j,
				)
			}
		}
	}
	if publish.AccessBoundaries != nil {
		if err := ValidateDigitalWorkerAccessBoundaries(*publish.AccessBoundaries); err != nil {
			return fmt.Errorf("activity.publish.accessBoundaries: %w", err)
		}
	}
	return nil
}

// ValidateDigitalWorkerAccessBoundaries validates the initial azd-supported
// developer-only boundary set.
func ValidateDigitalWorkerAccessBoundaries(boundaries []string) error {
	for _, boundary := range boundaries {
		if _, ok := supportedDigitalWorkerAccessBoundaries[strings.TrimSpace(boundary)]; !ok {
			return fmt.Errorf(
				"unsupported value %q; supported values are read.1on1.developers, "+
					"write.1on1.developers, read.group.developers, and write.group.developers",
				boundary,
			)
		}
	}
	return nil
}

// ComposeActivityAgentEndpoint folds the Activity endpoint requirements into an
// agent's agent_endpoint declaration instead of overwriting it, so the Activity
// protocol can coexist with the other protocols the agent speaks
// (responses/invocations/...). Activity is not exclusive: the platform models
// every protocol as a sibling per-protocol entry on the same endpoint, and the
// endpoint carries a list of protocols and a list of authorization schemes. This
// helper therefore advertises every selected protocol on the endpoint,
// normalizing the legacy "activity_protocol" spelling to the canonical
// "activity", and applies BotServiceRbac when authorization schemes are omitted.
// This init-time helper produces the Simple Activity shape; deploy-time Digital
// Worker defaults are handled separately. No-op inputs (nil existing endpoint)
// start fresh.
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

	if len(ep.AuthorizationSchemes) == 0 {
		ep.AuthorizationSchemes = append(ep.AuthorizationSchemes, agent_yaml.AuthorizationScheme{
			Type: string(agent_api.AgentEndpointAuthSchemeBotServiceRbac),
		})
	}
	return ep
}

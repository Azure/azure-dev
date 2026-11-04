// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"strings"

	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// lookupFold returns the value for key, matching the name the way
// encoding/json does when it decodes the same map into a struct: exact match
// first, then case-insensitively. Gates must use it so a differently cased
// property cannot be skipped by validation and still be decoded later.
func lookupFold(m map[string]any, key string) (any, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

// PreviewFeature identifies a resolved definition's capability.
// Ordinary definitions return an empty feature.
func PreviewFeature(definition map[string]any) featureflags.Feature {
	kind, _ := definition["kind"].(string)
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "prompt":
		harness, _ := definition["harness"].(map[string]any)
		harnessType, _ := harness["type"].(string)
		if agent_api.IsCopilotHarness(strings.TrimSpace(harnessType)) {
			return featureflags.ManagedHarness
		}
	case "voice", "prompt-voice":
		return featureflags.Voice
	case "hosted":
		// Manifests use protocols. Foundry API definitions use protocol_versions
		// or the legacy container_protocol_versions.
		for _, key := range []string{"protocols", "protocol_versions", "container_protocol_versions"} {
			value, _ := lookupFold(definition, key)
			protocols, _ := value.([]any)
			for _, entry := range protocols {
				protocol, _ := entry.(map[string]any)
				value, _ := lookupFold(protocol, "protocol")
				name, _ := value.(string)
				if strings.TrimSpace(name) == "invocations_ws" {
					return featureflags.Voice
				}
			}
		}
	}
	return featureflags.Feature{}
}

// RequireDefinitionFeatures checks the definition's preview opt-in.
func RequireDefinitionFeatures(definition map[string]any) error {
	yamlValue, _ := lookupFold(definition, "enableMemorySnapshot")
	apiValue, _ := lookupFold(definition, "enable_memory_snapshot")
	yamlSnapshot, _ := yamlValue.(bool)
	apiSnapshot, _ := apiValue.(bool)
	if yamlSnapshot || apiSnapshot {
		if err := featureflags.Require(featureflags.MemorySnapshot); err != nil {
			return err
		}
	}
	feature := PreviewFeature(definition)
	if feature.ID == "" {
		return nil
	}
	return featureflags.Require(feature)
}

// RequireAgentFeatures checks the inline or $ref definition.
func RequireAgentFeatures(
	service *azdext.ServiceConfig,
	projectRoot string,
) error {
	if service.GetHost() != foundryAgentHost {
		return nil
	}
	props := ServiceConfigProps(service)
	if props == nil {
		return nil
	}
	resolved, err := resolveServiceProps(props, service.GetName(), projectRoot)
	if err != nil {
		return err
	}
	return RequireDefinitionFeatures(resolved.AsMap())
}

// prepareGatedOperation adopts and resolves the service, then
// checks the preview for Package, Publish, and Deploy. Initialize,
// GetTargetResource, and Endpoints stay outside the gate so read and
// cleanup flows keep working.
func (p *AgentServiceTargetProvider) prepareGatedOperation(
	ctx context.Context, serviceConfig *azdext.ServiceConfig,
) error {
	if err := p.validateDeploymentServiceConfig(ctx, serviceConfig); err != nil {
		return err
	}
	return p.requirePreviewFeatures(ctx)
}

func (p *AgentServiceTargetProvider) requirePreviewFeatures(ctx context.Context) error {
	if props := ServiceConfigProps(p.serviceConfig); props != nil &&
		containsFileRef(props.AsMap()) {
		if err := p.loadProjectPath(ctx); err != nil {
			return err
		}
	}
	return RequireAgentFeatures(p.serviceConfig, p.projectPath)
}

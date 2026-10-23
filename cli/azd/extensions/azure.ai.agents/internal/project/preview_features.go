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
			protocols, _ := definition[key].([]any)
			for _, entry := range protocols {
				protocol, _ := entry.(map[string]any)
				name, _ := protocol["protocol"].(string)
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
	if err := p.adoptAndResolveServiceConfig(ctx, serviceConfig); err != nil {
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

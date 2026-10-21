// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
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
		if agent_api.IsCopilotHarness(harnessType) {
			return featureflags.ManagedHarness
		}
	case "voice", "prompt-voice":
		return featureflags.Voice
	case "hosted":
		protocols, _ := definition["protocols"].([]any)
		for _, entry := range protocols {
			protocol, _ := entry.(map[string]any)
			if protocol["protocol"] == "invocations_ws" {
				return featureflags.Voice
			}
		}
	}
	return featureflags.Feature{}
}

// RequireDefinitionFeatures checks the definition's preview opt-in.
// Environments include services in a manifest not yet adopted.
func RequireDefinitionFeatures(
	ctx context.Context, client *azdext.AzdClient, definition map[string]any,
	environments ...map[string]string,
) error {
	feature := PreviewFeature(definition)
	if feature.ID == "" {
		return nil
	}
	values := make([]string, 0, len(environments)+1)
	if env, ok := definition["env"].(map[string]any); ok {
		if value, found := env[feature.EnvVar]; found {
			values = append(values, fmt.Sprint(value))
		}
	}
	for _, env := range environments {
		values = append(values, env[feature.EnvVar])
	}
	return featureflags.Require(ctx, client, feature, values...)
}

// RequireAgentFeatures checks the inline or $ref definition.
func RequireAgentFeatures(
	ctx context.Context,
	client *azdext.AzdClient,
	service *azdext.ServiceConfig,
	projectRoot string,
	environments ...map[string]string,
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
	return RequireDefinitionFeatures(ctx, client, resolved.AsMap(), environments...)
}

func (p *AgentServiceTargetProvider) requirePreviewFeatures(ctx context.Context) error {
	if props := ServiceConfigProps(p.serviceConfig); props != nil &&
		containsFileRef(props.AsMap()) {
		if err := p.loadProjectPath(ctx); err != nil {
			return err
		}
	}
	return RequireAgentFeatures(ctx, p.azdClient, p.serviceConfig, p.projectPath)
}

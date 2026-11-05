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

// foldValues returns the values of every key that matches name
// case-insensitively. encoding/json decodes any of them into the same struct
// field, and a null or false under one spelling does not cancel a value under
// another, so gates must consider all of them, not just the first match.
func foldValues(m map[string]any, name string) []any {
	var values []any
	for k, v := range m {
		if strings.EqualFold(k, name) {
			values = append(values, v)
		}
	}
	return values
}

// hasFold reports whether any key matches name case-insensitively,
// including keys whose value is null.
func hasFold(m map[string]any, name string) bool {
	return len(foldValues(m, name)) > 0
}

// anyFoldTrue reports whether any spelling of name is set to true.
func anyFoldTrue(m map[string]any, name string) bool {
	for _, v := range foldValues(m, name) {
		if b, _ := v.(bool); b {
			return true
		}
	}
	return false
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
			for _, value := range foldValues(definition, key) {
				protocols, _ := value.([]any)
				for _, entry := range protocols {
					protocol, _ := entry.(map[string]any)
					for _, name := range foldValues(protocol, "protocol") {
						text, _ := name.(string)
						if strings.TrimSpace(text) == "invocations_ws" {
							return featureflags.Voice
						}
					}
				}
			}
		}
	}
	return featureflags.Feature{}
}

// RequireDefinitionFeatures checks the definition's preview opt-in.
func RequireDefinitionFeatures(definition map[string]any) error {
	if anyFoldTrue(definition, "enableMemorySnapshot") || anyFoldTrue(definition, "enable_memory_snapshot") {
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

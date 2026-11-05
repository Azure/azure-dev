// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"io"
	"strings"

	"azure.ai.projects/synthesis"
	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/phasetelemetry"
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
func RequireDefinitionFeatures(definition map[string]any, writer io.Writer) error {
	for _, feature := range DefinitionFeatures(definition) {
		if err := featureflags.RequireWithWriter(feature, writer); err != nil {
			return err
		}
	}
	return nil
}

// CheckDefinitionFeatures validates preview opt-ins without announcing enabled features.
func CheckDefinitionFeatures(definition map[string]any) error {
	for _, feature := range DefinitionFeatures(definition) {
		if err := featureflags.Check(feature); err != nil {
			return err
		}
	}
	return nil
}

// RequireDefinitionFeatureNotices checks enabled features and returns lifecycle notices.
func RequireDefinitionFeatureNotices(definition map[string]any) ([]string, error) {
	features := DefinitionFeatures(definition)
	for _, feature := range features {
		if err := featureflags.Check(feature); err != nil {
			return nil, err
		}
	}

	var notices []string
	for _, feature := range features {
		notice, err := featureflags.RequireWithNotice(feature)
		if err != nil {
			return nil, err
		}
		if notice != "" {
			notices = append(notices, notice)
		}
	}
	return notices, nil
}

// DefinitionFeatures returns the preview features used by a definition.
func DefinitionFeatures(definition map[string]any) []featureflags.Feature {
	var features []featureflags.Feature
	yamlSnapshot, _ := definition["enableMemorySnapshot"].(bool)
	apiSnapshot, _ := definition["enable_memory_snapshot"].(bool)
	if yamlSnapshot || apiSnapshot {
		features = append(features, featureflags.MemorySnapshot)
	}
	feature := PreviewFeature(definition)
	if feature.ID != "" {
		features = append(features, feature)
	}
	return features
}

// RequireAgentFeatures checks the inline or $ref definition.
func RequireAgentFeatures(
	service *azdext.ServiceConfig,
	projectRoot string,
	writer io.Writer,
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
	return RequireDefinitionFeatures(resolved.AsMap(), writer)
}

// CheckAgentFeatures validates an inline or $ref definition without announcing enabled features.
func CheckAgentFeatures(service *azdext.ServiceConfig, projectRoot string) error {
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
	return CheckDefinitionFeatures(resolved.AsMap())
}

// RequireAgentFeatureNotices checks an inline or $ref definition and returns lifecycle notices.
func RequireAgentFeatureNotices(service *azdext.ServiceConfig, projectRoot string) ([]string, error) {
	if service.GetHost() != foundryAgentHost {
		return nil, nil
	}
	props := ServiceConfigProps(service)
	if props == nil {
		return nil, nil
	}
	resolved, err := resolveServiceProps(props, service.GetName(), projectRoot)
	if err != nil {
		return nil, err
	}
	return RequireDefinitionFeatureNotices(resolved.AsMap())
}

// prepareGatedOperation adopts and resolves the service, then
// checks previews for Package, Publish, and Deploy. ACR reuse also
// requires its shared switch, independently of agent-kind previews.
func (p *AgentServiceTargetProvider) prepareGatedOperation(
	ctx context.Context, serviceConfig *azdext.ServiceConfig,
) error {
	defer phasetelemetry.Start(ctx, phasetelemetry.Validation)()

	if err := p.validateDeploymentServiceConfig(ctx, serviceConfig); err != nil {
		return err
	}
	if err := p.requirePreviewFeatures(ctx); err != nil {
		return err
	}
	if synthesis.ACRReuseEnabled() {
		return nil
	}
	if err := p.loadProjectPath(ctx); err != nil {
		return err
	}
	return synthesis.RequireACRReuseForProject(ctx, p.azdClient, &azdext.ProjectConfig{
		Path: p.projectPath, Services: p.projectServices,
	})
}

func (p *AgentServiceTargetProvider) requirePreviewFeatures(ctx context.Context) error {
	if props := ServiceConfigProps(p.serviceConfig); props != nil &&
		containsFileRef(props.AsMap()) {
		if err := p.loadProjectPath(ctx); err != nil {
			return err
		}
	}
	if err := CheckAgentFeatures(p.serviceConfig, p.projectPath); err != nil {
		return err
	}
	return nil
}

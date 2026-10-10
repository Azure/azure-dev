// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/protobuf/proto"
)

func unsupportedLegacyPreview() error {
	return exterrors.Validation(exterrors.CodeDeploymentPreviewUnsupported,
		"deployment preview is unsupported for legacy agent.yaml/agent.manifest.yaml projects, "+
			"whole agent-file references, nested config, or AGENT_DEFINITION_PATH",
		"move the hosted agent definition to service-level properties in azure.yaml. See "+MigrationGuideURL)
}

func resolvePreviewDefinition(
	service *azdext.ServiceConfig, projectRoot string,
) (*azdext.ServiceConfig, agent_yaml.ContainerAgent, error) {
	service, err := resolvePreviewSource(service, projectRoot)
	if err != nil {
		return nil, agent_yaml.ContainerAgent{}, err
	}
	definition, _, _, _, err := AgentDefinitionFromService(service)
	if err != nil {
		return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
	}
	if strings.TrimSpace(definition.Name) == "" || strings.ContainsAny(definition.Name, "/\\?#%\r\n") {
		return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
	}
	if err := validateEnvironmentVariableNames(service.Environment, definition.EnvironmentVariables); err != nil {
		return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
	}
	return service, definition, nil
}

func resolvePreviewSource(service *azdext.ServiceConfig, projectRoot string) (*azdext.ServiceConfig, error) {
	if service == nil || service.GetHost() != foundryAgentHost {
		return nil, exterrors.Validation(exterrors.CodeDeploymentPreviewUnsupported,
			"deployment preview supports hosted azure.ai.agent services only", "select a hosted agent service")
	}
	if os.Getenv("AGENT_DEFINITION_PATH") != "" || len(service.GetConfig().GetFields()) > 0 {
		return nil, unsupportedLegacyPreview()
	}
	service = proto.CloneOf(service)
	props := service.GetAdditionalProperties()
	if props != nil {
		if ref := props.GetFields()[AgentDefinitionRefKey]; ref != nil {
			included, err := foundry.ResolveFileRefs(map[string]any{AgentDefinitionRefKey: ref.AsInterface()}, projectRoot)
			if err != nil {
				return nil, previewConfigurationError()
			}
			_, definesKind := included["kind"]
			_, isManifest := included["template"]
			if definesKind || isManifest {
				return nil, unsupportedLegacyPreview()
			}
		}
		resolved, err := resolveServiceProps(props, service.Name, projectRoot)
		if err != nil {
			return nil, previewConfigurationError()
		}
		service.AdditionalProperties = resolved
		if structHasKind(resolved) {
			if structKind(resolved) != string(agent_yaml.AgentKindHosted) {
				return nil, exterrors.Validation(exterrors.CodeUnsupportedAgentKind,
					"deployment preview supports kind: hosted only", "select a hosted agent service")
			}
			return service, nil
		}
	}
	for _, file := range []string{"agent.yaml", "agent.yml", "agent.manifest.yaml", "agent.manifest.yml"} {
		path, err := paths.JoinAllowRoot(projectRoot, service.RelativePath, file)
		if err != nil {
			return nil, previewConfigurationError()
		}
		if _, err := os.Stat(path); err == nil {
			return nil, unsupportedLegacyPreview()
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("cannot inspect the agent definition source")
		}
	}
	return nil, previewConfigurationError()
}

func previewConfigurationError() error {
	// Parser errors may contain authored secrets. Return guidance, not their values.
	return exterrors.Validation(exterrors.CodeInvalidServiceConfig,
		"invalid unified agent configuration for deployment preview",
		"check the service-level kind, name, image, container, env, metadata.tags, and file references in azure.yaml")
}

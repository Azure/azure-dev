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
		"Deployment preview is not supported for legacy agent projects using agent.yaml/agent.manifest.yaml, "+
			"AGENT_DEFINITION_PATH, whole agent-file references, or deprecated nested config. "+
			"Move the agent definition to unified service-level azure.yaml.",
		"Move the agent definition to the unified service-level azure.yaml format. See "+MigrationGuideURL)
}

// resolvePreviewDefinition follows the existing resolver's precedence without
// falling back to a legacy definition or resolving an unused nested config.
func resolvePreviewDefinition(
	service *azdext.ServiceConfig, projectRoot string,
) (*azdext.ServiceConfig, agent_yaml.ContainerAgent, error) {
	if service == nil || service.GetHost() != "azure.ai.agent" {
		return nil, agent_yaml.ContainerAgent{}, exterrors.Validation(exterrors.CodeDeploymentPreviewUnsupported,
			"Deployment preview supports hosted azure.ai.agent services only.", "select a hosted agent service")
	}
	if os.Getenv("AGENT_DEFINITION_PATH") != "" {
		return nil, agent_yaml.ContainerAgent{}, unsupportedLegacyPreview()
	}
	if len(service.GetConfig().GetFields()) > 0 {
		return nil, agent_yaml.ContainerAgent{}, unsupportedLegacyPreview()
	}
	service = proto.CloneOf(service)
	props := service.GetAdditionalProperties()
	if props != nil {
		// A root include supplying kind is a whole definition, even when the
		// author also writes kind locally. Field includes remain supported.
		if ref := props.GetFields()["$ref"]; ref != nil {
			included, err := foundry.ResolveFileRefs(map[string]any{"$ref": ref.AsInterface()}, projectRoot)
			if err != nil {
				return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
			}
			_, definesKind := included["kind"]
			_, isManifest := included["template"]
			if definesKind || isManifest {
				return nil, agent_yaml.ContainerAgent{}, unsupportedLegacyPreview()
			}
		}
		resolved, err := resolveServiceProps(props, service.Name, projectRoot)
		if err != nil {
			return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
		}
		service.AdditionalProperties = resolved
		if structHasKind(resolved) {
			service.Config = nil
			definition, hosted, _, _, err := AgentDefinitionFromService(service)
			if err != nil {
				return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
			}
			if !hosted {
				return nil, agent_yaml.ContainerAgent{}, exterrors.Validation(exterrors.CodeUnsupportedAgentKind,
					"Deployment preview supports kind: hosted only.", "select a hosted agent service")
			}
			if strings.TrimSpace(definition.Name) == "" || strings.ContainsAny(definition.Name, "/\\?#%\r\n") {
				return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
			}
			if service.GetDocker().GetImagePassthrough() && definition.Image == "" && definition.CodeConfiguration == nil {
				return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
			}
			if err := validateRegistryConnectionServiceConfig(service); err != nil {
				return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
			}
			if err := validateEnvironmentVariableNames(service.Environment, definition.EnvironmentVariables); err != nil {
				return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
			}
			return service, definition, nil
		}
		if resolved.GetFields()["kind"] != nil {
			return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
		}
	}
	for _, file := range []string{"agent.yaml", "agent.yml", "agent.manifest.yaml", "agent.manifest.yml"} {
		path, err := paths.JoinAllowRoot(projectRoot, service.RelativePath, file)
		if err != nil {
			return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
		}
		if _, err := os.Stat(path); err == nil {
			return nil, agent_yaml.ContainerAgent{}, unsupportedLegacyPreview()
		} else if !os.IsNotExist(err) {
			return nil, agent_yaml.ContainerAgent{}, fmt.Errorf("cannot inspect the agent definition source")
		}
	}
	return nil, agent_yaml.ContainerAgent{}, previewConfigurationError()
}

func previewConfigurationError() error {
	// Schema/parser errors can embed authored values (including secrets). Do not
	// forward those errors through gRPC or debug logs.
	return exterrors.Validation(exterrors.CodeInvalidServiceConfig,
		"Invalid unified agent configuration for deployment preview.",
		"check the service-level kind, name, image, container, env, and file references in azure.yaml")
}

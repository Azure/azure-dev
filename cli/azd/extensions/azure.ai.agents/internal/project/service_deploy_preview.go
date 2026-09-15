// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/pkg/projectconfig"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/proto"
)

// AgentServicePreviewOptions describes a configured agent service to preview.
// Environment contains azd lookup values, not an environment to inject wholesale.
type AgentServicePreviewOptions struct {
	Service            *azdext.ServiceConfig
	ProjectRoot        string
	ProjectName        string
	ProjectEndpoint    string
	CodePath           string
	Environment        map[string]string
	PendingEnvironment []string
}

func previewAgentService(
	ctx context.Context,
	options AgentServicePreviewOptions,
	newClient standaloneAgentReaderFactory,
) (*DirectDeployPreviewResult, error) {
	if options.Service == nil {
		return nil, fmt.Errorf("agent service is required for deployment preview")
	}
	overridePath, err := agentDefinitionOverridePath()
	if err != nil {
		return nil, err
	}
	service := proto.CloneOf(options.Service)
	if err := ResolveServiceConfigInPlace(service, options.ProjectRoot); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("failed to resolve service %q: %s", service.Name, err),
			"fix the agent service configuration in azure.yaml",
		)
	}
	codePath := strings.TrimSpace(options.CodePath)
	serviceDir, err := paths.JoinAllowRoot(options.ProjectRoot, service.RelativePath)
	if err != nil {
		return nil, exterrors.Validation(exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("invalid source path for service %q: %s", service.Name, err),
			"keep the service project path within the azure.yaml project directory")
	}
	loaded, err := loadProjectPreviewDefinition(service, options.ProjectRoot, serviceDir, options.Environment, overridePath)
	if err != nil {
		return nil, err
	}
	definition := loaded.Definition
	plan, err := planPreviewImage(definition, service, options.ProjectName, options.Environment)
	if err != nil {
		return nil, err
	}
	if plan.Mode == "prebuilt" && options.CodePath != "" {
		return nil, exterrors.Validation(exterrors.CodeConflictingArguments,
			"--code cannot be used with a prebuilt-image preview", "omit --code to preview the configured image")
	}
	if codePath == "" {
		codePath = agentSourceDirectory(serviceDir, overridePath, overridePath != "")
	}
	if plan.Mode != "prebuilt" {
		codePath, err = resolveAgentCodePath(codePath)
		if err != nil {
			return nil, err
		}
	}
	environment := maps.Clone(options.Environment)
	if environment == nil {
		environment = map[string]string{}
	}
	environment["FOUNDRY_PROJECT_ENDPOINT"] = options.ProjectEndpoint
	pending := slices.Clone(options.PendingEnvironment)
	if definition.EnvironmentVariables != nil {
		for _, variable := range *definition.EnvironmentVariables {
			if _, resolvedByCore := service.Environment[variable.Name]; !resolvedByCore && previewContainsUnknown(variable.Value) {
				pending = append(pending, variable.Name)
			}
		}
	}
	if err := previewDefinitionDefaults(&definition, plan.Mode == "code"); err != nil {
		return nil, err
	}
	prepared, err := prepareDeployRequest(service, definition, environment, previewImageOption(plan))
	if err != nil {
		return nil, err
	}
	profile := ResolveActivityProfile(definition)
	if prepared.request.DigitalWorkerType == agent_api.DigitalWorkerTypeM365 {
		profile = ActivityProfile{IsActivity: true, UseCase: ActivityUseCaseDigitalWorker}
	}
	input := hostedAgentPreview{
		request: prepared.request, definition: definition, activityProfile: profile,
		projectEndpoint: options.ProjectEndpoint, codePath: codePath,
		image: plan, sources: loaded.Sources,
	}
	if err := addPreviewAlternatives(&input, loaded, nil); err != nil {
		return nil, err
	}
	result, err := previewHostedAgentRequest(ctx, input, pending, newClient)
	if err != nil {
		return nil, err
	}
	result.Service = redactPreviewString(service.Name)
	result.Notes = append(result.Notes, "Only agent changes are previewed; infrastructure and dependencies are not deployed.")
	if len(pending) > 0 {
		result.Notes = append(result.Notes, "Unresolved environment values are pending until their inputs are available.")
	}
	return result, nil
}

func loadProjectPreviewDefinition(
	service *azdext.ServiceConfig, projectRoot, serviceDir string, environment map[string]string, overridePath string,
) (*AgentPreviewDefinition, error) {
	if overridePath != "" {
		definition, hosted, err := loadAgentDefinitionFile(overridePath, false)
		if err != nil {
			return nil, err
		}
		if !hosted {
			return nil, exterrors.Validation(exterrors.CodeUnsupportedAgentKind,
				"deployment preview supports hosted agents only", "select a hosted agent definition")
		}
		return &AgentPreviewDefinition{Definition: definition, Sources: []string{overridePath}}, nil
	}
	sources, err := companionPreviewSources(serviceDir, "", environment)
	if err != nil {
		return nil, err
	}
	definition, hosted, found, _, err := AgentDefinitionFromResolvedService(service, projectRoot)
	if err != nil {
		return nil, err
	}
	properties := map[string]any{}
	if found {
		if !hosted {
			return nil, exterrors.Validation(exterrors.CodeUnsupportedAgentKind,
				"deployment preview supports hosted agents only", "select a hosted agent service")
		}
		data, err := yaml.Marshal(definition)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(data, &properties); err != nil {
			return nil, err
		}
		// An inline service is authoritative. Retained legacy files are still
		// compared separately but must not resurrect settings removed here.
		for _, key := range []string{
			"description", "metadata", "resources", "environment_variables", "code_configuration",
			"registryConnectionId", "agent_endpoint", "agent_card", "policies", "session_configuration",
		} {
			if _, exists := properties[key]; !exists {
				properties[key] = nil
			}
		}
		properties["image"] = definition.Image
	}
	config, err := LoadServiceTargetAgentConfig(service)
	if err != nil {
		return nil, err
	}
	if config.Container != nil && config.Container.Resources != nil {
		resources := map[string]any{}
		if config.Container.Resources.Cpu != "" {
			resources["cpu"] = config.Container.Resources.Cpu
		}
		if config.Container.Resources.Memory != "" {
			resources["memory"] = config.Container.Resources.Memory
		}
		properties["resources"] = resources
	}
	if service.Image != "" {
		properties["image"] = service.Image
	}
	rawEnv, err := projectconfig.LoadServiceEnvironment(projectRoot, service.Name)
	if err != nil {
		return nil, err
	}
	if rawEnv != nil && len(rawEnv) == 0 {
		properties["environment_variables"] = []any{}
	} else if len(service.Environment) > 0 {
		var variables []any
		for name, value := range service.Environment {
			variables = append(variables, map[string]any{"name": name, "value": value})
		}
		properties["environment_variables"] = variables
	}
	if len(properties) > 0 {
		sources = append(sources, previewDefinitionSource{
			path: filepath.Join(projectRoot, "azure.yaml") + "#services." + service.Name, properties: properties,
			replaceEnvironment: found,
		})
	}
	if len(sources) == 0 {
		return nil, exterrors.Dependency(exterrors.CodeAgentDefinitionNotFound,
			fmt.Sprintf("no agent definition found for service %q", service.Name),
			"define the agent in azure.yaml, agent.yaml, or agent.manifest.yaml")
	}
	return combinePreviewDefinitionSources(sources)
}

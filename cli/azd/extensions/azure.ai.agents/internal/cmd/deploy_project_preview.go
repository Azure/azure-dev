// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/projectconfig"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func previewProjectAgentDeployment(
	ctx context.Context, flags agentDeployFlags,
) (*project.DirectDeployPreviewResult, error) {
	client, err := azdext.NewAzdClient()
	if err != nil {
		return nil, exterrors.FromHost(err, exterrors.CodeProjectNotFound, "connecting to azd for project preview")
	}
	defer client.Close()
	options, err := loadProjectAgentPreview(ctx, client, flags)
	if err != nil {
		return nil, err
	}
	return project.PreviewAgentService(ctx, options)
}

func loadProjectAgentPreview(
	ctx context.Context, client *azdext.AzdClient, flags agentDeployFlags,
) (project.AgentServicePreviewOptions, error) {
	service, projectConfig, err := resolveAgentService(ctx, client, flags.serviceName, flags.noPrompt, "--service")
	if err != nil {
		return project.AgentServicePreviewOptions{}, err
	}
	environment, err := loadPreviewEnvironment(ctx, client)
	if err != nil {
		return project.AgentServicePreviewOptions{}, err
	}
	endpoint, err := resolveProjectPreviewEndpoint(ctx, service, projectConfig, environment, flags.projectEndpoint)
	if err != nil {
		return project.AgentServicePreviewOptions{}, err
	}
	pending, err := pendingProjectEnvironment(projectConfig.Path, service, environment)
	if err != nil {
		return project.AgentServicePreviewOptions{}, err
	}
	return project.AgentServicePreviewOptions{
		Service: service, ProjectRoot: projectConfig.Path, ProjectName: projectConfig.Name, ProjectEndpoint: endpoint,
		CodePath: flags.codePath, Environment: environment, PendingEnvironment: pending,
	}, nil
}

func loadPreviewEnvironment(ctx context.Context, client *azdext.AzdClient) (map[string]string, error) {
	current, err := client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if status.Code(err) == codes.NotFound {
		// An initialized project can be previewed without deployment outputs.
		// Remote agent existence is still checked separately.
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, exterrors.FromHost(err, exterrors.CodeEnvironmentNotFound, "reading the current azd environment")
	}
	if current == nil || current.Environment == nil || current.Environment.Name == "" {
		return nil, fmt.Errorf("azd returned an invalid current environment")
	}
	values, err := client.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: current.Environment.Name})
	if err != nil {
		return nil, exterrors.FromHost(err, exterrors.CodeEnvironmentValuesFailed, "reading azd environment values")
	}
	if values == nil {
		return nil, fmt.Errorf("azd returned an invalid environment values response")
	}
	environment := make(map[string]string, len(values.KeyValues))
	for _, entry := range values.KeyValues {
		environment[entry.GetKey()] = entry.GetValue()
	}
	return environment, nil
}

func resolveProjectPreviewEndpoint(
	ctx context.Context,
	service *azdext.ServiceConfig,
	projectConfig *azdext.ProjectConfig,
	environment map[string]string,
	override string,
) (string, error) {
	if override != "" {
		endpoint, _, err := validateProjectEndpoint(override)
		return endpoint, err
	}
	var projectService *azdext.ServiceConfig
	for _, name := range service.GetUses() {
		dependency := projectConfig.Services[name]
		if dependency == nil {
			return "", exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("agent service %q uses unknown service %q", service.Name, name),
				"fix the agent service uses list in azure.yaml",
			)
		}
		if dependency.Host != AiProjectHost {
			continue
		}
		if projectService != nil {
			return "", exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("agent service %q uses multiple Foundry projects", service.Name),
				"select one azure.ai.project dependency or pass --project-endpoint",
			)
		}
		projectService = proto.CloneOf(dependency)
	}
	if projectService != nil {
		if err := project.ResolveServiceConfigInPlace(projectService, projectConfig.Path); err != nil {
			return "", err
		}
		config, err := project.LoadServiceTargetAgentConfig(projectService)
		if err != nil {
			return "", err
		}
		if config.Endpoint != "" {
			endpoint, err := project.ExpandEnv(config.Endpoint, func(name string) string {
				if value, found := environment[name]; found {
					return value
				}
				return os.Getenv(name)
			})
			if err != nil {
				return "", fmt.Errorf("resolve the Foundry project endpoint: %w", err)
			}
			normalized, _, err := validateProjectEndpoint(endpoint)
			return normalized, err
		}
	}
	if endpoint := environment["FOUNDRY_PROJECT_ENDPOINT"]; endpoint != "" {
		normalized, _, err := validateProjectEndpoint(endpoint)
		return normalized, err
	}
	resolved, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{})
	if err != nil {
		return "", err
	}
	return resolved.Endpoint, nil
}

func pendingProjectEnvironment(
	projectRoot string, service *azdext.ServiceConfig, environment map[string]string,
) ([]string, error) {
	raw, err := projectconfig.LoadServiceEnvironment(projectRoot, service.Name)
	if err != nil {
		return nil, err
	}
	var pending []string
	for name, expression := range raw {
		if service.GetEnvironment()[name] != "" {
			continue
		}
		missing := false
		expanded, err := project.ExpandEnv(expression, func(variable string) string {
			if value, found := environment[variable]; found {
				return value
			}
			value, found := os.LookupEnv(variable)
			missing = missing || !found
			return value
		})
		if err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("failed to resolve environment variable %q: %s", name, err),
				"fix the service env expression in azure.yaml",
			)
		}
		if missing && expanded == "" {
			pending = append(pending, name)
		}
	}
	slices.Sort(pending)
	return pending, nil
}

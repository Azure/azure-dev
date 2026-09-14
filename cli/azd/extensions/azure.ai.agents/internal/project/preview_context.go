// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"slices"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/projectconfig"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func loadServicePreviewOptions(
	ctx context.Context, client *azdext.AzdClient, service *azdext.ServiceConfig,
) (AgentServicePreviewOptions, error) {
	if service == nil {
		return AgentServicePreviewOptions{}, fmt.Errorf("service config is required for deployment preview")
	}
	response, err := client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return AgentServicePreviewOptions{},
			exterrors.FromHost(err, exterrors.CodeProjectNotFound, "reading the project for deployment preview")
	}
	if response.GetProject() == nil || response.Project.Path == "" {
		return AgentServicePreviewOptions{}, fmt.Errorf("azd returned an invalid project for deployment preview")
	}
	environment, err := loadPreviewEnvironment(ctx, client)
	if err != nil {
		return AgentServicePreviewOptions{}, err
	}
	endpoint, err := resolvePreviewProjectEndpoint(service, response.Project, environment)
	if err != nil {
		return AgentServicePreviewOptions{}, err
	}
	pending, err := pendingProjectEnvironment(response.Project.Path, service, environment)
	if err != nil {
		return AgentServicePreviewOptions{}, err
	}
	return AgentServicePreviewOptions{
		Service: service, ProjectRoot: response.Project.Path, ProjectName: response.Project.Name,
		ProjectEndpoint: endpoint, Environment: environment, PendingEnvironment: pending,
	}, nil
}

func loadPreviewEnvironment(ctx context.Context, client *azdext.AzdClient) (map[string]string, error) {
	current, err := client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if status.Code(err) == codes.NotFound {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, exterrors.FromHost(err, exterrors.CodeEnvironmentNotFound, "reading the current azd environment")
	}
	if current.GetEnvironment() == nil || current.Environment.Name == "" {
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
	if _, present := environment["AZURE_ENV_NAME"]; !present {
		environment["AZURE_ENV_NAME"] = current.Environment.Name
	}
	return environment, nil
}

func resolvePreviewProjectEndpoint(
	service *azdext.ServiceConfig, config *azdext.ProjectConfig, environment map[string]string,
) (string, error) {
	var projectService *azdext.ServiceConfig
	for _, name := range service.GetUses() {
		dependency := config.Services[name]
		if dependency == nil {
			return "", exterrors.Validation(exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("agent service %q uses unknown service %q", service.Name, name),
				"fix the agent service uses list in azure.yaml")
		}
		if dependency.Host != "azure.ai.project" {
			continue
		}
		if projectService != nil {
			return "", exterrors.Validation(exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("agent service %q uses multiple Foundry projects", service.Name),
				"select one azure.ai.project dependency in azure.yaml")
		}
		projectService = proto.CloneOf(dependency)
	}
	if projectService != nil {
		if err := ResolveServiceConfigInPlace(projectService, config.Path); err != nil {
			return "", err
		}
		projectConfig, err := LoadServiceTargetAgentConfig(projectService)
		if err != nil {
			return "", err
		}
		if projectConfig != nil && projectConfig.Endpoint != "" {
			endpoint, err := ExpandEnv(projectConfig.Endpoint, func(name string) string {
				if value, found := environment[name]; found {
					return value
				}
				return os.Getenv(name)
			})
			if err != nil {
				return "", fmt.Errorf("resolve the Foundry project endpoint: %w", err)
			}
			normalized, _, err := ValidateProjectEndpoint(endpoint)
			return normalized, err
		}
	}
	endpoint := environment["FOUNDRY_PROJECT_ENDPOINT"]
	if endpoint == "" {
		endpoint = os.Getenv("FOUNDRY_PROJECT_ENDPOINT")
	}
	if endpoint == "" {
		return "", exterrors.Dependency(exterrors.CodeMissingAiProjectEndpoint,
			"a Foundry project endpoint is required to compare the deployed agent",
			"run 'azd provision', or configure an existing azure.ai.project endpoint in azure.yaml")
	}
	normalized, _, err := ValidateProjectEndpoint(endpoint)
	return normalized, err
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
		expanded, err := ExpandEnv(expression, func(variable string) string {
			if value, found := environment[variable]; found {
				return value
			}
			value, found := os.LookupEnv(variable)
			missing = missing || !found
			return value
		})
		if err != nil {
			return nil, exterrors.Validation(exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("failed to resolve environment variable %q: %s", name, err),
				"fix the service env expression in azure.yaml")
		}
		if missing && expanded == "" {
			pending = append(pending, name)
		}
	}
	slices.Sort(pending)
	return pending, nil
}

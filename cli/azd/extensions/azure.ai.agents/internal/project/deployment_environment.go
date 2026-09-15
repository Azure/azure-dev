// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/proto"
)

func deploymentEnvironmentValue(environment map[string]string, name string) string {
	if value, found := environment[name]; found {
		return value
	}
	return os.Getenv(name)
}

func legacySkipACREnabled(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}

func (p *AgentServiceTargetProvider) loadDeploymentEnvironment(
	ctx context.Context, service *azdext.ServiceConfig,
) (map[string]string, error) {
	if p.env == nil || p.env.Name == "" {
		return nil, exterrors.Dependency(exterrors.CodeEnvironmentNotFound,
			"an azd environment is required for deployment", "select an existing environment with --environment")
	}
	response, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: p.env.Name})
	if err != nil {
		return nil, exterrors.FromHost(err, exterrors.CodeEnvironmentValuesFailed, "reading deployment environment values")
	}
	if response == nil {
		return nil, fmt.Errorf("azd returned an invalid environment values response")
	}
	values := make(map[string]string, len(response.KeyValues))
	for _, entry := range response.KeyValues {
		values[entry.GetKey()] = entry.GetValue()
	}
	endpoint, err := resolveAgentProjectEndpoint(service, &azdext.ProjectConfig{
		Path: p.projectPath, Services: p.projectServices,
	}, values)
	if err != nil {
		return nil, err
	}
	// Resolve into this request's snapshot, not the persisted azd environment.
	values["FOUNDRY_PROJECT_ENDPOINT"] = endpoint
	return values, nil
}

func resolveAgentProjectEndpoint(
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
				return deploymentEnvironmentValue(environment, name)
			})
			if err != nil {
				return "", fmt.Errorf("resolve the Foundry project endpoint: %w", err)
			}
			normalized, _, err := ValidateProjectEndpoint(endpoint)
			return normalized, err
		}
	}
	endpoint := deploymentEnvironmentValue(environment, "FOUNDRY_PROJECT_ENDPOINT")
	if endpoint == "" {
		return "", exterrors.Dependency(exterrors.CodeMissingAiProjectEndpoint,
			"FOUNDRY_PROJECT_ENDPOINT is required to deploy or preview the agent",
			"run 'azd provision', or configure an existing azure.ai.project endpoint in azure.yaml")
	}
	normalized, _, err := ValidateProjectEndpoint(endpoint)
	return normalized, err
}

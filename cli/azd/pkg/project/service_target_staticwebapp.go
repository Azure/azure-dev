// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// TODO: Enhance for multi-environment support
// https://github.com/Azure/azure-dev/issues/1152
const DefaultStaticWebAppEnvironmentName = "default"

type staticWebAppTarget struct {
	config *ServiceConfig
	env    *environment.Environment
	scope  *environment.DeploymentScope
	cli    tools.AzCli
	swa    tools.SwaCli
}

func (at *staticWebAppTarget) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{at.cli, at.swa}
}

func (at *staticWebAppTarget) Deploy(ctx context.Context, azdCtx *environment.AzdContext, path string, progress chan<- string) (ServiceDeploymentResult, error) {
	if strings.TrimSpace(at.config.OutputPath) == "" {
		at.config.OutputPath = "build"
	}

	// Get the static webapp deployment token
	progress <- "Retrieving deployment tokens"
	deploymentToken, err := at.cli.GetStaticWebAppApiKey(ctx, at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName())
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed retrieving static web app deployment token: %w", err)
	}

	// SWA performs a zip & deploy of the specified output folder and publishes it to the configured environment
	progress <- "Publishing deployment artifacts"
	res, err := at.swa.Deploy(ctx, at.env.GetTenantId(), at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName(), at.config.RelativePath, at.config.OutputPath, DefaultStaticWebAppEnvironmentName, deploymentToken)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed deploying static web app: %w", err)
	}

	verifyMsg := "Verifying deployment"
	retries := 0
	const maxRetries = 10

	for {
		progress <- verifyMsg
		envProps, err := at.cli.GetStaticWebAppEnvironmentProperties(ctx, at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName(), DefaultStaticWebAppEnvironmentName)
		if err != nil {
			return ServiceDeploymentResult{}, fmt.Errorf("failed verifying static web app deployment: %w", err)
		}

		if envProps.Status == "Ready" {
			break
		}

		retries++

		if retries >= maxRetries {
			return ServiceDeploymentResult{}, fmt.Errorf("failed verifying static web app deployment. Still in %s state", envProps.Status)
		}

		verifyMsg += "."
		time.Sleep(2 * time.Second)
	}

	progress <- "Fetching endpoints for static web app"
	endpoints, err := at.Endpoints(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	sdr := NewServiceDeploymentResult(
		azure.StaticWebAppRID(at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName()),
		StaticWebAppTarget,
		res,
		endpoints,
	)

	return sdr, nil
}

func (at *staticWebAppTarget) Endpoints(ctx context.Context) ([]string, error) {
	// TODO: Enhance for multi-environment support
	// https://github.com/Azure/azure-dev/issues/1152
	envProps, err := at.cli.GetStaticWebAppEnvironmentProperties(ctx, at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName(), DefaultStaticWebAppEnvironmentName)
	if err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	}

	return []string{fmt.Sprintf("https://%s/", envProps.Hostname)}, nil
}

func NewStaticWebAppTarget(config *ServiceConfig, env *environment.Environment, scope *environment.DeploymentScope, azCli tools.AzCli, swaCli tools.SwaCli) ServiceTarget {
	return &staticWebAppTarget{
		config: config,
		env:    env,
		scope:  scope,
		cli:    azCli,
		swa:    swaCli,
	}
}

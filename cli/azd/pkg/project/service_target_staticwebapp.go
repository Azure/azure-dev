// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

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

	staticWebAppEnvironmentName := at.env.GetEnvName()
	if strings.TrimSpace(staticWebAppEnvironmentName) == "" {
		staticWebAppEnvironmentName = "production"
	}

	log.Printf("Logging into SWA CLI: TenantId: %s, SubscriptionId: %s, ResourceGroup: %s, ResourceName: %s", at.env.GetTenantId(), at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName())

	// Login to get the app deployment token
	progress <- "Retrieving deployment tokens"
	deploymentToken, err := at.cli.GetStaticWebAppApiKey(ctx, at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName())
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("Failed retrieving static web app deployment token: %w", err)
	}

	// SWA performs a zip & deploy of the specified output folder and publishes it to the configured environment
	log.Printf("Deploying SWA app: TenantId: %s, SubscriptionId: %s, ResourceGroup: %s, ResourceName: %s", at.env.GetTenantId(), at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName())
	progress <- "Publishing deployment artifacts"
	res, err := at.swa.Deploy(ctx, at.env.GetTenantId(), at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName(), at.config.RelativePath, at.config.OutputPath, staticWebAppEnvironmentName, deploymentToken)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("Failed deploying static web app: %w", err)
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
	if props, err := at.cli.GetStaticWebAppProperties(ctx, at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName()); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		return []string{fmt.Sprintf("https://%s/", props.DefaultHostname)}, nil
	}
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

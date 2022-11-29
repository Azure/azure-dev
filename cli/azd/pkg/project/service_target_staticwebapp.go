// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/swa"
)

// TODO: Enhance for multi-environment support
// https://github.com/Azure/azure-dev/issues/1152
const DefaultStaticWebAppEnvironmentName = "default"

type staticWebAppTarget struct {
	config   *ServiceConfig
	env      *environment.Environment
	resource *environment.TargetResource
	cli      azcli.AzCli
	swa      swa.SwaCli
}

func (at *staticWebAppTarget) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{at.swa}
}

func (at *staticWebAppTarget) Deploy(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	path string,
	progress chan<- string,
) (ServiceDeploymentResult, error) {
	if strings.TrimSpace(at.config.OutputPath) == "" {
		at.config.OutputPath = "build"
	}

	// Get the static webapp deployment token
	progress <- "Retrieving deployment token"
	deploymentToken, err := at.cli.GetStaticWebAppApiKey(
		ctx,
		at.env.GetSubscriptionId(),
		at.resource.ResourceGroupName(),
		at.resource.ResourceName(),
	)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed retrieving static web app deployment token: %w", err)
	}

	// SWA performs a zip & deploy of the specified output folder and publishes it to the configured environment
	progress <- "Publishing deployment artifacts"
	res, err := at.swa.Deploy(ctx,
		at.config.Project.Path,
		at.env.GetTenantId(),
		at.env.GetSubscriptionId(),
		at.resource.ResourceGroupName(),
		at.resource.ResourceName(),
		at.config.RelativePath,
		at.config.OutputPath,
		DefaultStaticWebAppEnvironmentName,
		*deploymentToken)

	log.Println(res)

	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed deploying static web app: %w", err)
	}

	if err := at.verifyDeployment(ctx, progress); err != nil {
		return ServiceDeploymentResult{}, err
	}

	progress <- "Fetching endpoints for static web app"
	endpoints, err := at.Endpoints(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	sdr := NewServiceDeploymentResult(
		azure.StaticWebAppRID(
			at.env.GetSubscriptionId(),
			at.resource.ResourceGroupName(),
			at.resource.ResourceName(),
		),
		StaticWebAppTarget,
		res,
		endpoints,
	)

	return sdr, nil
}

func (at *staticWebAppTarget) Endpoints(ctx context.Context) ([]string, error) {
	// TODO: Enhance for multi-environment support
	// https://github.com/Azure/azure-dev/issues/1152
	if envProps, err := at.cli.GetStaticWebAppEnvironmentProperties(
		ctx,
		at.env.GetSubscriptionId(),
		at.resource.ResourceGroupName(),
		at.resource.ResourceName(),
		DefaultStaticWebAppEnvironmentName,
	); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		return []string{fmt.Sprintf("https://%s/", envProps.Hostname)}, nil
	}
}

func (at *staticWebAppTarget) verifyDeployment(ctx context.Context, progress chan<- string) error {
	verifyMsg := "Verifying deployment"
	retries := 0
	const maxRetries = 10

	for {
		progress <- verifyMsg
		envProps, err := at.cli.GetStaticWebAppEnvironmentProperties(
			ctx,
			at.env.GetSubscriptionId(),
			at.resource.ResourceGroupName(),
			at.resource.ResourceName(),
			DefaultStaticWebAppEnvironmentName,
		)
		if err != nil {
			return fmt.Errorf("failed verifying static web app deployment: %w", err)
		}

		if envProps.Status == "Ready" {
			break
		}

		retries++

		if retries >= maxRetries {
			return fmt.Errorf("failed verifying static web app deployment. Still in %s state", envProps.Status)
		}

		verifyMsg += "."
		time.Sleep(5 * time.Second)
	}

	return nil
}

func NewStaticWebAppTarget(
	config *ServiceConfig,
	env *environment.Environment,
	resource *environment.TargetResource,
	azCli azcli.AzCli,
	swaCli swa.SwaCli,
) (ServiceTarget, error) {
	if !strings.EqualFold(resource.ResourceType(), string(infra.AzureResourceTypeStaticWebSite)) {
		return nil, resourceTypeMismatchError(
			resource.ResourceName(),
			resource.ResourceType(),
			infra.AzureResourceTypeStaticWebSite,
		)
	}

	return &staticWebAppTarget{
		config:   config,
		env:      env,
		resource: resource,
		cli:      azCli,
		swa:      swaCli,
	}, nil
}

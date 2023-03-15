// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/swa"
)

// TODO: Enhance for multi-environment support
// https://github.com/Azure/azure-dev/issues/1152
const DefaultStaticWebAppEnvironmentName = "default"

type staticWebAppTarget struct {
	env *environment.Environment
	cli azcli.AzCli
	swa swa.SwaCli
}

func NewStaticWebAppTarget(
	env *environment.Environment,
	azCli azcli.AzCli,
	swaCli swa.SwaCli,
) ServiceTarget {
	return &staticWebAppTarget{
		env: env,
		cli: azCli,
		swa: swaCli,
	}
}

func (at *staticWebAppTarget) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{at.swa}
}

func (at *staticWebAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (at *staticWebAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			if strings.TrimSpace(serviceConfig.OutputPath) == "" {
				serviceConfig.OutputPath = "build"
			}

			task.SetResult(&ServicePackageResult{
				Build:       buildOutput,
				PackagePath: serviceConfig.OutputPath,
			})
		},
	)
}

func (at *staticWebAppTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServicePublishResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePublishResult, ServiceProgress]) {
			if err := at.validateTargetResource(ctx, serviceConfig, targetResource); err != nil {
				task.SetError(fmt.Errorf("validating target resource: %w", err))
				return
			}

			// Get the static webapp deployment token
			task.SetProgress(NewServiceProgress("Retrieving deployment token"))
			deploymentToken, err := at.cli.GetStaticWebAppApiKey(
				ctx,
				at.env.GetSubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
			)
			if err != nil {
				task.SetError(fmt.Errorf("failed retrieving static web app deployment token: %w", err))
				return
			}

			// SWA performs a zip & deploy of the specified output folder and publishes it to the configured environment
			task.SetProgress(NewServiceProgress("Publishing deployment artifacts"))
			res, err := at.swa.Deploy(ctx,
				serviceConfig.Project.Path,
				at.env.GetTenantId(),
				at.env.GetSubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				serviceConfig.RelativePath,
				packageOutput.PackagePath,
				DefaultStaticWebAppEnvironmentName,
				*deploymentToken)

			log.Println(res)

			if err != nil {
				task.SetError(fmt.Errorf("failed deploying static web app: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Verifying deployment"))
			if err := at.verifyDeployment(ctx, targetResource); err != nil {
				task.SetError(err)
			}

			task.SetProgress(NewServiceProgress("Fetching endpoints for static web app"))
			endpoints, err := at.Endpoints(ctx, serviceConfig, targetResource)
			if err != nil {
				task.SetError(err)
			}

			sdr := NewServicePublishResult(
				azure.StaticWebAppRID(
					at.env.GetSubscriptionId(),
					targetResource.ResourceGroupName(),
					targetResource.ResourceName(),
				),
				StaticWebAppTarget,
				res,
				endpoints,
			)
			sdr.Package = packageOutput

			task.SetResult(sdr)
		},
	)
}

func (at *staticWebAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// TODO: Enhance for multi-environment support
	// https://github.com/Azure/azure-dev/issues/1152
	if envProps, err := at.cli.GetStaticWebAppEnvironmentProperties(
		ctx,
		at.env.GetSubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		DefaultStaticWebAppEnvironmentName,
	); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		return []string{fmt.Sprintf("https://%s/", envProps.Hostname)}, nil
	}
}

func (at *staticWebAppTarget) validateTargetResource(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) error {
	if !strings.EqualFold(targetResource.ResourceType(), string(infra.AzureResourceTypeStaticWebSite)) {
		return resourceTypeMismatchError(
			targetResource.ResourceName(),
			targetResource.ResourceType(),
			infra.AzureResourceTypeStaticWebSite,
		)
	}

	return nil
}

func (at *staticWebAppTarget) verifyDeployment(ctx context.Context, targetResource *environment.TargetResource) error {
	retries := 0
	const maxRetries = 10

	for {
		envProps, err := at.cli.GetStaticWebAppEnvironmentProperties(
			ctx,
			at.env.GetSubscriptionId(),
			targetResource.ResourceGroupName(),
			targetResource.ResourceName(),
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

		time.Sleep(5 * time.Second)
	}

	return nil
}

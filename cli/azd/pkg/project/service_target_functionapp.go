// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/project/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// functionAppTarget specifies an Azure Function to deploy to.
// Implements `project.ServiceTarget`
type functionAppTarget struct {
	env *environment.Environment
	cli azcli.AzCli
}

func NewFunctionAppTarget(
	env *environment.Environment,
	azCli azcli.AzCli,
) ServiceTarget {
	return &functionAppTarget{
		env: env,
		cli: azCli,
	}
}

func (f *functionAppTarget) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (f *functionAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (f *functionAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			task.SetProgress(NewServiceProgress("Compressing deployment artifacts"))
			zipFilePath, err := internal.CreateDeployableZip(serviceConfig.Name, buildOutput.BuildOutputPath)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetResult(&ServicePackageResult{
				Build:       buildOutput,
				PackagePath: zipFilePath,
			})
		},
	)
}

func (f *functionAppTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServicePublishResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePublishResult, ServiceProgress]) {
			if !strings.EqualFold(targetResource.ResourceType(), string(infra.AzureResourceTypeWebSite)) {
				task.SetError(resourceTypeMismatchError(
					targetResource.ResourceName(),
					targetResource.ResourceType(),
					infra.AzureResourceTypeWebSite,
				))
				return
			}

			zipFile, err := os.Open(packageOutput.PackagePath)
			if err != nil {
				task.SetError(fmt.Errorf("failed reading deployment zip file: %w", err))
				return
			}

			defer os.Remove(packageOutput.PackagePath)
			defer zipFile.Close()

			task.SetProgress(NewServiceProgress("Publishing deployment package"))
			res, err := f.cli.DeployFunctionAppUsingZipFile(
				ctx,
				f.env.GetSubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				zipFile,
			)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetProgress(NewServiceProgress("Fetching endpoints for function app"))
			endpoints, err := f.Endpoints(ctx, serviceConfig, targetResource)
			if err != nil {
				task.SetError(err)
				return
			}

			sdr := NewServicePublishResult(
				azure.WebsiteRID(
					f.env.GetSubscriptionId(),
					targetResource.ResourceGroupName(),
					targetResource.ResourceName(),
				),
				AzureFunctionTarget,
				*res,
				endpoints,
			)
			sdr.Package = packageOutput

			task.SetResult(sdr)
		},
	)
}

func (f *functionAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// TODO(azure/azure-dev#670) Implement this. For now we just return an empty set of endpoints and
	// a nil error.  In `deploy` we just loop over the endpoint array and print any endpoints, so returning
	// an empty array and nil error will mean "no endpoints".
	if props, err := f.cli.GetFunctionAppProperties(
		ctx, f.env.GetSubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName()); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		endpoints := make([]string, len(props.HostNames))
		for idx, hostName := range props.HostNames {
			endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
		}

		return endpoints, nil
	}
}

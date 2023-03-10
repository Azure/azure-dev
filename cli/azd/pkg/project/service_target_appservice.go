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

type appServiceTarget struct {
	config *ServiceConfig
	env    *environment.Environment
	cli    azcli.AzCli
}

func NewAppServiceTarget(
	env *environment.Environment,
	azCli azcli.AzCli,
) ServiceTarget {

	return &appServiceTarget{
		env: env,
		cli: azCli,
	}
}

func (st *appServiceTarget) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (st *appServiceTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (st *appServiceTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			task.SetProgress(NewServiceProgress("Compressing deployment artifacts"))
			zipFilePath, err := internal.CreateDeployableZip(st.config.Name, buildOutput.BuildOutputPath)
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

func (st *appServiceTarget) Publish(
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
			res, err := st.cli.DeployAppServiceZip(
				ctx,
				st.env.GetSubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				zipFile,
			)
			if err != nil {
				task.SetError(fmt.Errorf("deploying service %s: %w", st.config.Name, err))
				return
			}

			task.SetProgress(NewServiceProgress("Fetching endpoints for app service"))
			endpoints, err := st.Endpoints(ctx, serviceConfig, targetResource)
			if err != nil {
				task.SetError(err)
				return
			}

			sdr := NewServicePublishResult(
				azure.WebsiteRID(
					st.env.GetSubscriptionId(),
					targetResource.ResourceGroupName(),
					targetResource.ResourceName(),
				),
				AppServiceTarget,
				*res,
				endpoints,
			)
			sdr.Package = packageOutput

			task.SetResult(sdr)
		},
	)
}

func (st *appServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	appServiceProperties, err := st.cli.GetAppServiceProperties(
		ctx,
		st.env.GetSubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	}

	endpoints := make([]string, len(appServiceProperties.HostNames))
	for idx, hostName := range appServiceProperties.HostNames {
		endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
	}

	return endpoints, nil
}

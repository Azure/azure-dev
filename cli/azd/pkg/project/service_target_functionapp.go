// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/project/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// functionAppTarget specifies an Azure Function to deploy to.
// Implements `project.ServiceTarget`
type functionAppTarget struct {
	config *ServiceConfig
	env    *environment.Environment
	scope  *environment.DeploymentScope
	cli    azcli.AzCli
}

func (f *functionAppTarget) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{f.cli}
}

func (f *functionAppTarget) Deploy(ctx context.Context, _ *azdcontext.AzdContext, path string, progress chan<- string) (ServiceDeploymentResult, error) {
	progress <- "Compressing deployment artifacts"
	zipFilePath, err := internal.CreateDeployableZip(f.config.Name, path)

	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	defer os.Remove(zipFilePath)

	progress <- "Publishing deployment package"
	res, err := f.cli.DeployFunctionAppUsingZipFile(ctx, f.env.GetSubscriptionId(), f.scope.ResourceGroupName(), f.scope.ResourceName(), zipFilePath)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	progress <- "Fetching endpoints for function app"
	endpoints, err := f.Endpoints(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	sdr := NewServiceDeploymentResult(
		azure.WebsiteRID(f.env.GetSubscriptionId(), f.scope.ResourceGroupName(), f.scope.ResourceName()),
		AzureFunctionTarget,
		res,
		endpoints,
	)
	return sdr, nil
}

func (f *functionAppTarget) Endpoints(ctx context.Context) ([]string, error) {
	// TODO(azure/azure-dev#670) Implement this. For now we just return an empty set of endpoints and
	// a nil error.  In `deploy` we just loop over the endpoint array and print any endpoints, so returning
	// an empty array and nil error will mean "no endpoints".
	if props, err := f.cli.GetFunctionAppProperties(ctx, f.env.GetSubscriptionId(), f.scope.ResourceGroupName(), f.scope.ResourceName()); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		endpoints := make([]string, len(props.HostNames))
		for idx, hostName := range props.HostNames {
			endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
		}

		return endpoints, nil
	}
}

func NewFunctionAppTarget(config *ServiceConfig, env *environment.Environment, scope *environment.DeploymentScope, azCli azcli.AzCli) ServiceTarget {
	return &functionAppTarget{
		config: config,
		env:    env,
		scope:  scope,
		cli:    azCli,
	}
}

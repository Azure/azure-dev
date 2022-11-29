// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/project/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// functionAppTarget specifies an Azure Function to deploy to.
// Implements `project.ServiceTarget`
type functionAppTarget struct {
	config   *ServiceConfig
	env      *environment.Environment
	resource *environment.TargetResource
	cli      azcli.AzCli
}

func (f *functionAppTarget) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (f *functionAppTarget) Deploy(
	ctx context.Context,
	_ *azdcontext.AzdContext,
	path string,
	progress chan<- string,
) (ServiceDeploymentResult, error) {
	progress <- "Compressing deployment artifacts"

	zipFilePath, err := internal.CreateDeployableZip(f.config.Name, path)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	zipFile, err := os.Open(zipFilePath)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed reading deployment zip file: %w", err)
	}

	defer os.Remove(zipFilePath)
	defer zipFile.Close()

	progress <- "Publishing deployment package"
	res, err := f.cli.DeployFunctionAppUsingZipFile(
		ctx,
		f.env.GetSubscriptionId(),
		f.resource.ResourceGroupName(),
		f.resource.ResourceName(),
		zipFile,
	)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	progress <- "Fetching endpoints for function app"
	endpoints, err := f.Endpoints(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	sdr := NewServiceDeploymentResult(
		azure.WebsiteRID(f.env.GetSubscriptionId(), f.resource.ResourceGroupName(), f.resource.ResourceName()),
		AzureFunctionTarget,
		*res,
		endpoints,
	)
	return sdr, nil
}

func (f *functionAppTarget) Endpoints(ctx context.Context) ([]string, error) {
	// TODO(azure/azure-dev#670) Implement this. For now we just return an empty set of endpoints and
	// a nil error.  In `deploy` we just loop over the endpoint array and print any endpoints, so returning
	// an empty array and nil error will mean "no endpoints".
	if props, err := f.cli.GetFunctionAppProperties(
		ctx, f.env.GetSubscriptionId(),
		f.resource.ResourceGroupName(),
		f.resource.ResourceName()); err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	} else {
		endpoints := make([]string, len(props.HostNames))
		for idx, hostName := range props.HostNames {
			endpoints[idx] = fmt.Sprintf("https://%s/", hostName)
		}

		return endpoints, nil
	}
}

func NewFunctionAppTarget(
	config *ServiceConfig,
	env *environment.Environment,
	resource *environment.TargetResource,
	azCli azcli.AzCli,
) (ServiceTarget, error) {
	if !strings.EqualFold(resource.ResourceType(), string(infra.AzureResourceTypeWebSite)) {
		return nil, resourceTypeMismatchError(
			resource.ResourceName(),
			resource.ResourceType(),
			infra.AzureResourceTypeWebSite,
		)
	}

	return &functionAppTarget{
		config:   config,
		env:      env,
		resource: resource,
		cli:      azCli,
	}, nil
}

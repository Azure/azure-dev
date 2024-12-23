// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// functionAppTarget specifies an Azure Function to deploy to.
// Implements `project.ServiceTarget`
type functionAppTarget struct {
	env *environment.Environment
	cli *azapi.AzureClient
}

// NewFunctionAppTarget creates a new instance of the Function App target
func NewFunctionAppTarget(
	env *environment.Environment,
	azCli *azapi.AzureClient,
) ServiceTarget {
	return &functionAppTarget{
		env: env,
		cli: azCli,
	}
}

// Gets the required external tools for the Function app
func (f *functionAppTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Initializes the function app target
func (f *functionAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Prepares a zip archive from the specified build output
func (f *functionAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	progress.SetProgress(NewServiceProgress("Compressing deployment artifacts"))
	zipFilePath, err := createDeployableZip(
		serviceConfig.Project.Name,
		serviceConfig.Name,
		packageOutput.PackagePath,
	)
	if err != nil {
		return nil, err
	}

	return &ServicePackageResult{
		Build:       packageOutput.Build,
		PackagePath: zipFilePath,
	}, nil
}

// Deploys the prepared zip archive using Zip deploy to the Azure App Service resource
func (f *functionAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := f.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	zipFile, err := os.Open(packageOutput.PackagePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading deployment zip file: %w", err)
	}

	defer os.Remove(packageOutput.PackagePath)
	defer zipFile.Close()

	progress.SetProgress(NewServiceProgress("Uploading deployment package"))
	remoteBuild := serviceConfig.Language == ServiceLanguageJavaScript ||
		serviceConfig.Language == ServiceLanguageTypeScript ||
		serviceConfig.Language == ServiceLanguagePython
	res, err := f.cli.DeployFunctionAppUsingZipFile(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		zipFile,
		remoteBuild,
	)
	if err != nil {
		return nil, err
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for function app"))
	endpoints, err := f.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	sdr := NewServiceDeployResult(
		azure.WebsiteRID(
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			targetResource.ResourceName(),
		),
		AzureFunctionTarget,
		*res,
		endpoints,
	)
	sdr.Package = packageOutput

	return sdr, nil
}

// Gets the exposed endpoints for the Function App
func (f *functionAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// TODO(azure/azure-dev#670) Implement this. For now we just return an empty set of endpoints and
	// a nil error.  In `deploy` we just loop over the endpoint array and print any endpoints, so returning
	// an empty array and nil error will mean "no endpoints".
	if props, err := f.cli.GetFunctionAppProperties(
		ctx,
		targetResource.SubscriptionId(),
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

func (f *functionAppTarget) validateTargetResource(
	targetResource *environment.TargetResource,
) error {
	if !strings.EqualFold(targetResource.ResourceType(), string(azapi.AzureResourceTypeWebSite)) {
		return resourceTypeMismatchError(
			targetResource.ResourceName(),
			targetResource.ResourceType(),
			azapi.AzureResourceTypeWebSite,
		)
	}

	return nil
}

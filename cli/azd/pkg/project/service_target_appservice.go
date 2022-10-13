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

type appServiceTarget struct {
	config *ServiceConfig
	env    *environment.Environment
	scope  *environment.DeploymentScope
	cli    azcli.AzCli
}

func (st *appServiceTarget) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{st.cli}
}

func (st *appServiceTarget) Deploy(
	ctx context.Context,
	_ *azdcontext.AzdContext,
	path string,
	progress chan<- string,
) (ServiceDeploymentResult, error) {
	progress <- "Compressing deployment artifacts"

	zipFilePath, err := internal.CreateDeployableZip(st.config.Name, path)
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
	res, err := st.cli.DeployAppServiceZip(
		ctx,
		st.env.GetSubscriptionId(),
		st.scope.ResourceGroupName(),
		st.scope.ResourceName(),
		zipFile,
	)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("deploying service %s: %w", st.config.Name, err)
	}

	progress <- "Fetching endpoints for app service"
	endpoints, err := st.Endpoints(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	sdr := NewServiceDeploymentResult(
		azure.WebsiteRID(st.env.GetSubscriptionId(), st.scope.ResourceGroupName(), st.scope.ResourceName()),
		AppServiceTarget,
		*res,
		endpoints,
	)
	return sdr, nil
}

func (st *appServiceTarget) Endpoints(ctx context.Context) ([]string, error) {
	appServiceProperties, err := st.cli.GetAppServiceProperties(
		ctx,
		st.env.GetSubscriptionId(),
		st.scope.ResourceGroupName(),
		st.scope.ResourceName(),
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

func NewAppServiceTarget(
	config *ServiceConfig,
	env *environment.Environment,
	scope *environment.DeploymentScope,
	azCli azcli.AzCli,
) ServiceTarget {
	return &appServiceTarget{
		config: config,
		env:    env,
		scope:  scope,
		cli:    azCli,
	}
}

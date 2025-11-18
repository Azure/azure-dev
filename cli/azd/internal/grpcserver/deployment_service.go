// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type deploymentService struct {
	azdext.UnimplementedDeploymentServiceServer

	lazyAzdContext    *lazy.Lazy[*azdcontext.AzdContext]
	lazyEnvManager    *lazy.Lazy[environment.Manager]
	lazyProjectConfig *lazy.Lazy[*project.ProjectConfig]
	lazyBicepProvider *lazy.Lazy[*bicep.BicepProvider]
	deploymentService azapi.DeploymentService
}

func NewDeploymentService(
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnvManager *lazy.Lazy[environment.Manager],
	lazyProjectConfig *lazy.Lazy[*project.ProjectConfig],
	lazyBicepProvider *lazy.Lazy[*bicep.BicepProvider],
	azureDeploymentService azapi.DeploymentService,
) azdext.DeploymentServiceServer {
	return &deploymentService{
		lazyAzdContext:    lazyAzdContext,
		lazyEnvManager:    lazyEnvManager,
		lazyProjectConfig: lazyProjectConfig,
		lazyBicepProvider: lazyBicepProvider,
		deploymentService: azureDeploymentService,
	}
}

func (s *deploymentService) GetDeployment(
	ctx context.Context,
	req *azdext.EmptyRequest,
) (*azdext.GetDeploymentResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}

	bicepProvider, err := s.lazyBicepProvider.GetValue()
	if err != nil {
		return nil, err
	}

	if err := bicepProvider.Initialize(ctx, azdContext.ProjectDirectory(), projectConfig.Infra); err != nil {
		return nil, err
	}

	latestDeployment, err := bicepProvider.LastDeployment(ctx)
	if err != nil {
		return nil, err
	}

	deployment := &azdext.Deployment{
		Id:           latestDeployment.Id,
		Name:         latestDeployment.Name,
		Location:     latestDeployment.Location,
		DeploymentId: latestDeployment.DeploymentId,
		Type:         latestDeployment.Type,
		Resources:    []string{},
		Tags:         map[string]string{},
	}

	for key, value := range latestDeployment.Tags {
		deployment.Tags[key] = *value
	}

	for _, resource := range latestDeployment.Resources {
		deployment.Resources = append(deployment.Resources, *resource.ID)
	}

	return &azdext.GetDeploymentResponse{
		Deployment: deployment,
	}, nil
}

func (s *deploymentService) GetDeploymentContext(
	ctx context.Context,
	req *azdext.EmptyRequest,
) (*azdext.GetDeploymentContextResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	defaultEnvironment, err := azdContext.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}

	if defaultEnvironment == "" {
		return nil, environment.ErrDefaultEnvironmentNotFound
	}

	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	env, err := envManager.Get(ctx, defaultEnvironment)
	if err != nil {
		return nil, err
	}

	tenantId := env.Getenv(environment.TenantIdEnvVarName)
	subscriptionId := env.GetSubscriptionId()
	resourceGroup := env.Getenv(environment.ResourceGroupEnvVarName)
	location := env.Getenv(environment.LocationEnvVarName)

	azureScope := &azdext.AzureScope{
		TenantId:       tenantId,
		SubscriptionId: subscriptionId,
		ResourceGroup:  resourceGroup,
		Location:       location,
	}

	latestDeployment, err := s.GetDeployment(ctx, req)
	if err != nil {
		return nil, err
	}

	azureContext := &azdext.AzureContext{
		Scope:     azureScope,
		Resources: latestDeployment.Deployment.Resources,
	}

	return &azdext.GetDeploymentContextResponse{
		AzureContext: azureContext,
	}, nil
}

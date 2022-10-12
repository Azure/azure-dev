// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type Service struct {
	// The reference to the parent project
	Project *Project
	// The reference to the service configuration from the azure.yaml file
	Config *ServiceConfig
	// The framework/platform service used to build and package the service
	Framework FrameworkService
	// The application target service used to deploy the service to azure
	Target ServiceTarget
	// The deployment scope of the service, ex) subscriptionId, resource group name & resource name
	Scope *environment.DeploymentScope
}

type ServiceDeploymentChannelResponse struct {
	// The result of a service deploy operation
	Result *ServiceDeploymentResult
	// The error that may have occurred during a deploy operation
	Error error
}

func (svc *Service) RequiredExternalTools() []tools.ExternalTool {
	requiredTools := []tools.ExternalTool{}
	requiredTools = append(requiredTools, svc.Framework.RequiredExternalTools()...)
	requiredTools = append(requiredTools, svc.Target.RequiredExternalTools()...)

	return requiredTools
}

func (svc *Service) Deploy(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
) (<-chan *ServiceDeploymentChannelResponse, <-chan string) {
	result := make(chan *ServiceDeploymentChannelResponse, 1)
	progress := make(chan string)

	go func() {
		defer close(result)
		defer close(progress)

		log.Printf("packing service %s", svc.Config.Name)

		progress <- "Preparing packaging"
		artifact, err := svc.Framework.Package(ctx, progress)
		if err != nil {
			result <- &ServiceDeploymentChannelResponse{
				Error: fmt.Errorf("packaging service %s: %w", svc.Config.Name, err),
			}

			return
		}

		log.Printf("deploying service %s", svc.Config.Name)

		progress <- "Preparing for deployment"
		res, err := svc.Target.Deploy(ctx, azdCtx, artifact, progress)
		if err != nil {
			result <- &ServiceDeploymentChannelResponse{
				Error: fmt.Errorf("deploying service %s package: %w", svc.Config.Name, err),
			}

			return
		}

		log.Printf("deployed service %s", svc.Config.Name)
		progress <- "Deployment completed"

		result <- &ServiceDeploymentChannelResponse{
			Result: &res,
		}
	}()

	return result, progress
}

// GetServiceResourceName attempts to find the name of the azure resource with the 'azd-service-name' tag set to the service key.
func GetServiceResourceName(
	ctx context.Context,
	resourceGroupName string,
	serviceName string,
	env *environment.Environment,
) (string, error) {
	res, err := GetServiceResources(ctx, resourceGroupName, serviceName, env)
	if err != nil {
		return "", err
	}

	if len(res) != 1 {
		log.Printf("Expecting only '1' resource match to override resource name but found '%d'", len(res))
		return fmt.Sprintf("%s%s", env.GetEnvName(), serviceName), nil
	}

	return res[0].Name, nil
}

// GetServiceResources gets the resources tagged for a given service
func GetServiceResources(
	ctx context.Context,
	resourceGroupName string,
	serviceName string,
	env *environment.Environment,
) ([]azcli.AzCliResource, error) {
	azCli := azcli.GetAzCli(ctx)
	filter := fmt.Sprintf("tagName eq 'azd-service-name' and tagValue eq '%s'", serviceName)

	return azCli.ListResourceGroupResources(
		ctx,
		env.GetSubscriptionId(),
		resourceGroupName,
		&azcli.ListResourceGroupResourcesOptions{
			Filter: &filter,
		},
	)
}

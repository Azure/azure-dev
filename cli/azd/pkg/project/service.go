// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type Service struct {
	// The reference to the parent project
	Project *Project
	// The reference to the service configuration from the azure.yaml file
	Config *ServiceConfig
	// The environment the service is executing in
	Environment *environment.Environment
	// The framework/platform service used to build and package the service
	Framework FrameworkService
	// The application target service used to deploy the service to azure
	Target ServiceTarget
	// The target resource of the service, ex) subscriptionId, resource group name & resource name
	TargetResource *environment.TargetResource
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

		// Allow users to specify their own endpoints, in cases where they've configured their own front-end load balancers,
		// reverse proxies or DNS host names outside of the service target (and prefer that to be used instead).
		overriddenEndpoints := svc.getOverriddenEndpoints()
		if len(overriddenEndpoints) > 0 {
			res.Endpoints = overriddenEndpoints
		}

		log.Printf("deployed service %s", svc.Config.Name)
		progress <- "Deployment completed"

		result <- &ServiceDeploymentChannelResponse{
			Result: &res,
		}
	}()

	return result, progress
}

func (svc *Service) getOverriddenEndpoints() []string {
	overriddenEndpoints := svc.Environment.GetServiceProperty(svc.Config.Name, "ENDPOINTS")
	if overriddenEndpoints != "" {
		var endpoints []string
		err := json.Unmarshal([]byte(overriddenEndpoints), &endpoints)
		if err != nil {
			// This can only happen if the environment output was not a valid JSON array, which would be due to an authoring
			// error. For typical infra provider output passthrough, the infra provider would guarantee well-formed syntax
			log.Printf(
				"failed to unmarshal endpoints override for service '%s' as JSON array of strings: %v, skipping override",
				svc.Config.Name,
				err)
		}

		return endpoints
	}

	return nil
}

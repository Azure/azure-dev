// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"fmt"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// RefreshEnvironmentAsync is the server implementation of:
// ValueTask<Environment> RefreshEnvironmentAsync(RequestContext, string, IObserver<ProgressMessage>, CancellationToken);
//
// RefreshEnvironmentAsync loads the specified environment, and fetches information about it from Azure. If you are willing
// to accept some loss of information in favor of a faster load time, use `LoadEnvironmentAsync` instead, which does not
// contact azure to compute service endpoints or last deployment information.
func (s *environmentService) RefreshEnvironmentAsync(
	ctx context.Context, rc RequestContext, name string, observer *Observer[ProgressMessage],
) (*Environment, error) {
	session, err := s.server.validateSession(rc.Session)
	if err != nil {
		return nil, err
	}

	container, err := session.newContainer(rc)
	if err != nil {
		return nil, err
	}

	return s.refreshEnvironmentAsync(ctx, container, name, observer)
}

func (s *environmentService) refreshEnvironmentAsync(
	ctx context.Context, container *container, name string, observer *Observer[ProgressMessage],
) (*Environment, error) {
	env, err := s.loadEnvironmentAsync(ctx, container, name, true)
	if err != nil {
		return nil, err
	}

	var c struct {
		projectManager       project.ProjectManager  `container:"type"`
		projectConfig        *project.ProjectConfig  `container:"type"`
		importManager        *project.ImportManager  `container:"type"`
		bicep                provisioning.Provider   `container:"name"`
		azureResourceManager infra.ResourceManager   `container:"type"`
		resourceService      *azapi.ResourceService  `container:"type"`
		resourceManager      project.ResourceManager `container:"type"`
		serviceManager       project.ServiceManager  `container:"type"`
		envManager           environment.Manager     `container:"type"`
	}

	container.MustRegisterScoped(func() internal.EnvFlag {
		return internal.EnvFlag{
			EnvironmentName: name,
		}
	})

	if err := container.Fill(&c); err != nil {
		return nil, err
	}

	bicepProvider := c.bicep.(*bicep.BicepProvider)

	if err := c.projectManager.Initialize(ctx, c.projectConfig); err != nil {
		return nil, err
	}

	if err := c.projectManager.EnsureAllTools(ctx, c.projectConfig, nil); err != nil {
		return nil, err
	}

	infra, err := c.importManager.ProjectInfrastructure(ctx, c.projectConfig)
	if err != nil {
		return nil, err
	}
	defer func() { _ = infra.Cleanup() }()

	if err := bicepProvider.Initialize(ctx, c.projectConfig.Path, infra.Options); err != nil {
		return nil, fmt.Errorf("initializing provisioning manager: %w", err)
	}

	_ = observer.OnNext(ctx, newInfoProgressMessage("Loading latest deployment information"))

	deployment, err := bicepProvider.LastDeployment(ctx)
	if err != nil {
		log.Printf("failed to get latest deployment result: %v", err)
	} else {
		env.LastDeployment = &DeploymentResult{
			DeploymentId: deployment.Id,
			Success:      deployment.ProvisioningState == azapi.DeploymentProvisioningStateSucceeded,
			Time:         deployment.Timestamp,
		}
	}

	stableServices, err := c.importManager.ServiceStable(ctx, c.projectConfig)
	if err != nil {
		return nil, err
	}

	subId := env.Properties["Subscription"]
	envName := env.Name

	nameIdx := make(map[string]int, len(env.Services)) // maps service name to index in env.Services slice
	for idx, svc := range env.Services {
		nameIdx[svc.Name] = idx
	}

	_ = observer.OnNext(ctx, newInfoProgressMessage("Loading server resources"))

	rgName, err := c.azureResourceManager.FindResourceGroupForEnvironment(ctx, subId, envName)
	if err == nil {
		env.Properties["ResourceGroup"] = rgName

		for _, serviceConfig := range stableServices {
			svcName := serviceConfig.Name

			_ = observer.OnNext(ctx, newInfoProgressMessage("Loading server resources for service "+svcName))

			resources, err := c.resourceManager.GetServiceResources(ctx, subId, rgName, serviceConfig)
			if err == nil {
				resourceIds := make([]string, len(resources))
				for idx, res := range resources {
					resourceIds[idx] = res.Id
				}

				if svcIdx, has := nameIdx[svcName]; has {
					resSvc := env.Services[svcIdx]

					if len(resourceIds) > 0 {
						resSvc.ResourceId = to.Ptr(resourceIds[0])
					}

					resSvc.Endpoint = to.Ptr(s.serviceEndpoint(
						ctx, subId, serviceConfig, c.resourceManager, c.serviceManager,
					))
				}
			} else {
				log.Printf("ignoring error determining resource id for service %s: %v", svcName, err)
			}
		}

		resources, err := c.resourceService.ListResourceGroupResources(ctx, subId, rgName, nil)
		if err == nil {
			for _, res := range resources {
				env.Resources = append(env.Resources, &Resource{
					Id:   res.Id,
					Name: res.Name,
					Type: res.Type,
				})
			}
		} else {
			log.Printf("ignoring error loading resources for environment %s: %v", envName, err)
		}
	} else {
		log.Printf(
			"ignoring error determining resource group for environment %s, resources will not be available: %v",
			env.Name,
			err)
	}

	return env, nil
}

func (s *environmentService) serviceEndpoint(
	ctx context.Context,
	subId string,
	serviceConfig *project.ServiceConfig,
	resourceManager project.ResourceManager,
	serviceManager project.ServiceManager,
) string {
	targetResource, err := resourceManager.GetTargetResource(ctx, subId, serviceConfig)
	if err != nil {
		log.Printf("error: getting target-resource. Endpoints will be empty: %v", err)
		return ""
	}

	st, err := serviceManager.GetServiceTarget(ctx, serviceConfig)
	if err != nil {
		log.Printf("error: getting service target. Endpoints will be empty: %v", err)
		return ""
	}

	endpoints, err := st.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		log.Printf("error: getting service endpoints. Endpoints might be empty: %v", err)
	}

	if len(endpoints) == 0 {
		return ""
	}

	return endpoints[0]
}

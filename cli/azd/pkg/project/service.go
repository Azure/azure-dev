// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/sethvargo/go-retry"
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

func (svc *Service) Deploy(ctx context.Context, azdCtx *azdcontext.AzdContext) (<-chan *ServiceDeploymentChannelResponse, <-chan string) {
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

// GetServiceResourceName attempts to query the azure resource graph and find the resource with the 'azd-service-name' tag set to the service key
// If not found will assume resource name conventions
func GetServiceResourceName(ctx context.Context, resourceGroupName string, serviceName string, env *environment.Environment) (string, error) {
	azCli := commands.GetAzCliFromContext(ctx)
	query := fmt.Sprintf(`resources | 
		where resourceGroup == '%s' | where tags['azd-service-name'] == '%s' |
		project id, name, type, tags, location`,
		// The Resource Graph queries have resource groups all lower-cased
		// see: https://github.com/Azure/azure-dev/issues/115
		strings.ToLower(resourceGroupName),
		serviceName)

	var graphQueryResults *azcli.AzCliGraphQuery
	err := retry.Do(ctx, retry.WithMaxRetries(5, retry.NewConstant(2*time.Second)), func(ctx context.Context) error {
		queryResult, err := azCli.GraphQuery(ctx, query, []string{env.GetSubscriptionId()})
		if err != nil {
			return fmt.Errorf("executing graph query: %s: %w", query, err)
		}

		graphQueryResults = queryResult

		if graphQueryResults.Count == 0 {
			notFoundError := azureutil.ResourceNotFound(errors.New("azure graph query returned 0 results"))
			return retry.RetryableError(notFoundError)
		}

		return nil
	})

	var notFoundError *azureutil.ResourceNotFoundError
	if err != nil && !errors.As(err, &notFoundError) {
		return "", fmt.Errorf("executing graph query: %s: %w", query, err)
	}

	// If the graph query result did not return a single result
	// Fallback to default envName + serviceName
	if graphQueryResults.TotalRecords != 1 {
		log.Printf("Expecting only '1' resource match to override resource name but found '%d'", graphQueryResults.TotalRecords)
		return fmt.Sprintf("%s%s", env.GetEnvName(), serviceName), nil
	}

	return graphQueryResults.Data[0].Name, nil
}

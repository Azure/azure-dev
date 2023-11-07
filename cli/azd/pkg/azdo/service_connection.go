// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/microsoft/azure-devops-go-api/azuredevops"
	"github.com/microsoft/azure-devops-go-api/azuredevops/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/serviceendpoint"
)

// authorize a service connection to be used in all pipelines
func authorizeServiceConnectionToAllPipelines(
	ctx context.Context,
	projectId string,
	endpoint *serviceendpoint.ServiceEndpoint,
	connection *azuredevops.Connection) error {
	buildClient, err := build.NewClient(ctx, connection)
	if err != nil {
		return err
	}

	endpointResource := "endpoint"
	endpointAuthorized := true
	endpointId := endpoint.Id.String()
	resources := []build.DefinitionResourceReference{
		{
			Type:       &endpointResource,
			Authorized: &endpointAuthorized,
			Id:         &endpointId,
		}}

	authorizeProjectResourcesArgs := build.AuthorizeProjectResourcesArgs{
		Project:   &projectId,
		Resources: &resources,
	}

	_, err = buildClient.AuthorizeProjectResources(ctx, authorizeProjectResourcesArgs)

	if err != nil {
		return err
	}
	return nil
}

// find service connection by name.
func serviceConnectionExists(ctx context.Context,
	client *serviceendpoint.Client,
	projectId *string,
	serviceConnectionName *string) (*serviceendpoint.ServiceEndpoint, error) {

	endpointNames := make([]string, 1)
	endpointNames[0] = *serviceConnectionName
	getServiceEndpointsByNamesArgs := serviceendpoint.GetServiceEndpointsByNamesArgs{
		Project:       projectId,
		EndpointNames: &endpointNames,
	}

	serviceEndpoints, err := (*client).GetServiceEndpointsByNames(ctx, getServiceEndpointsByNamesArgs)
	if err != nil {
		return nil, err
	}

	for _, endpoint := range *serviceEndpoints {
		if *endpoint.Name == *serviceConnectionName && *endpoint.IsReady {
			return &endpoint, nil
		}
	}

	return nil, nil
}

// create a new service connection that will be used in the deployment pipeline
func CreateServiceConnection(
	ctx context.Context,
	connection *azuredevops.Connection,
	projectId string,
	azdEnvironment environment.Environment,
	credentials *azcli.AzureCredentials,
	console input.Console) error {

	client, err := serviceendpoint.NewClient(ctx, connection)
	if err != nil {
		return fmt.Errorf("creating new azdo client: %w", err)
	}

	foundServiceConnection, err := serviceConnectionExists(ctx, &client, &projectId, &ServiceConnectionName)
	if err != nil {
		return fmt.Errorf("creating service connection: looking for existing connection: %w", err)
	}

	// endpoint contains the Azure credentials
	createServiceEndpointArgs, err := createAzureRMServiceEndPointArgs(ctx, &projectId, credentials)
	if err != nil {
		return fmt.Errorf("creating Azure DevOps endpoint: %w", err)
	}

	// if a service connection exists, skip creating a new Service connection. But update the current connection only
	if foundServiceConnection != nil {
		// After updating the endpoint with credentials, we no longer need it
		_, err := client.UpdateServiceEndpoint(ctx, serviceendpoint.UpdateServiceEndpointArgs{
			Endpoint:   createServiceEndpointArgs.Endpoint,
			Project:    createServiceEndpointArgs.Project,
			EndpointId: foundServiceConnection.Id,
		})
		if err != nil {
			return fmt.Errorf("updating service connection: %w", err)
		}
		console.MessageUxItem(ctx, &ux.DisplayedResource{
			Type: "Azure DevOps",
			Name: "Updated service connection",
		})
		return nil
	}

	// Service connection not found. Creating a new one and authorizing.
	endpoint, err := client.CreateServiceEndpoint(ctx, createServiceEndpointArgs)
	if err != nil {
		return fmt.Errorf("Creating new service connection: %w", err)
	}
	console.MessageUxItem(ctx, &ux.DisplayedResource{
		Type: "Azure DevOps",
		Name: "Service connection",
	})

	err = authorizeServiceConnectionToAllPipelines(ctx, projectId, endpoint, connection)
	if err != nil {
		return fmt.Errorf("authorizing service connection: %w", err)
	}

	return nil
}

// creates input parameter needed to create the azure rm service connection
func createAzureRMServiceEndPointArgs(
	ctx context.Context,
	projectId *string,
	credentials *azcli.AzureCredentials,
) (serviceendpoint.CreateServiceEndpointArgs, error) {
	endpointType := "azurerm"
	endpointOwner := "library"
	endpointUrl := "https://management.azure.com/"
	endpointName := ServiceConnectionName
	endpointIsShared := false
	endpointScheme := "ServicePrincipal"

	endpointAuthorizationParameters := map[string]string{
		"serviceprincipalid":  credentials.ClientId,
		"serviceprincipalkey": credentials.ClientSecret,
		"authenticationType":  "spnKey",
		"tenantid":            credentials.TenantId,
	}

	endpointData := map[string]string{
		"environment":      CloudEnvironment,
		"subscriptionId":   credentials.SubscriptionId,
		"subscriptionName": "azure subscription",
		"scopeLevel":       "Subscription",
		"creationMode":     "Manual",
	}

	endpointAuthorization := serviceendpoint.EndpointAuthorization{
		Scheme:     &endpointScheme,
		Parameters: &endpointAuthorizationParameters,
	}
	serviceEndpoint := &serviceendpoint.ServiceEndpoint{
		Type:          &endpointType,
		Owner:         &endpointOwner,
		Url:           &endpointUrl,
		Name:          &endpointName,
		IsShared:      &endpointIsShared,
		Authorization: &endpointAuthorization,
		Data:          &endpointData,
	}

	createServiceEndpointArgs := serviceendpoint.CreateServiceEndpointArgs{
		Project:  projectId,
		Endpoint: serviceEndpoint,
	}
	return createServiceEndpointArgs, nil
}

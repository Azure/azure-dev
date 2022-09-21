// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
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
	resources := make([]build.DefinitionResourceReference, 1)
	resources[0] = build.DefinitionResourceReference{
		Type:       &endpointResource,
		Authorized: &endpointAuthorized,
		Id:         &endpointId,
	}

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
	serviceConnectionName *string) (bool, error) {

	endpointNames := make([]string, 1)
	endpointNames[0] = *serviceConnectionName
	getServiceEndpointsByNamesArgs := serviceendpoint.GetServiceEndpointsByNamesArgs{
		Project:       projectId,
		EndpointNames: &endpointNames,
	}

	serviceEndpoints, err := (*client).GetServiceEndpointsByNames(ctx, getServiceEndpointsByNamesArgs)
	if err != nil {
		return false, err
	}

	for _, endpoint := range *serviceEndpoints {
		if *endpoint.Name == *serviceConnectionName && *endpoint.IsReady {
			return true, nil
		}
	}

	return false, nil
}

// create a new service connection that will be used in the deployment pipeline
func CreateServiceConnection(
	ctx context.Context,
	connection *azuredevops.Connection,
	projectId string,
	azdEnvironment environment.Environment,
	credentials AzureServicePrincipalCredentials,
	console input.Console) error {

	client, err := serviceendpoint.NewClient(ctx, connection)
	if err != nil {
		return err
	}

	foundServiceConnection, err := serviceConnectionExists(ctx, &client, &projectId, &ServiceConnectionName)
	if err != nil {
		return err
	}

	// if a service connection exists, skip creation.
	if foundServiceConnection {
		console.Message(ctx, output.WithWarningFormat("Service Connection %s already exists. Skipping Service Connection Creation", ServiceConnectionName))
		return nil
	}

	createServiceEndpointArgs, err := createAzureRMServiceEndPointArgs(ctx, &projectId, credentials)
	if err != nil {
		return err
	}
	endpoint, err := client.CreateServiceEndpoint(ctx, createServiceEndpointArgs)
	if err != nil {
		return err
	}

	err = authorizeServiceConnectionToAllPipelines(ctx, projectId, endpoint, connection)
	if err != nil {
		return err
	}

	return nil
}

// creates input parameter needed to create the azure rm service connection
func createAzureRMServiceEndPointArgs(
	ctx context.Context,
	projectId *string,
	credentials AzureServicePrincipalCredentials,
) (serviceendpoint.CreateServiceEndpointArgs, error) {
	endpointType := "azurerm"
	endpointOwner := "library"
	endpointUrl := "https://management.azure.com/"
	endpointName := ServiceConnectionName
	endpointIsShared := false
	endpointScheme := "ServicePrincipal"

	endpointAuthorizationParameters := make(map[string]string)
	endpointAuthorizationParameters["serviceprincipalid"] = credentials.ClientId
	endpointAuthorizationParameters["serviceprincipalkey"] = credentials.ClientSecret
	endpointAuthorizationParameters["authenticationType"] = "spnKey"
	endpointAuthorizationParameters["tenantid"] = credentials.TenantId

	endpointData := make(map[string]string)
	endpointData["environment"] = CloudEnvironment
	endpointData["subscriptionId"] = credentials.SubscriptionId
	endpointData["subscriptionName"] = "azure subscription"
	endpointData["scopeLevel"] = "Subscription"
	endpointData["creationMode"] = "Manual"

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

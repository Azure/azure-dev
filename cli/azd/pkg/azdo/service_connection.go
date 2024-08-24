// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/serviceendpoint"
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

func ServiceConnection(
	ctx context.Context,
	connection *azuredevops.Connection,
	projectId string, serviceConnectionName *string) (*serviceendpoint.ServiceEndpoint, error) {

	client, err := serviceendpoint.NewClient(ctx, connection)
	if err != nil {
		return nil, fmt.Errorf("creating new azdo client: %w", err)
	}

	return serviceConnectionExists(ctx, &client, &projectId, serviceConnectionName)
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
	projectName string,
	azdEnvironment environment.Environment,
	credentials *entraid.AzureCredentials,
	console input.Console) (*serviceendpoint.ServiceEndpoint, error) {

	client, err := serviceendpoint.NewClient(ctx, connection)
	if err != nil {
		return nil, fmt.Errorf("creating new azdo client: %w", err)
	}

	foundServiceConnection, err := serviceConnectionExists(ctx, &client, &projectId, &ServiceConnectionName)
	if err != nil {
		return nil, fmt.Errorf("creating service connection: looking for existing connection: %w", err)
	}

	createServiceEndpointArgs, err := createAzureRMServiceEndPointArgs(&projectId, &projectName, credentials)
	if err != nil {
		return nil, fmt.Errorf("creating Azure DevOps endpoint: %w", err)
	}

	// if a service connection exists, skip creating a new Service connection. But update the current connection only
	if foundServiceConnection != nil {
		updated, err := client.UpdateServiceEndpoint(ctx, serviceendpoint.UpdateServiceEndpointArgs{
			Endpoint:   createServiceEndpointArgs.Endpoint,
			EndpointId: foundServiceConnection.Id,
		})
		if err != nil {
			return nil, fmt.Errorf("updating service connection: %w", err)
		}
		console.MessageUxItem(ctx, &ux.DisplayedResource{
			Type: "Azure DevOps",
			Name: "Updated service connection",
		})
		return updated, nil
	}

	// Service connection not found. Creating a new one and authorizing.
	endpoint, err := client.CreateServiceEndpoint(ctx, createServiceEndpointArgs)
	if err != nil {
		return nil, fmt.Errorf("Creating new service connection: %w", err)
	}
	console.MessageUxItem(ctx, &ux.DisplayedResource{
		Type: "Azure DevOps",
		Name: "Service connection",
	})

	err = authorizeServiceConnectionToAllPipelines(ctx, projectId, endpoint, connection)
	if err != nil {
		return nil, fmt.Errorf("authorizing service connection: %w", err)
	}

	return endpoint, nil
}

func ListTypes(
	ctx context.Context,
	connection *azuredevops.Connection,
	projectId string) (*[]serviceendpoint.ServiceEndpointType, error) {

	client, err := serviceendpoint.NewClient(ctx, connection)
	if err != nil {
		return nil, fmt.Errorf("creating new azdo client: %w", err)
	}

	return client.GetServiceEndpointTypes(ctx, serviceendpoint.GetServiceEndpointTypesArgs{})
}

// creates input parameter needed to create the azure rm service connection
func createAzureRMServiceEndPointArgs(
	projectId *string,
	projectName *string,
	credentials *entraid.AzureCredentials,
) (serviceendpoint.CreateServiceEndpointArgs, error) {
	endpointScheme := "WorkloadIdentityFederation"
	endpointAuthorizationParameters := map[string]string{
		"serviceprincipalid": credentials.ClientId,
		"tenantid":           credentials.TenantId,
	}
	if credentials.ClientSecret != "" {
		endpointAuthorizationParameters["serviceprincipalkey"] = credentials.ClientSecret
		endpointAuthorizationParameters["authenticationType"] = "spnKey"
		endpointScheme = "ServicePrincipal"
	}

	endpointData := map[string]string{
		"environment":      CloudEnvironment,
		"subscriptionId":   credentials.SubscriptionId,
		"subscriptionName": "azure subscription", // fix? to sub name?
		"scopeLevel":       "Subscription",
		"creationMode":     "Manual",
	}

	endpointAuthorization := serviceendpoint.EndpointAuthorization{
		Scheme:     &endpointScheme,
		Parameters: &endpointAuthorizationParameters,
	}
	description := "Azure Service Connection created by azd"

	pRef := []serviceendpoint.ServiceEndpointProjectReference{{
		Name:        &ServiceConnectionName,
		Description: &description,
		ProjectReference: &serviceendpoint.ProjectReference{
			Id:   to.Ptr(uuid.MustParse(*projectId)),
			Name: projectName,
		}}}

	serviceEndpoint := &serviceendpoint.ServiceEndpoint{
		Type:                             to.Ptr("azurerm"),
		Owner:                            to.Ptr("library"),
		Url:                              to.Ptr("https://management.azure.com/"),
		Name:                             &ServiceConnectionName,
		IsShared:                         to.Ptr(false),
		Authorization:                    &endpointAuthorization,
		Data:                             &endpointData,
		ServiceEndpointProjectReferences: &pRef,
		Description:                      &description,
	}

	createServiceEndpointArgs := serviceendpoint.CreateServiceEndpointArgs{
		Endpoint: serviceEndpoint,
	}
	return createServiceEndpointArgs, nil
}

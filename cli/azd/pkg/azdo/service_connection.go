package azdo

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
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
		Project:  &projectId,
		Endpoint: serviceEndpoint,
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

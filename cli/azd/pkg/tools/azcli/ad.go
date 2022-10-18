package azcli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
)

func (cli *azCli) GetSignedInUserId(ctx context.Context) (string, error) {
	client, err := cli.createUsersClient(ctx)
	if err != nil {
		return "", err
	}

	userProfile, err := client.Me().Get(ctx)
	if err != nil {
		return "", fmt.Errorf("failed retrieving current user profile: %w", err)
	}

	return userProfile.Id, nil
}

func (cli *azCli) CreateOrUpdateServicePrincipal(
	ctx context.Context,
	subscriptionId string,
	applicationName string,
	roleName string,
) (json.RawMessage, error) {
	// By default the role assignment is tied to the root of the currently active subscription (in the az cli), which may not
	// be the same
	// subscription that the user has requested, so build the scope ourselves.
	scopes := azure.SubscriptionRID(subscriptionId)
	var result ServicePrincipalCredentials

	res, err := cli.runAzCommand(
		ctx,
		"ad",
		"sp",
		"create-for-rbac",
		"--scopes",
		scopes,
		"--name",
		applicationName,
		"--role",
		roleName,
		"--output",
		"json",
	)
	if isNotLoggedInMessage(res.Stderr) {
		return nil, ErrAzCliNotLoggedIn
	} else if err != nil {
		return nil, fmt.Errorf("failed running az ad sp create-for-rbac: %s: %w", res.String(), err)
	}

	if err := json.Unmarshal([]byte(res.Stdout), &result); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as a string: %w", res.Stdout, err)
	}

	// --sdk-auth arg was deprecated from the az cli. See: https://docs.microsoft.com/cli/azure/microsoft-graph-migration
	// this argument would ensure that the output from creating a Service Principal could
	// be used as input to log in to azure. See: https://github.com/Azure/login#configure-a-service-principal-with-a-secret
	// Create the credentials expected structure from the json-rawResponse
	credentials := AzureCredentials{
		ClientId:                   result.AppId,
		ClientSecret:               result.Password,
		SubscriptionId:             subscriptionId,
		TenantId:                   result.Tenant,
		ResourceManagerEndpointUrl: "https://management.azure.com/",
	}

	credentialsJson, err := json.Marshal(credentials)
	if err != nil {
		return nil, fmt.Errorf("couldn't build Azure Credential")
	}

	var resultWithAzureCredentialsModel json.RawMessage
	if err := json.Unmarshal(credentialsJson, &resultWithAzureCredentialsModel); err != nil {
		return nil, fmt.Errorf("couldn't build Azure Credential Json")
	}

	return resultWithAzureCredentialsModel, nil
}

// Creates a graph users client using credentials from the Go context.
func (cli *azCli) createUsersClient(ctx context.Context) (*graphsdk.GraphClient, error) {
	cred, err := identity.GetCredentials(ctx)
	if err != nil {
		return nil, err
	}

	options := cli.createDefaultClientOptionsBuilder(ctx).BuildCoreClientOptions()
	client, err := graphsdk.NewGraphClient(cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating Graph Users client: %w", err)
	}

	return client, nil
}

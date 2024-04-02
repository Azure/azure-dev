package cosmosdb

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

// CosmosDbService is the interface for the CosmosDbService
type CosmosDbService interface {
	ConnectionString(ctx context.Context, subId, rgName, accountName string) (string, error)
}

type cosmosDbClient struct {
	accountCreds account.SubscriptionCredentialProvider
	options      *arm.ClientOptions
}

// NewCosmosDbService creates a new instance of the CosmosDbService
func NewCosmosDbService(
	accountCreds account.SubscriptionCredentialProvider,
	options *arm.ClientOptions,
) (CosmosDbService, error) {
	return &cosmosDbClient{
		accountCreds: accountCreds,
		options:      options,
	}, nil
}

// ConnectionString returns the connection string for the CosmosDB account
func (c *cosmosDbClient) ConnectionString(ctx context.Context, subId, rgName, accountName string) (string, error) {
	credential, err := c.accountCreds.CredentialForSubscription(ctx, subId)
	if err != nil {
		return "", err
	}

	client, err := armcosmos.NewDatabaseAccountsClient(subId, credential, c.options)
	if err != nil {
		return "", err
	}
	res, err := client.ListConnectionStrings(
		ctx, rgName, accountName, &armcosmos.DatabaseAccountsClientListConnectionStringsOptions{})

	if err != nil {
		return "", err
	}

	if res.ConnectionStrings == nil {
		return "", fmt.Errorf("connection strings are nil")
	}

	if len(res.ConnectionStrings) == 0 {
		return "", fmt.Errorf("no connection strings found")
	}

	return *res.ConnectionStrings[0].ConnectionString, nil
}

package sqldb

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

// SqlDbService is the interface for the SqlDbService
type SqlDbService interface {
	ConnectionString(ctx context.Context, subId, rgName, serverName string, dbName string) (string, error)
}

type sqlDbClient struct {
	accountCreds account.SubscriptionCredentialProvider
	options      *arm.ClientOptions
}

// NewSqlDbService creates a new instance of the SqlDbService
func NewSqlDbService(
	accountCreds account.SubscriptionCredentialProvider,
	options *arm.ClientOptions,
) (SqlDbService, error) {
	return &sqlDbClient{
		accountCreds: accountCreds,
		options:      options,
	}, nil
}

// ConnectionString returns the connection string for the CosmosDB account
func (c *sqlDbClient) ConnectionString(ctx context.Context, subId, rgName, serverName, dbName string) (string, error) {
	credential, err := c.accountCreds.CredentialForSubscription(ctx, subId)
	if err != nil {
		return "", err
	}

	clientFactory, err := armsql.NewClientFactory(subId, credential, c.options)
	if err != nil {
		return "", err
	}

	// get server fully qualified domain name
	res, err := clientFactory.NewServersClient().Get(ctx, rgName, serverName, &armsql.ServersClientGetOptions{Expand: nil})
	if err != nil {
		return "", fmt.Errorf("failed getting server '%s' for resource group '%s'", serverName, rgName)
	}

	if res.Server.Properties == nil {
		return "", fmt.Errorf("failed getting server properties from server '%s'", serverName)
	}

	if res.Server.Properties.FullyQualifiedDomainName == nil || len(*res.Server.Properties.FullyQualifiedDomainName) == 0 {
		return "", fmt.Errorf("failed getting fully qualified domain name from server '%s'", serverName)
	}

	var initialCatalog string
	if dbName != "" {
		initialCatalog = fmt.Sprintf("Initial Catalog=%s;", dbName)
	}

	return fmt.Sprintf("Server=tcp:%s,1433;Encrypt=True;%s"+
		"TrustServerCertificate=False;Connection Timeout=30;Authentication=\"Active Directory Default\";",
		*res.Server.Properties.FullyQualifiedDomainName, initialCatalog), nil
}

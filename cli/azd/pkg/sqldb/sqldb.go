package sqldb

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql/v2"
)

// SqlDbService is the interface for the SqlDbService
type SqlDbService interface {
	ConnectionString(ctx context.Context, subId, rgName, serverName string, dbName string) (string, error)
}

type sqlDbClient struct {
	credential azcore.TokenCredential
	options    *arm.ClientOptions
}

// NewSqlDbService creates a new instance of the SqlDbService
func NewSqlDbService(
	credential azcore.TokenCredential,
	options *arm.ClientOptions,
) (SqlDbService, error) {
	return &sqlDbClient{
		credential: credential,
		options:    options,
	}, nil
}

// ConnectionString returns the connection string for the CosmosDB account
func (c *sqlDbClient) ConnectionString(ctx context.Context, subId, rgName, serverName, dbName string) (string, error) {
	clientFactory, err := armsql.NewClientFactory(subId, c.credential, c.options)
	if err != nil {
		return "", err
	}

	// get server fully qualified domain name
	res, err := clientFactory.NewServersClient().Get(ctx, rgName, serverName, &armsql.ServersClientGetOptions{Expand: nil})
	if err != nil {
		return "", fmt.Errorf("failed getting server '%s' for resource group '%s'", serverName, rgName)
	}

	serverDomain := *res.Server.Properties.FullyQualifiedDomainName

	if serverDomain == "" {
		return "", fmt.Errorf("failed getting fully qualified domain name from server '%s'", serverName)
	}

	// connection string for the server with no database
	if dbName == "" {
		return fmt.Sprintf("Server=tcp:%s,1433;Encrypt=True;TrustServerCertificate=False;"+
			"Connection Timeout=30;Authentication=\"Active Directory Default\";", serverDomain), nil
	}

	return fmt.Sprintf("Server=tcp:%s,1433;Encrypt=True;Initial Catalog=%s;"+
		"TrustServerCertificate=False;Connection Timeout=30;Authentication=\"Active Directory Default\";",
		serverDomain, dbName), nil
}
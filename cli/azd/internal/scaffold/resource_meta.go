// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

// ResourceMeta contains the metadata for a resource.
type ResourceMeta struct {
	// ResourceType is the resource type.
	ResourceType string

	//
	ParentResourceType string

	// ApiVersion is the api version for the resource.
	ApiVersion string

	// Variables are the variables for the resource.
	// The key is the variable name and the value is the expression.
	Variables map[string]string
}

// Resources that are supported by the scaffold.
var Resources = []ResourceMeta{
	// To register a new resource from AVM modules in resources.bicept:
	//    cd tools/avmres && go run main.go
	// and add the new resource to this list.
	{
		ResourceType: "Microsoft.App/containerApps",
		ApiVersion:   "2023-05-01",
	},
	{
		ResourceType: "Microsoft.App/managedEnvironments",
		ApiVersion:   "2023-05-01",
	},
	{
		ResourceType: "Microsoft.Cache/redis",
		ApiVersion:   "2024-03-01",
		Variables: map[string]string{
			"REDIS_HOST":     "${.properties.hostName}",
			"REDIS_PORT":     "6380",
			"REDIS_PASSWORD": "${vault.}",
			"REDIS_URL":      "redis://${REDIS_HOST}:${REDIS_PORT}",
			"REDIS_ENDPOINT": "${REDIS_HOST}:${REDIS_PORT}",
		},
	},
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		ApiVersion:   "2023-05-01",
	},
	{
		ResourceType: "Microsoft.ContainerRegistry/registries",
		ApiVersion:   "2023-06-01-preview",
	},
	{
		ResourceType: "Microsoft.DBforMySQL/flexibleServers",
		ApiVersion:   "2023-12-30",
		Variables: map[string]string{
			"MYSQL_DATABASE": "${spec.name}",
			"MYSQL_HOST":     "${.properties.fullyQualifiedDomainName}",
			"MYSQL_USERNAME": "${.properties.administratorLogin}",
			"MYSQL_PORT":     "3306",
			"MYSQL_PASSWORD": "${vault.}",
			"MYSQL_URL":      "mysql://${MYSQL_USERNAME}:${MYSQL_PASSWORD}@${MYSQL_HOST}:3306/${MYSQL_DATABASE}",
		},
	},
	{
		ResourceType: "Microsoft.DBforPostgreSQL/flexibleServers",
		ApiVersion:   "2022-12-01",
		Variables: map[string]string{
			"POSTGRES_DATABASE": "${spec.name}",
			"POSTGRES_HOST":     "${.properties.fullyQualifiedDomainName}",
			"POSTGRES_USERNAME": "${.properties.administratorLogin}",
			"POSTGRES_PORT":     "5432",
			"POSTGRES_PASSWORD": "${vault.}",
			"POSTGRES_URL":      "postgresql://${POSTGRES_USERNAME}:${POSTGRES_PASSWORD}@${POSTGRES_HOST}:5432/${POSTGRES_DATABASE}",
		},
	},
	{
		ResourceType: "Microsoft.DocumentDB/databaseAccounts",
		ApiVersion:   "2023-04-15",
	},
	{
		ResourceType: "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases",
		ApiVersion:   "2023-04-15",
		Variables: map[string]string{
			"MONGODB_URL": "${vault.}",
		},
	},
	{
		ResourceType: "Microsoft.EventHub/namespaces",
		ApiVersion:   "2024-01-01",
		Variables: map[string]string{
			"AZURE_EVENT_HUBS_NAME": "${.name}",
			"AZURE_EVENT_HUBS_HOST": "${host .properties.serviceBusEndpoint}",
		},
	},
	{
		ResourceType: "Microsoft.KeyVault/vaults",
		ApiVersion:   "2022-07-01",
	},
	{
		ResourceType: "Microsoft.ManagedIdentity/userAssignedIdentities",
		ApiVersion:   "2023-01-31",
	},
	{
		ResourceType: "Microsoft.ServiceBus/namespaces",
		ApiVersion:   "2022-10-01-preview",
		Variables: map[string]string{
			"AZURE_SERVICE_BUS_NAME": "${.name}",
			"AZURE_SERVICE_BUS_HOST": "${host .properties.serviceBusEndpoint}",
		},
	},
	{
		ResourceType: "Microsoft.Storage/storageAccounts",
		ApiVersion:   "2023-05-01",
		Variables: map[string]string{
			"AZURE_STORAGE_ACCOUNT_NAME":  "${.name}",
			"AZURE_STORAGE_BLOB_ENDPOINT": "${.properties.primaryEndpoints.blob}",
		},
	},
}

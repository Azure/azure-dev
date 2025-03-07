// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

// ResourceMeta contains the metadata for a resource.
type ResourceMeta struct {
	// ResourceType is the resource type.
	ResourceType string

	// ResourceKind is the resource kind.
	ResourceKind string

	// ParentForEval is the parent resource used for evaluation.
	// Note: This is temporarily used for displaying sub-resources and can be moved into the expression language later.
	ParentForEval string

	// ApiVersion is the api version for the resource.
	ApiVersion string

	// StandardVarPrefix is the standard variable prefix for the resource.
	StandardVarPrefix string

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
		ResourceType:      "Microsoft.App/containerApps",
		ApiVersion:        "2023-05-01",
		StandardVarPrefix: "${upper .name}",
		Variables: map[string]string{
			"baseUrl": "${.properties.configuration.ingress.fqdn}",
		},
	},
	{
		ResourceType: "Microsoft.App/managedEnvironments",
		ApiVersion:   "2023-05-01",
	},
	{
		ResourceType:      "Microsoft.Cache/redis",
		ApiVersion:        "2024-03-01",
		StandardVarPrefix: "REDIS",
		Variables: map[string]string{
			"host":     "${.properties.hostName}",
			"port":     "6380",
			"password": "${vault.redis-password}",
			"url":      "${vault.redis-url}",
			"endpoint": "${host}:${port}",
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
		ResourceType:      "Microsoft.DBforMySQL/flexibleServers",
		ApiVersion:        "2023-12-30",
		StandardVarPrefix: "MYSQL",
		Variables: map[string]string{
			"database": "${spec.name}",
			"host":     "${.properties.fullyQualifiedDomainName}",
			"username": "${.properties.administratorLogin}",
			"port":     "3306",
			"password": "${vault.mysql-password}",
			"url":      "mysql://${username}:${password}@${host}:${port}/${database}",
		},
	},
	{
		ResourceType:      "Microsoft.DBforPostgreSQL/flexibleServers",
		ApiVersion:        "2022-12-01",
		StandardVarPrefix: "POSTGRES",
		Variables: map[string]string{
			"database": "${spec.name}",
			"host":     "${.properties.fullyQualifiedDomainName}",
			"username": "${.properties.administratorLogin}",
			"port":     "5432",
			"password": "${vault.postgres-password}",
			"url":      "postgresql://${username}:${password}@${host}:${port}/${database}",
		},
	},
	{
		ResourceType:      "Microsoft.DocumentDB/databaseAccounts/sqlDatabases",
		ApiVersion:        "2023-04-15",
		ParentForEval:     "Microsoft.DocumentDB/databaseAccounts",
		StandardVarPrefix: "AZURE_COSMOS",
		Variables: map[string]string{
			"endpoint": "${.properties.documentEndpoint}",
		},
	},
	{
		ResourceType:      "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases",
		ApiVersion:        "2023-04-15",
		StandardVarPrefix: "MONGODB",
		Variables: map[string]string{
			"url": "${vault.mongodb-url}",
		},
	},
	{
		ResourceType:      "Microsoft.EventHub/namespaces",
		ApiVersion:        "2024-01-01",
		StandardVarPrefix: "AZURE_EVENT_HUBS",
		Variables: map[string]string{
			"name": "${.name}",
			"host": "${host .properties.serviceBusEndpoint}",
		},
	},
	{
		ResourceType:      "Microsoft.KeyVault/vaults",
		ApiVersion:        "2022-07-01",
		StandardVarPrefix: "AZURE_KEY_VAULT",
		Variables: map[string]string{
			"name":     "${.name}",
			"endpoint": "${.properties.vaultUri}",
		},
	},
	{
		ResourceType: "Microsoft.ManagedIdentity/userAssignedIdentities",
		ApiVersion:   "2023-01-31",
	},
	{
		ResourceType:      "Microsoft.ServiceBus/namespaces",
		ApiVersion:        "2022-10-01-preview",
		StandardVarPrefix: "AZURE_SERVICE_BUS",
		Variables: map[string]string{
			"name": "${.name}",
			"host": "${host .properties.serviceBusEndpoint}",
		},
	},
	{
		ResourceType:      "Microsoft.Storage/storageAccounts",
		ApiVersion:        "2023-05-01",
		StandardVarPrefix: "AZURE_STORAGE",
		Variables: map[string]string{
			"accountName":  "${.name}",
			"blobEndpoint": "${.properties.primaryEndpoints.blob}",
		},
	},
	{
		ResourceType:      "Microsoft.MachineLearningServices/workspaces",
		ResourceKind:      "Project",
		ApiVersion:        "2024-10-01",
		StandardVarPrefix: "AZURE_AI_PROJECT",
		Variables: map[string]string{
			"connectionString": "${aiProjectConnectionString .id .properties.discoveryUrl}",
		},
	},
}

// EnvVars creates a map of environment variables with the given prefix and variable names.
func EnvVars(prefix string, variables map[string]string) map[string]string {
	result := make(map[string]string)
	for name, value := range variables {
		result[EnvVarName(prefix, name)] = value
	}
	return result
}

// EnvVarName creates an environment variable name by concatenating the prefix and the variable name.
func EnvVarName(prefix string, varName string) string {
	if prefix == "" {
		return AlphaSnakeUpperFromCasing(varName)
	}
	return prefix + "_" + AlphaSnakeUpperFromCasing(varName)
}

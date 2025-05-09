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

	// Variables are the variables exposed by this resource for connecting to application code.
	//
	// To evaluate the actual values, see [Eval].
	Variables map[string]string

	// RoleAssignments are related role assignments the resource.
	RoleAssignments RoleAssignments
}

type RoleAssignments struct {
	Read  []RoleAssignment
	Write []RoleAssignment
}

type RoleAssignmentScope int32

const (
	RoleAssignmentScopeResource RoleAssignmentScope = iota
	RoleAssignmentScopeGroup
)

type RoleAssignment struct {
	// A name for the role assignment that is unique within the resource.
	// This should be a Bicep-friendly name.
	Name string

	// The Microsoft defined role definition ID.
	// See https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles
	RoleDefinitionId string

	// Friendly name for display purposes.
	RoleDefinitionName string

	// The Scope of the role assignment.
	// This is the resource ID of the resource to which the role assignment applies.
	// When empty, the scope is the resource itself.
	Scope RoleAssignmentScope
}

// List of resources that are supported by scaffold.
var Resources = []ResourceMeta{
	// To register a newly added resource, run the following command:
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
		ApiVersion:   "2025-04-01-preview",
	},
	{
		ResourceType:      "Microsoft.CognitiveServices/accounts/deployments",
		ApiVersion:        "2025-04-01-preview",
		ParentForEval:     "Microsoft.CognitiveServices/accounts",
		StandardVarPrefix: "AZURE_OPENAI",
		Variables: map[string]string{
			"endpoint": "${.properties.endpoint}",
		},
	},
	{
		ResourceType:      "Microsoft.CognitiveServices/accounts/projects",
		ResourceKind:      "AIServices",
		ApiVersion:        "2025-04-01-preview",
		StandardVarPrefix: "AZURE_AI_PROJECT",
		Variables: map[string]string{
			"endpoint": "${aiProjectEndpoint .properties.endpoints}",
		},
		RoleAssignments: RoleAssignments{
			Write: []RoleAssignment{
				{
					Name:               "AzureAIDeveloper",
					RoleDefinitionName: "Azure AI Developer",
					RoleDefinitionId:   "64702f94-c441-49e6-a78b-ef80e0188fee",
					Scope:              RoleAssignmentScopeGroup,
				},
				{
					Name:               "CognitiveServicesUser",
					RoleDefinitionName: "Cognitive Services User",
					RoleDefinitionId:   "a97b65f3-24c7-4388-baec-2e87135dc908",
					Scope:              RoleAssignmentScopeGroup,
				},
			},
		},
	},
	{
		ResourceType: "Microsoft.ContainerRegistry/registries",
		ApiVersion:   "2023-06-01-preview",
	},
	{
		ResourceType:      "Microsoft.DBforMySQL/flexibleServers/databases",
		ApiVersion:        "2023-12-30",
		StandardVarPrefix: "MYSQL",
		ParentForEval:     "Microsoft.DBforMySQL/flexibleServers",
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
		ResourceType:      "Microsoft.DBforPostgreSQL/flexibleServers/databases",
		ApiVersion:        "2022-12-01",
		StandardVarPrefix: "POSTGRES",
		ParentForEval:     "Microsoft.DBforPostgreSQL/flexibleServers",
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
		RoleAssignments: RoleAssignments{
			Write: []RoleAssignment{
				{
					Name:               "HubDataOwner",
					RoleDefinitionName: "Azure Event Hubs Data Owner",
					RoleDefinitionId:   "f526a384-b230-433a-b45c-95f59c4a2dec",
				},
			},
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
		RoleAssignments: RoleAssignments{
			Read: []RoleAssignment{
				{
					Name:               "Reader",
					RoleDefinitionName: "Key Vault Secrets User",
					RoleDefinitionId:   "4633458b-17de-408a-b874-0445c86b69e6",
				},
			},
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
		RoleAssignments: RoleAssignments{
			Write: []RoleAssignment{
				{
					Name:               "BusDataOwner",
					RoleDefinitionName: "Azure Service Bus Data Owner",
					RoleDefinitionId:   "090c5cfd-751d-490a-894a-3ce6f1109419",
				},
			},
		},
	},
	{
		ResourceType:      "Microsoft.Storage/storageAccounts",
		ApiVersion:        "2023-05-01",
		StandardVarPrefix: "AZURE_STORAGE",
		RoleAssignments: RoleAssignments{
			Read: []RoleAssignment{
				{
					Name:               "Reader",
					RoleDefinitionName: "Storage Blob Data Reader",
					RoleDefinitionId:   "2a2b9908-6ea1-4ae2-8e65-a410df84e7d1",
				},
			},
			Write: []RoleAssignment{
				{
					Name:               "Contributor",
					RoleDefinitionName: "Storage Blob Data Contributor",
					RoleDefinitionId:   "ba92f5b4-2d11-453d-a403-e96b0029c9fe",
				},
			},
		},
		Variables: map[string]string{
			"accountName":  "${.name}",
			"blobEndpoint": "${.properties.primaryEndpoints.blob}",
		},
	},
	{
		ResourceType:      "Microsoft.Search/searchServices",
		ApiVersion:        "2025-02-01-preview",
		StandardVarPrefix: "AZURE_AI_SEARCH",
		Variables: map[string]string{
			"endpoint": "${.properties.endpoint}",
		},
		RoleAssignments: RoleAssignments{
			Write: []RoleAssignment{
				{
					Name:               "IdxContributor",
					RoleDefinitionName: "Search Index Data Contributor",
					RoleDefinitionId:   "8ebe5a00-799e-43f5-93ac-243d3dce84a7",
				},
				{
					Name:               "SvcContributor",
					RoleDefinitionName: "Search Service Contributor",
					RoleDefinitionId:   "7ca78c08-252a-4471-8644-bb5ff32d4ba0",
				},
			},
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

func ResourceMetaFromType(resourceType string) (ResourceMeta, bool) {
	for _, resource := range Resources {
		if resource.ResourceType == resourceType {
			return resource, true
		}
	}
	return ResourceMeta{}, false
}

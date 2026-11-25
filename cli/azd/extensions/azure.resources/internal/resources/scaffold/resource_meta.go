// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import "fmt"

// ResourceMeta contains the metadata for a resource.
type ResourceMeta struct {
	// ResourceType is the resource type.
	ResourceType string

	// ResourceKind is the resource kind.
	ResourceKind string

	// ParentResourceType is the parent Azure resource used when linking an existing sub-resource.
	ParentResourceType string

	// ApiVersion is the api version for the resource.
	ApiVersion string

	// StandardVarPrefix is the standard variable prefix for the resource.
	StandardVarPrefix string

	// bindings builds Bicep connection values for an existing resource.
	// A nil function means generated host bindings are not supported for this resource type.
	bindings func(resource string) map[string]string

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
	{
		ResourceType: "Microsoft.Web/sites",
		ApiVersion:   "2024-04-01",
	},
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
	},
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		ApiVersion:   "2025-04-01-preview",
	},
	{
		ResourceType:       "Microsoft.CognitiveServices/accounts/deployments",
		ApiVersion:         "2025-04-01-preview",
		ParentResourceType: "Microsoft.CognitiveServices/accounts",
		StandardVarPrefix:  "AZURE_OPENAI",
		bindings: func(resource string) map[string]string {
			return map[string]string{"endpoint": resource + ".properties.endpoint"}
		},
	},
	{
		ResourceType:      "Microsoft.CognitiveServices/accounts/projects",
		ResourceKind:      "AIServices",
		ApiVersion:        "2025-04-01-preview",
		StandardVarPrefix: "AZURE_AI_PROJECT",
		bindings: func(resource string) map[string]string {
			return map[string]string{"endpoint": resource + ".properties.endpoints['AI Foundry API']"}
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
		ResourceType:       "Microsoft.DocumentDB/databaseAccounts/sqlDatabases",
		ApiVersion:         "2023-04-15",
		ParentResourceType: "Microsoft.DocumentDB/databaseAccounts",
		StandardVarPrefix:  "AZURE_COSMOS",
		bindings: func(resource string) map[string]string {
			return map[string]string{"endpoint": resource + ".properties.documentEndpoint"}
		},
	},
	{
		ResourceType: "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases",
		ApiVersion:   "2023-04-15",
	},
	{
		ResourceType:      "Microsoft.EventHub/namespaces",
		ApiVersion:        "2024-01-01",
		StandardVarPrefix: "AZURE_EVENT_HUBS",
		bindings:          messagingBindings,
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
		bindings: func(resource string) map[string]string {
			return map[string]string{
				"name":     resource + ".name",
				"endpoint": resource + ".properties.vaultUri",
			}
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
		bindings:          messagingBindings,
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
		bindings: func(resource string) map[string]string {
			return map[string]string{
				"accountName":  resource + ".name",
				"blobEndpoint": resource + ".properties.primaryEndpoints.blob",
			}
		},
	},
	{
		ResourceType:      "Microsoft.Search/searchServices",
		ApiVersion:        "2025-02-01-preview",
		StandardVarPrefix: "AZURE_AI_SEARCH",
		bindings: func(resource string) map[string]string {
			return map[string]string{"endpoint": resource + ".properties.endpoint"}
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

// BicepBindings returns connection values as Bicep expressions referencing the given resource symbol.
// It returns an error when this resource type does not support generated host bindings.
func (m ResourceMeta) BicepBindings(resource string) (map[string]string, error) {
	if m.bindings == nil {
		return nil, fmt.Errorf("resource type %q does not support generated host bindings", m.ResourceType)
	}
	return m.bindings(resource), nil
}

// messagingBindings returns the namespace name and host expressions shared by Event Hubs and Service Bus.
func messagingBindings(resource string) map[string]string {
	return map[string]string{
		"name": resource + ".name",
		"host": fmt.Sprintf("split(split(%s.properties.serviceBusEndpoint, '//')[1], ':')[0]", resource),
	}
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

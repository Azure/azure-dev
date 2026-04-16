// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_AllResourceTypes(t *testing.T) {
	all := AllResourceTypes()

	require.NotEmpty(t, all)

	// Verify every known constant is present
	expected := []ResourceType{
		ResourceTypeDbRedis,
		ResourceTypeDbPostgres,
		ResourceTypeDbMySql,
		ResourceTypeDbMongo,
		ResourceTypeDbCosmos,
		ResourceTypeHostAppService,
		ResourceTypeHostContainerApp,
		ResourceTypeOpenAiModel,
		ResourceTypeMessagingEventHubs,
		ResourceTypeMessagingServiceBus,
		ResourceTypeStorage,
		ResourceTypeAiProject,
		ResourceTypeAiSearch,
		ResourceTypeKeyVault,
	}

	require.Equal(t, expected, all)
}

func Test_ResourceType_String(t *testing.T) {
	tests := []struct {
		name     string
		rt       ResourceType
		expected string
	}{
		{"Redis", ResourceTypeDbRedis, "Redis"},
		{"PostgreSQL", ResourceTypeDbPostgres, "PostgreSQL"},
		{"MySQL", ResourceTypeDbMySql, "MySQL"},
		{"MongoDB", ResourceTypeDbMongo, "MongoDB"},
		{"CosmosDB", ResourceTypeDbCosmos, "CosmosDB"},
		{
			"App Service",
			ResourceTypeHostAppService,
			"App Service",
		},
		{
			"Container App",
			ResourceTypeHostContainerApp,
			"Container App",
		},
		{
			"Open AI Model",
			ResourceTypeOpenAiModel,
			"Open AI Model",
		},
		{
			"Event Hubs",
			ResourceTypeMessagingEventHubs,
			"Event Hubs",
		},
		{
			"Service Bus",
			ResourceTypeMessagingServiceBus,
			"Service Bus",
		},
		{
			"Storage Account",
			ResourceTypeStorage,
			"Storage Account",
		},
		{"Foundry", ResourceTypeAiProject, "Foundry"},
		{"AI Search", ResourceTypeAiSearch, "AI Search"},
		{"Key Vault", ResourceTypeKeyVault, "Key Vault"},
		{
			"unknown returns empty",
			ResourceType("unknown.type"),
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.rt.String())
		})
	}
}

func Test_ResourceType_AzureResourceType(t *testing.T) {
	tests := []struct {
		name     string
		rt       ResourceType
		expected string
	}{
		{
			"AppService",
			ResourceTypeHostAppService,
			"Microsoft.Web/sites",
		},
		{
			"ContainerApp",
			ResourceTypeHostContainerApp,
			"Microsoft.App/containerApps",
		},
		{
			"Redis",
			ResourceTypeDbRedis,
			"Microsoft.Cache/redis",
		},
		{
			"Postgres",
			ResourceTypeDbPostgres,
			"Microsoft.DBforPostgreSQL/flexibleServers/databases",
		},
		{
			"MySQL",
			ResourceTypeDbMySql,
			"Microsoft.DBforMySQL/flexibleServers/databases",
		},
		{
			"MongoDB",
			ResourceTypeDbMongo,
			"Microsoft.DocumentDB/databaseAccounts/mongodbDatabases",
		},
		{
			"OpenAI Model",
			ResourceTypeOpenAiModel,
			"Microsoft.CognitiveServices/accounts/deployments",
		},
		{
			"CosmosDB",
			ResourceTypeDbCosmos,
			"Microsoft.DocumentDB/databaseAccounts/sqlDatabases",
		},
		{
			"EventHubs",
			ResourceTypeMessagingEventHubs,
			"Microsoft.EventHub/namespaces",
		},
		{
			"ServiceBus",
			ResourceTypeMessagingServiceBus,
			"Microsoft.ServiceBus/namespaces",
		},
		{
			"Storage",
			ResourceTypeStorage,
			"Microsoft.Storage/storageAccounts",
		},
		{
			"KeyVault",
			ResourceTypeKeyVault,
			"Microsoft.KeyVault/vaults",
		},
		{
			"AiProject",
			ResourceTypeAiProject,
			"Microsoft.CognitiveServices/accounts/projects",
		},
		{
			"AiSearch",
			ResourceTypeAiSearch,
			"Microsoft.Search/searchServices",
		},
		{
			"unknown returns empty",
			ResourceType("custom.thing"),
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.rt.AzureResourceType())
		})
	}
}

func Test_AllResourceTypes_StringAndAzureType_Complete(
	t *testing.T,
) {
	// Every type in AllResourceTypes should have a non-empty
	// String() and AzureResourceType() value.
	for _, rt := range AllResourceTypes() {
		t.Run(string(rt), func(t *testing.T) {
			require.NotEmpty(
				t,
				rt.String(),
				"String() should not be empty for %s",
				rt,
			)
			require.NotEmpty(
				t,
				rt.AzureResourceType(),
				"AzureResourceType() should not be empty for %s",
				rt,
			)
		})
	}
}

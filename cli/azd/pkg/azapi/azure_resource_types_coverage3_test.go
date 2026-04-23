// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_GetResourceTypeDisplayName_AllCases_Coverage3(t *testing.T) {
	cases := []struct {
		resourceType AzureResourceType
		expected     string
	}{
		{AzureResourceTypeResourceGroup, "Resource group"},
		{AzureResourceTypeStorageAccount, "Storage account"},
		{AzureResourceTypeKeyVault, "Key Vault"},
		{AzureResourceTypeManagedHSM, "Managed HSM"},
		{AzureResourceTypePortalDashboard, "Portal dashboard"},
		{AzureResourceTypeAppInsightComponent, "Application Insights"},
		{AzureResourceTypeAutomationAccount, "Automation account"},
		{AzureResourceTypeLogAnalyticsWorkspace, "Log Analytics workspace"},
		{AzureResourceTypeWebSite, "Web App"},
		{AzureResourceTypeStaticWebSite, "Static Web App"},
		{AzureResourceTypeContainerApp, "Container App"},
		{AzureResourceTypeContainerAppJob, "Container App Job"},
		{AzureResourceTypeContainerAppEnvironment, "Container Apps Environment"},
		{AzureResourceTypeSreAgent, "SRE Agent"},
		{AzureResourceTypeServiceBusNamespace, "Service Bus Namespace"},
		{AzureResourceTypeEventHubsNamespace, "Event Hubs Namespace"},
		{AzureResourceTypeServicePlan, "App Service plan"},
		{AzureResourceTypeCosmosDb, "Azure Cosmos DB"},
		{AzureResourceTypeDocumentDB, "Azure DocumentDB"},
		{AzureResourceTypeApim, "Azure API Management"},
		{AzureResourceTypeCacheForRedis, "Cache for Redis"},
		{AzureResourceTypeRedisEnterprise, "Redis Enterprise"},
		{AzureResourceTypeSqlServer, "Azure SQL Server"},
		{AzureResourceTypePostgreSqlServer, "Azure Database for PostgreSQL flexible server"},
		{AzureResourceTypeMySqlServer, "Azure Database for MySQL flexible server"},
		{AzureResourceTypeCDNProfile, "Azure Front Door / CDN profile"},
		{AzureResourceTypeLoadTest, "Load Tests"},
		{AzureResourceTypeVirtualNetwork, "Virtual Network"},
		{AzureResourceTypeContainerRegistry, "Container Registry"},
		{AzureResourceTypeManagedCluster, "AKS Managed Cluster"},
		{AzureResourceTypeAgentPool, "AKS Agent Pool"},
		{AzureResourceTypeCognitiveServiceAccount, "Azure AI Services"},
		{AzureResourceTypeCognitiveServiceAccountDeployment, "Azure AI Services Model Deployment"},
		{AzureResourceTypeCognitiveServiceAccountProject, "Foundry project"},
		{AzureResourceTypeCognitiveServiceAccountCapabilityHost, "Foundry capability host"},
		{AzureResourceTypeSearchService, "Search service"},
		{AzureResourceTypeVideoIndexer, "Video Indexer"},
		{AzureResourceTypePrivateEndpoint, "Private Endpoint"},
		{AzureResourceTypeDevCenter, "Dev Center"},
		{AzureResourceTypeDevCenterProject, "Dev Center Project"},
		{AzureResourceTypeMachineLearningWorkspace, "Machine Learning Workspace"},
		{AzureResourceTypeMachineLearningEndpoint, "Machine Learning Endpoint"},
		{AzureResourceTypeMachineLearningConnection, "Machine Learning Connection"},
		{AzureResourceTypeAppConfig, ""},   // not in switch
		{AzureResourceTypeWebSiteSlot, ""}, // not in switch
		{AzureResourceType("unknown.type"), ""},
	}

	for _, tc := range cases {
		t.Run(string(tc.resourceType), func(t *testing.T) {
			result := GetResourceTypeDisplayName(tc.resourceType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

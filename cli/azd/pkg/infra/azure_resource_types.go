// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import "strings"

type AzureResourceType string

const (
	AzureResourceTypeApim                    AzureResourceType = "Microsoft.ApiManagement/service"
	AzureResourceTypeAppConfig               AzureResourceType = "Microsoft.AppConfiguration/configurationStores"
	AzureResourceTypeAppInsightComponent     AzureResourceType = "Microsoft.Insights/components"
	AzureResourceTypeCacheForRedis           AzureResourceType = "Microsoft.Cache/redis"
	AzureResourceTypeCDNProfile              AzureResourceType = "Microsoft.Cdn/profiles"
	AzureResourceTypeCosmosDb                AzureResourceType = "Microsoft.DocumentDB/databaseAccounts"
	AzureResourceTypeContainerApp            AzureResourceType = "Microsoft.App/containerApps"
	AzureResourceTypeSpringApp               AzureResourceType = "Microsoft.AppPlatform/Spring"
	AzureResourceTypeContainerAppEnvironment AzureResourceType = "Microsoft.App/managedEnvironments"
	AzureResourceTypeDeployment              AzureResourceType = "Microsoft.Resources/deployments"
	AzureResourceTypeKeyVault                AzureResourceType = "Microsoft.KeyVault/vaults"
	AzureResourceTypeManagedHSM              AzureResourceType = "Microsoft.KeyVault/managedHSMs"
	AzureResourceTypeLoadTest                AzureResourceType = "Microsoft.LoadTestService/loadTests"
	AzureResourceTypeLogAnalyticsWorkspace   AzureResourceType = "Microsoft.OperationalInsights/workspaces"
	AzureResourceTypePortalDashboard         AzureResourceType = "Microsoft.Portal/dashboards"
	AzureResourceTypePostgreSqlServer        AzureResourceType = "Microsoft.DBforPostgreSQL/flexibleServers"
	AzureResourceTypeMySqlServer             AzureResourceType = "Microsoft.DBforMySQL/flexibleServers"
	AzureResourceTypeResourceGroup           AzureResourceType = "Microsoft.Resources/resourceGroups"
	AzureResourceTypeStorageAccount          AzureResourceType = "Microsoft.Storage/storageAccounts"
	AzureResourceTypeStaticWebSite           AzureResourceType = "Microsoft.Web/staticSites"
	AzureResourceTypeServiceBusNamespace     AzureResourceType = "Microsoft.ServiceBus/namespaces"
	AzureResourceTypeServicePlan             AzureResourceType = "Microsoft.Web/serverfarms"
	AzureResourceTypeSqlServer               AzureResourceType = "Microsoft.Sql/servers"
	AzureResourceTypeVirtualNetwork          AzureResourceType = "Microsoft.Network/virtualNetworks"
	AzureResourceTypeWebSite                 AzureResourceType = "Microsoft.Web/sites"
	AzureResourceTypeContainerRegistry       AzureResourceType = "Microsoft.ContainerRegistry/registries"
	AzureResourceTypeManagedCluster          AzureResourceType = "Microsoft.ContainerService/managedClusters"
	AzureResourceTypeAgentPool               AzureResourceType = "Microsoft.ContainerService/managedClusters/agentPools"
	AzureResourceTypeCognitiveServiceAccount AzureResourceType = "Microsoft.CognitiveServices/accounts"
	AzureResourceTypeSearchService           AzureResourceType = "Microsoft.Search/searchServices"
	AzureResourceTypeVideoIndexer            AzureResourceType = "Microsoft.VideoIndexer/accounts"
	AzurePrivateEndpoint                     AzureResourceType = "Microsoft.Network/privateEndpoints"
	AzureDevCenter                           AzureResourceType = "Microsoft.DevCenter/devcenters"
	AzureDevCenterProject                    AzureResourceType = "Microsoft.DevCenter/projects"
	AzureMachineLearningWorkspace            AzureResourceType = "Microsoft.MachineLearningServices/workspaces"
	//nolint:lll
	AzureMachineLearningEndpoint   AzureResourceType = "Microsoft.MachineLearningServices/workspaces/onlineEndpoints"
	AzureMachineLearningConnection AzureResourceType = "Microsoft.MachineLearningServices/workspaces/connections"
)

const resourceLevelSeparator = "/"

// GetResourceTypeDisplayName retrieves the display name for the given resource type.
// If the display name was not found for the given resource type, an empty string is returned instead.
func GetResourceTypeDisplayName(resourceType AzureResourceType) string {
	// Azure Resource Manager does not offer an API for obtaining display name for resource types.
	// Display names for Azure resource types in Azure Portal are encoded in UX definition files instead.
	// As a result, we provide static translations for known resources below. These are obtained from the Azure Portal.
	switch resourceType {
	case AzureResourceTypeResourceGroup:
		return "Resource group"
	case AzureResourceTypeStorageAccount:
		return "Storage account"
	case AzureResourceTypeKeyVault:
		return "Key Vault"
	case AzureResourceTypeManagedHSM:
		return "Managed HSM"
	case AzureResourceTypePortalDashboard:
		return "Portal dashboard"
	case AzureResourceTypeAppInsightComponent:
		return "Application Insights"
	case AzureResourceTypeLogAnalyticsWorkspace:
		return "Log Analytics workspace"
	case AzureResourceTypeWebSite:
		return "Web App"
	case AzureResourceTypeStaticWebSite:
		return "Static Web App"
	case AzureResourceTypeContainerApp:
		return "Container App"
	case AzureResourceTypeContainerAppEnvironment:
		return "Container Apps Environment"
	case AzureResourceTypeServiceBusNamespace:
		return "Service Bus Namespace"
	case AzureResourceTypeServicePlan:
		return "App Service plan"
	case AzureResourceTypeCosmosDb:
		return "Azure Cosmos DB"
	case AzureResourceTypeApim:
		return "Azure API Management"
	case AzureResourceTypeCacheForRedis:
		return "Cache for Redis"
	case AzureResourceTypeSqlServer:
		return "Azure SQL Server"
	case AzureResourceTypePostgreSqlServer:
		return "Azure Database for PostgreSQL flexible server"
	case AzureResourceTypeMySqlServer:
		return "Azure Database for MySQL flexible server"
	case AzureResourceTypeCDNProfile:
		return "Azure Front Door / CDN profile"
	case AzureResourceTypeLoadTest:
		return "Load Tests"
	case AzureResourceTypeVirtualNetwork:
		return "Virtual Network"
	case AzureResourceTypeContainerRegistry:
		return "Container Registry"
	case AzureResourceTypeManagedCluster:
		return "AKS Managed Cluster"
	case AzureResourceTypeAgentPool:
		return "AKS Agent Pool"
	case AzureResourceTypeCognitiveServiceAccount:
		return "Cognitive Service"
	case AzureResourceTypeSearchService:
		return "Search service"
	case AzureResourceTypeVideoIndexer:
		return "Video Indexer"
	case AzureResourceTypeSpringApp:
		return "Azure Spring Apps"
	case AzurePrivateEndpoint:
		return "Private Endpoint"
	case AzureDevCenter:
		return "Dev Center"
	case AzureDevCenterProject:
		return "Dev Center Project"
	case AzureMachineLearningWorkspace:
		return "Machine Learning Workspace"
	case AzureMachineLearningEndpoint:
		return "Machine Learning Endpoint"
	case AzureMachineLearningConnection:
		return "Machine Learning Connection"
	}

	return ""
}

// IsTopLevelResourceType returns true if the resource type is a top-level resource type, otherwise false.
// A top-level resource type is of the format of: {ResourceProvider}/{TopLevelResourceType}, i.e.
// Microsoft.DocumentDB/databaseAccounts
func IsTopLevelResourceType(resourceType AzureResourceType) bool {
	// a deployment is not top level, but grouping level
	if resourceType == AzureResourceTypeDeployment {
		return false
	}

	resType := string(resourceType)
	firstIndex := strings.Index(resType, resourceLevelSeparator)

	if firstIndex == -1 ||
		firstIndex == 0 ||
		firstIndex == len(resType)-1 {
		return false
	}

	// Should not contain second separator
	return !strings.Contains(resType[firstIndex+1:], resourceLevelSeparator)
}

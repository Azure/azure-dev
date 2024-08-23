// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

type AzureResourceType string

const (
	AzureResourceTypeApim                      AzureResourceType = "Microsoft.ApiManagement/service"
	AzureResourceTypeAppConfig                 AzureResourceType = "Microsoft.AppConfiguration/configurationStores"
	AzureResourceTypeAppInsightComponent       AzureResourceType = "Microsoft.Insights/components"
	AzureResourceTypeCacheForRedis             AzureResourceType = "Microsoft.Cache/redis"
	AzureResourceTypeCDNProfile                AzureResourceType = "Microsoft.Cdn/profiles"
	AzureResourceTypeCosmosDb                  AzureResourceType = "Microsoft.DocumentDB/databaseAccounts"
	AzureResourceTypeContainerApp              AzureResourceType = "Microsoft.App/containerApps"
	AzureResourceTypeSpringApp                 AzureResourceType = "Microsoft.AppPlatform/Spring"
	AzureResourceTypeContainerAppEnvironment   AzureResourceType = "Microsoft.App/managedEnvironments"
	AzureResourceTypeDeployment                AzureResourceType = "Microsoft.Resources/deployments"
	AzureResourceTypeKeyVault                  AzureResourceType = "Microsoft.KeyVault/vaults"
	AzureResourceTypeManagedHSM                AzureResourceType = "Microsoft.KeyVault/managedHSMs"
	AzureResourceTypeLoadTest                  AzureResourceType = "Microsoft.LoadTestService/loadTests"
	AzureResourceTypeLogAnalyticsWorkspace     AzureResourceType = "Microsoft.OperationalInsights/workspaces"
	AzureResourceTypePortalDashboard           AzureResourceType = "Microsoft.Portal/dashboards"
	AzureResourceTypePostgreSqlServer          AzureResourceType = "Microsoft.DBforPostgreSQL/flexibleServers"
	AzureResourceTypeMySqlServer               AzureResourceType = "Microsoft.DBforMySQL/flexibleServers"
	AzureResourceTypeResourceGroup             AzureResourceType = "Microsoft.Resources/resourceGroups"
	AzureResourceTypeStorageAccount            AzureResourceType = "Microsoft.Storage/storageAccounts"
	AzureResourceTypeStaticWebSite             AzureResourceType = "Microsoft.Web/staticSites"
	AzureResourceTypeServiceBusNamespace       AzureResourceType = "Microsoft.ServiceBus/namespaces"
	AzureResourceTypeServicePlan               AzureResourceType = "Microsoft.Web/serverfarms"
	AzureResourceTypeSqlServer                 AzureResourceType = "Microsoft.Sql/servers"
	AzureResourceTypeVirtualNetwork            AzureResourceType = "Microsoft.Network/virtualNetworks"
	AzureResourceTypeWebSite                   AzureResourceType = "Microsoft.Web/sites"
	AzureResourceTypeContainerRegistry         AzureResourceType = "Microsoft.ContainerRegistry/registries"
	AzureResourceTypeManagedCluster            AzureResourceType = "Microsoft.ContainerService/managedClusters"
	AzureResourceTypeAgentPool                 AzureResourceType = "Microsoft.ContainerService/managedClusters/agentPools"
	AzureResourceTypeCognitiveServiceAccount   AzureResourceType = "Microsoft.CognitiveServices/accounts"
	AzureResourceTypeSearchService             AzureResourceType = "Microsoft.Search/searchServices"
	AzureResourceTypeVideoIndexer              AzureResourceType = "Microsoft.VideoIndexer/accounts"
	AzureResourceTypePrivateEndpoint           AzureResourceType = "Microsoft.Network/privateEndpoints"
	AzureResourceTypeDevCenter                 AzureResourceType = "Microsoft.DevCenter/devcenters"
	AzureResourceTypeDevCenterProject          AzureResourceType = "Microsoft.DevCenter/projects"
	AzureResourceTypeMachineLearningWorkspace  AzureResourceType = "Microsoft.MachineLearningServices/workspaces"
	AzureResourceTypeMachineLearningConnection AzureResourceType = "Microsoft.MachineLearningServices/workspaces/connections"

	//nolint:lll
	AzureResourceTypeMachineLearningEndpoint           AzureResourceType = "Microsoft.MachineLearningServices/workspaces/onlineEndpoints"
	AzureResourceTypeCognitiveServiceAccountDeployment AzureResourceType = "Microsoft.CognitiveServices/accounts/deployments"
)

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
		return "Azure AI Services"
	case AzureResourceTypeCognitiveServiceAccountDeployment:
		return "Azure AI Services Model Deployment"
	case AzureResourceTypeSearchService:
		return "Search service"
	case AzureResourceTypeVideoIndexer:
		return "Video Indexer"
	case AzureResourceTypeSpringApp:
		return "Azure Spring Apps"
	case AzureResourceTypePrivateEndpoint:
		return "Private Endpoint"
	case AzureResourceTypeDevCenter:
		return "Dev Center"
	case AzureResourceTypeDevCenterProject:
		return "Dev Center Project"
	case AzureResourceTypeMachineLearningWorkspace:
		return "Machine Learning Workspace"
	case AzureResourceTypeMachineLearningEndpoint:
		return "Machine Learning Endpoint"
	case AzureResourceTypeMachineLearningConnection:
		return "Machine Learning Connection"
	}

	return ""
}

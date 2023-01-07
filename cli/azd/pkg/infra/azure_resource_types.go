// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import "strings"

type AzureResourceType string

const (
	AzureResourceTypeResourceGroup           AzureResourceType = "Microsoft.Resources/resourceGroups"
	AzureResourceTypeDeployment              AzureResourceType = "Microsoft.Resources/deployments"
	AzureResourceTypeStorageAccount          AzureResourceType = "Microsoft.Storage/storageAccounts"
	AzureResourceTypeKeyVault                AzureResourceType = "Microsoft.KeyVault/vaults"
	AzureResourceTypeAppConfig               AzureResourceType = "Microsoft.AppConfiguration/configurationStores"
	AzureResourceTypePortalDashboard         AzureResourceType = "Microsoft.Portal/dashboards"
	AzureResourceTypeAppInsightComponent     AzureResourceType = "Microsoft.Insights/components"
	AzureResourceTypeLogAnalyticsWorkspace   AzureResourceType = "Microsoft.OperationalInsights/workspaces"
	AzureResourceTypeWebSite                 AzureResourceType = "Microsoft.Web/sites"
	AzureResourceTypeStaticWebSite           AzureResourceType = "Microsoft.Web/staticSites"
	AzureResourceTypeServicePlan             AzureResourceType = "Microsoft.Web/serverfarms"
	AzureResourceTypeSqlDatabase             AzureResourceType = "Microsoft.Sql/servers"
	AzureResourceTypeCosmosDb                AzureResourceType = "Microsoft.DocumentDB/databaseAccounts"
	AzureResourceTypeContainerApp            AzureResourceType = "Microsoft.App/containerApps"
	AzureResourceTypeContainerAppEnvironment AzureResourceType = "Microsoft.App/managedEnvironments"
	AzureResourceTypeApim                    AzureResourceType = "Microsoft.ApiManagement/service"
	AzureResourceTypeCacheForRedis           AzureResourceType = "Microsoft.Cache/redis"
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
		return "Key vault"
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
	case AzureResourceTypeServicePlan:
		return "App Service plan"
	case AzureResourceTypeCosmosDb:
		return "Azure Cosmos DB"
	case AzureResourceTypeApim:
		return "Azure API Management"
	case AzureResourceTypeCacheForRedis:
		return "Cache for Redis"
	case AzureResourceTypeSqlDatabase:
		return "Azure SQL DB"
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

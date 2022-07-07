package infra

type AzureResourceType string

const (
	AzureResourceTypeResourceGroup       AzureResourceType = "Microsoft.Resources/resourceGroups"
	AzureResourceTypeDeployment          AzureResourceType = "Microsoft.Resources/deployments"
	AzureResourceTypeStorageAccount      AzureResourceType = "Microsoft.Storage/storageAccounts"
	AzureResourceTypeKeyVault            AzureResourceType = "Microsoft.KeyVault/vaults"
	AzureResourceTypePortalDashboard     AzureResourceType = "Microsoft.Portal/dashboards"
	AzureResourceTypeAppInsightComponent AzureResourceType = "Microsoft.Insights/components"
	AzureResourceTypeWebSite             AzureResourceType = "Microsoft.Web/sites"
	AzureResourceTypeContainerApp        AzureResourceType = "Microsoft.App/containerApps"
)

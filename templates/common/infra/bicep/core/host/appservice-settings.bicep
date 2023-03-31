// Name of the app service
param name string
// App settings to apply
param appSettings object = {}

// Well-known app settings
// Sets APPLICATIONINSIGHTS_CONNECTION_STRING based on application insights resource. Not set if empty.
param applicationInsightsName string = ''
// Sets AZURE_KEY_VAULT_ENDPOINT based on keyVault resource. Not set if empty.
param keyVaultName string = ''
// Sets ENABLE_ORYX_BUILD. Defaults to true for linux systems, and always false on non-linux systems.
param enableOryxBuild bool = true
// Sets SCM_DO_BUILD_DURING_DEPLOYMENT
param scmDoBuildDuringDeployment bool = false

var orxyEnabled = contains(appService.kind, 'linux') ? enableOryxBuild : false

resource appService 'Microsoft.Web/sites@2022-03-01' existing = {
  name: name
}

resource settings 'Microsoft.Web/sites/config@2022-03-01' = {
  name: 'appsettings'
  parent: appService
  properties: union(appSettings,
    {
      SCM_DO_BUILD_DURING_DEPLOYMENT: string(scmDoBuildDuringDeployment)
      ENABLE_ORYX_BUILD: string(orxyEnabled)
    },
    !empty(applicationInsightsName) ? { APPLICATIONINSIGHTS_CONNECTION_STRING: applicationInsights.properties.ConnectionString } : {},
    !empty(keyVaultName) ? { AZURE_KEY_VAULT_ENDPOINT: keyVault.properties.vaultUri } : {})
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = if (!(empty(keyVaultName))) {
  name: keyVaultName
}

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = if (!empty(applicationInsightsName)) {
  name: applicationInsightsName
}

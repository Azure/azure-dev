param environmentName string
param location string = resourceGroup().location
param serviceName string
param kind string = 'app,linux'
param linuxFxVersion string = ''
param appCommandLine string = ''
param scmDoBuildDuringDeployment bool = false
param managedIdentity bool = useKeyVault
param appSettings object = {}
param useKeyVault bool = false

var tags = { 'azd-env-name': environmentName }
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var abbrs = loadJsonContent('../../../../common/infra/bicep/abbreviations.json')

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = if (useKeyVault) {
  name: '${abbrs.keyVaultVaults}${resourceToken}'
}

resource appServicePlan 'Microsoft.Web/serverfarms@2022-03-01' existing = {
  name: '${abbrs.webServerFarms}${resourceToken}'
}

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: '${abbrs.insightsComponents}${resourceToken}'
}

resource appservice 'Microsoft.Web/sites@2022-03-01' = {
  name: '${abbrs.webSitesAppService}${serviceName}-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': serviceName })
  kind: kind
  properties: {
    serverFarmId: appServicePlan.id
    siteConfig: {
      linuxFxVersion: linuxFxVersion
      alwaysOn: true
      ftpsState: 'FtpsOnly'
      appCommandLine: appCommandLine
    }
    httpsOnly: true
  }

  identity: managedIdentity ? { type: 'SystemAssigned' } : null

  resource appSettings 'config' = {
    name: 'appsettings'
    properties: union({
        SCM_DO_BUILD_DURING_DEPLOYMENT: string(scmDoBuildDuringDeployment)
        APPLICATIONINSIGHTS_CONNECTION_STRING: applicationInsights.properties.ConnectionString
      },
      useKeyVault ? { AZURE_KEY_VAULT_ENDPOINT: keyVault.properties.vaultUri } : {})
  }
}

module apiAppSettings 'appservice-config-union.bicep' = if (!empty(appSettings)) {
  name: 'api-app-settings-${serviceName}'
  params: {
    resourceName: appservice.name
    configName: 'appsettings'
    currentConfigProperties: appservice::appSettings.list().properties
    additionalConfigProperties: appSettings
  }
}

module apiSiteConfigLogs 'appservice-config-logs.bicep' = {
  name: 'appservice-config-logs-${serviceName}'
  params: {
    resourceName: appservice.name
  }
}

module keyVaultAccess 'keyvault-access.bicep' = if (useKeyVault) {
  name: 'keyvault-access-api'
  params: {
    principalId: appservice.identity.principalId
    environmentName: environmentName
    location: location
  }
}

output NAME string = appservice.name
output URI string = 'https://${appservice.properties.defaultHostName}'
output IDENTITY_PRINCIPAL_ID string = managedIdentity ? appservice.identity.principalId : ''

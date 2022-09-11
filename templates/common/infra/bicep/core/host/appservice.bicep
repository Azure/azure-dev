param environmentName string
param location string = resourceGroup().location
param serviceName string
param kind string = 'app,linux'
param linuxFxVersion string = ''
param appCommandLine string = ''
param scmDoBuildDuringDeployment bool = false
param appSettings object = {}
param keyVaultName string = ''
param useKeyVault bool = !(empty(keyVaultName))
param managedIdentity bool = useKeyVault
param applicationInsightsName string
param appServicePlanId string
param numberOfWorkers int = -1
param alwaysOn bool = true
param minimumElasticInstanceCount int = -1
param use32BitWorkerProcess bool = false
param clientAffinityEnabled bool = false
param allowedOrigins array = []
param functionAppScaleLimit int = -1

var tags = { 'azd-env-name': environmentName }
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var abbrs = loadJsonContent('../../abbreviations.json')

var prefix = contains(kind, 'function') ? abbrs.webSitesFunctions : abbrs.webSitesAppService

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = if (useKeyVault) {
  name: keyVaultName
}

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: applicationInsightsName
}

resource appservice 'Microsoft.Web/sites@2022-03-01' = {
  name: '${prefix}${serviceName}-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': serviceName })
  kind: kind
  properties: {
    serverFarmId: appServicePlanId
    siteConfig: {
      linuxFxVersion: linuxFxVersion
      alwaysOn: alwaysOn
      ftpsState: 'FtpsOnly'
      appCommandLine: appCommandLine
      numberOfWorkers: numberOfWorkers != -1 ? numberOfWorkers : null
      minimumElasticInstanceCount: minimumElasticInstanceCount != -1 ? minimumElasticInstanceCount : null
      use32BitWorkerProcess: use32BitWorkerProcess
      functionAppScaleLimit: functionAppScaleLimit != -1 ? functionAppScaleLimit : null
      cors: {
        allowedOrigins: union([ 'https://portal.azure.com', 'https://ms.portal.azure.com' ], allowedOrigins)
      }
    }
    clientAffinityEnabled: clientAffinityEnabled
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

module appSettingsUnion 'appservice-config-union.bicep' = if (!empty(appSettings)) {
  name: 'app-settings-union-${serviceName}'
  params: {
    appServiceName: appservice.name
    configName: 'appsettings'
    currentConfigProperties: appservice::appSettings.list().properties
    additionalConfigProperties: appSettings
  }
}

module siteConfigLogs 'appservice-config-logs.bicep' = {
  name: 'appservice-config-logs-${serviceName}'
  params: {
    appServiceName: appservice.name
  }
}

module keyVaultAccess '../security/keyvault-access.bicep' = if (useKeyVault) {
  name: 'appservice-keyvault-access-${serviceName}'
  params: {
    principalId: appservice.identity.principalId
    environmentName: environmentName
    location: location
  }
}

output NAME string = appservice.name
output URI string = 'https://${appservice.properties.defaultHostName}'
output IDENTITY_PRINCIPAL_ID string = managedIdentity ? appservice.identity.principalId : ''

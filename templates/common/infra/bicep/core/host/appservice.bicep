param environmentName string
param location string = resourceGroup().location

param allowedOrigins array = []
param alwaysOn bool = true
param appCommandLine string = ''
param applicationInsightsName string
param appServicePlanId string
param appSettings object = {}
param clientAffinityEnabled bool = false
param functionAppScaleLimit int = -1
param keyVaultName string = ''
param kind string = 'app,linux'
param linuxFxVersion string = ''
param managedIdentity bool = !(empty(keyVaultName))
param minimumElasticInstanceCount int = -1
param numberOfWorkers int = -1
param scmDoBuildDuringDeployment bool = false
param serviceName string
param use32BitWorkerProcess bool = false

var abbrs = loadJsonContent('../../abbreviations.json')
var tags = { 'azd-env-name': environmentName }
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

var prefix = contains(kind, 'function') ? abbrs.webSitesFunctions : abbrs.webSitesAppService

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = if (!(empty(keyVaultName))) {
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
      !(empty(keyVaultName)) ? { AZURE_KEY_VAULT_ENDPOINT: keyVault.properties.vaultUri } : {})
  }
}

module appSettingsUnion 'appservice-config-union.bicep' = if (!empty(appSettings)) {
  name: '${serviceName}-app-settings-union'
  params: {
    appServiceName: appservice.name
    configName: 'appsettings'
    currentConfigProperties: appservice::appSettings.list().properties
    additionalConfigProperties: appSettings
  }
}

module siteConfigLogs 'appservice-config-logs.bicep' = {
  name: '${serviceName}-appservice-config-logs'
  params: {
    appServiceName: appservice.name
  }
}

module keyVaultAccess '../security/keyvault-access.bicep' = if (!(empty(keyVaultName))) {
  name: '${serviceName}-appservice-keyvault-access'
  params: {
    principalId: appservice.identity.principalId
    environmentName: environmentName
    location: location
  }
}

output identityPrincipalId string = managedIdentity ? appservice.identity.principalId : ''
output name string = appservice.name
output uri string = 'https://${appservice.properties.defaultHostName}'

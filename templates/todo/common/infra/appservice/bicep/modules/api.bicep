param environmentName string
param location string = resourceGroup().location
param cosmosDatabaseName string = 'Todo'
param cosmosConnectionStringKey string = 'AZURE-COSMOS-CONNECTION-STRING'
param cosmosEndpoint string = ''
param scmDoBuildDuringDeployment bool = true

param linuxFxVersion string
param appCommandLine string = ''

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var abbrs = loadJsonContent('../../../../../../common/infra/bicep/abbreviations.json')

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: '${abbrs.insightsComponents}${resourceToken}'
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: '${abbrs.keyVaultVaults}${resourceToken}'
}

resource appServicePlan 'Microsoft.Web/serverfarms@2022-03-01' existing = {
  name: '${abbrs.webServerFarms}${resourceToken}'
}

resource api 'Microsoft.Web/sites@2022-03-01' = {
  name: '${abbrs.webSitesAppService}api-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': 'api' })
  kind: 'app,linux'
  properties: {
    serverFarmId: appServicePlan.id
    siteConfig: {
      alwaysOn: true
      linuxFxVersion: linuxFxVersion
      ftpsState: 'FtpsOnly'
      appCommandLine: appCommandLine
    }
    httpsOnly: true
  }

  identity: { type: 'SystemAssigned' }

  resource appSettings 'config' = {
    name: 'appsettings'
    properties: {
      AZURE_COSMOS_ENDPOINT: cosmosEndpoint
      AZURE_COSMOS_CONNECTION_STRING_KEY: cosmosConnectionStringKey
      AZURE_COSMOS_DATABASE_NAME: cosmosDatabaseName
      SCM_DO_BUILD_DURING_DEPLOYMENT: string(scmDoBuildDuringDeployment)
      AZURE_KEY_VAULT_ENDPOINT: keyVault.properties.vaultUri
      APPLICATIONINSIGHTS_CONNECTION_STRING: applicationInsights.properties.ConnectionString
    }
  }

  resource logs 'config' = {
    name: 'logs'
    properties: {
      applicationLogs: { fileSystem: { level: 'Verbose' } }
      detailedErrorMessages: { enabled: true }
      failedRequestsTracing: { enabled: true }
      httpLogs: { fileSystem: { enabled: true, retentionInDays: 1, retentionInMb: 35 } }
    }
  }
}

resource keyVaultAccessPolicies 'Microsoft.KeyVault/vaults/accessPolicies@2022-07-01' = {
  parent: keyVault
  name: 'add'
  properties: {
    accessPolicies: [ {
        objectId: api.identity.principalId
        tenantId: subscription().tenantId
        permissions: { secrets: [ 'get', 'list' ] }
      } ]
  }
}




output API_URI string = 'https://${api.properties.defaultHostName}'
output API_PRINCIPAL_ID string = api.identity.principalId

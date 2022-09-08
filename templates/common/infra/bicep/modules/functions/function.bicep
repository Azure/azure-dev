param environmentName string
param location string = resourceGroup().location
param linuxFxVersion string
param functionsWorkerRuntime string
param functionsExtensionVersion string = '~4'
param allowedOrigins array
param cosmosDatabaseName string
param cosmosConnectionStringKey string = 'AZURE-COSMOS-CONNECTION-STRING'

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var abbrs = loadJsonContent('../../../../../common/infra/bicep/abbreviations.json')

resource appServicePlan 'Microsoft.Web/serverfarms@2022-03-01' existing = {
  name: '${abbrs.webServerFarms}${resourceToken}'
}

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: '${abbrs.insightsComponents}${resourceToken}'
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: '${abbrs.keyVaultVaults}${resourceToken}'
}

resource storage 'Microsoft.Storage/storageAccounts@2021-09-01' existing = {
  name: '${abbrs.storageStorageAccounts}${resourceToken}'
}

resource api 'Microsoft.Web/sites@2022-03-01' = {
  name: '${abbrs.webSitesFunctions}api-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': 'api' })
  kind: 'functionapp,linux'
  properties: {
    serverFarmId: appServicePlan.id
    siteConfig: {
      numberOfWorkers: 1
      linuxFxVersion: linuxFxVersion
      alwaysOn: false
      functionAppScaleLimit: 200
      minimumElasticInstanceCount: 0
      ftpsState: 'FtpsOnly'
      use32BitWorkerProcess: false
      cors: {
        allowedOrigins: union([ 'https://portal.azure.com' ], allowedOrigins)
      }
    }
    clientAffinityEnabled: false
    httpsOnly: true
  }

  identity: { type: 'SystemAssigned' }

  resource appSettings 'config' = {
    name: 'appsettings'
    properties: {
      APPLICATIONINSIGHTS_CONNECTION_STRING: applicationInsights.properties.ConnectionString
      AzureWebJobsStorage: 'DefaultEndpointsProtocol=https;AccountName=${storage.name};AccountKey=${storage.listKeys().keys[0].value};EndpointSuffix=${environment().suffixes.storage}'
      FUNCTIONS_EXTENSION_VERSION: functionsExtensionVersion
      FUNCTIONS_WORKER_RUNTIME: functionsWorkerRuntime
      SCM_DO_BUILD_DURING_DEPLOYMENT: 'true'
      AZURE_COSMOS_CONNECTION_STRING_KEY: cosmosConnectionStringKey
      AZURE_COSMOS_DATABASE_NAME: cosmosDatabaseName
      AZURE_KEY_VAULT_ENDPOINT: keyVault.properties.vaultUri
    }
  }

  resource logs 'config' = {
    name: 'logs'
    properties: { applicationLogs: { fileSystem: { level: 'Verbose' } }
      detailedErrorMessages: { enabled: true }
      failedRequestsTracing: { enabled: true }
      httpLogs: { fileSystem: { enabled: true, retentionInDays: 1, retentionInMb: 35 }
      }
    }
  }
}

output API_URI string = 'https://${api.properties.defaultHostName}'

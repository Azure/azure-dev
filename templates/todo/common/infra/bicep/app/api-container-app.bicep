param environmentName string
param location string = resourceGroup().location

param applicationInsightsName string = ''
param containerAppsEnvironmentName string = ''
param containerRegistryName string = ''
param imageName string = ''
param keyVaultName string = ''
param serviceName string = 'api'

var abbrs = loadJsonContent('../../../../../common/infra/bicep/abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

module api '../../../../../common/infra/bicep/core/host/container-app.bicep' = {
  name: '${serviceName}-container-app-module'
  params: {
    environmentName: environmentName
    location: location
    containerAppsEnvironmentName: containerAppsEnvironmentName
    containerRegistryName: containerRegistryName
    env: [
      {
        name: 'AZURE_KEY_VAULT_ENDPOINT'
        value: keyVault.properties.vaultUri
      }
      {
        name: 'APPLICATIONINSIGHTS_CONNECTION_STRING'
        value: applicationInsights.properties.ConnectionString
      }
    ]
    imageName: !empty(imageName) ? imageName : 'nginx:latest'
    keyVaultName: keyVault.name
    serviceName: serviceName
    targetPort: 3100
  }
}

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: !empty(applicationInsightsName) ? applicationInsightsName : '${abbrs.insightsComponents}${resourceToken}'
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: !empty(keyVaultName) ? keyVaultName : '${abbrs.keyVaultVaults}${resourceToken}'
}

output API_IDENTITY_PRINCIPAL_ID string = api.outputs.identityPrincipalId
output API_NAME string = api.outputs.name
output API_URI string = api.outputs.uri

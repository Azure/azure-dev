param environmentName string
param location string = resourceGroup().location
param serviceName string = 'api'
param imageName string

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var abbrs = loadJsonContent('../../../../../common/infra/bicep/abbreviations.json')

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: '${abbrs.insightsComponents}${resourceToken}'
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: '${abbrs.keyVaultVaults}${resourceToken}'
}

module api '../../../../../common/infra/bicep/core/host/container-app.bicep' = {
  name: 'api-containerapp-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    serviceName: serviceName
    useKeyVault: true
    targetPort: 3100
    imageName: imageName
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
  }
}

output API_NAME string = api.outputs.NAME
output API_URI string = api.outputs.URI
output API_IDENTITY_PRINCIPAL_ID string = api.outputs.IDENTITY_PRINCIPAL_ID

param environmentName string
param location string = resourceGroup().location
param keyVaultName string
param cosmosConnectionStringKey string = 'AZURE-COSMOS-CONNECTION-STRING'
param kind string = 'MongoDB'

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var abbrs = loadJsonContent('../../abbreviations.json')

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: keyVaultName
}

resource cosmos 'Microsoft.DocumentDB/databaseAccounts@2022-05-15' = {
  name: '${abbrs.documentDBDatabaseAccounts}${resourceToken}'
  kind: kind
  location: location
  tags: tags
  properties: {
    consistencyPolicy: { defaultConsistencyLevel: 'Session' }
    locations: [
      {
        locationName: location
        failoverPriority: 0
        isZoneRedundant: false
      }
    ]
    databaseAccountOfferType: 'Standard'
    enableAutomaticFailover: false
    enableMultipleWriteLocations: false
    apiProperties: (kind == 'MongoDB') ? { serverVersion: '4.0' } : {}
    capabilities: [ { name: 'EnableServerless' } ]
  }

}

resource cosmosConnectionString 'Microsoft.KeyVault/vaults/secrets@2022-07-01' = {
  parent: keyVault
  name: cosmosConnectionStringKey
  properties: {
    value: cosmos.listConnectionStrings().connectionStrings[0].connectionString
  }
}

output AZURE_COSMOS_RESOURCE_ID string = cosmos.id
output AZURE_COSMOS_ENDPOINT string = cosmos.properties.documentEndpoint
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmosConnectionStringKey

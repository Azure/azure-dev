param environmentName string
param location string = resourceGroup().location
param keyVaultName string
param cosmosDatabaseName string
param cosmosConnectionStringKey string = 'AZURE-COSMOS-CONNECTION-STRING'
param collections array = []

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var abbrs = loadJsonContent('../../abbreviations.json')

module cosmos 'cosmos.bicep' = {
  name: 'cosmos-account-resources'
  params: {
    environmentName: environmentName
    location: location
    keyVaultName: keyVaultName
  }
}

resource database 'Microsoft.DocumentDB/databaseAccounts/mongodbDatabases@2022-05-15' = {
  name: '${abbrs.documentDBDatabaseAccounts}${resourceToken}/${cosmosDatabaseName}'
  properties: {
    resource: { id: cosmosDatabaseName }
  }

  resource list 'collections' = [for collection in collections: {
    name: collection.name
    properties: {
      resource: {
        id: collection.id
        shardKey: { _id: collection.shardKey }
        indexes: [ { key: { keys: [ collection.indexKey ] } } ]
      }
    }
  }]

  dependsOn: [
    cosmos
  ]
}

output AZURE_COSMOS_ENDPOINT string = cosmos.outputs.AZURE_COSMOS_ENDPOINT
output AZURE_COSMOS_DATABASE_NAME string = cosmosDatabaseName
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmosConnectionStringKey

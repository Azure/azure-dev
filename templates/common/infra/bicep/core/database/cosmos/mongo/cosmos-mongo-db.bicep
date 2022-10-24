param accountName string
param databaseName string
param location string = resourceGroup().location
param tags object = {}

param collections array = []
param connectionStringKey string = 'AZURE-COSMOS-CONNECTION-STRING'
param keyVaultName string

module cosmos 'cosmos-mongo-account.bicep' = {
  name: 'cosmos-mongo-account'
  params: {
    name: accountName
    location: location
    keyVaultName: keyVaultName
    tags: tags
    connectionStringKey: connectionStringKey
  }
}

resource database 'Microsoft.DocumentDB/databaseAccounts/mongodbDatabases@2022-05-15' = {
  name: '${accountName}/${databaseName}'
  tags: tags
  properties: {
    resource: { id: databaseName }
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

output cosmosConnectionStringKey string = connectionStringKey
output cosmosDatabaseName string = databaseName
output cosmosEndpoint string = cosmos.outputs.cosmosEndpoint

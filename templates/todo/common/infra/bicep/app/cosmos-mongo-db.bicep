param accountName string
param location string = resourceGroup().location
param tags object = {}

param collections array = [
  {
    name: 'TodoList'
    id: 'TodoList'
    shardKey: 'Hash'
    indexKey: '_id'
  }
  {
    name: 'TodoItem'
    id: 'TodoItem'
    shardKey: 'Hash'
    indexKey: '_id'
  }
]
param databaseName string = 'Todo'
param keyVaultName string

module cosmos '../../../../../common/infra/bicep/core/database/cosmos/mongo/cosmos-mongo-db.bicep' = {
  name: 'cosmos-mongo'
  params: {
    accountName: accountName
    databaseName: databaseName
    location: location
    collections: collections
    keyVaultName: keyVaultName
    tags: tags
  }
}

output cosmosConnectionStringKey string = cosmos.outputs.cosmosConnectionStringKey
output cosmosDatabaseName string = databaseName
output cosmosEndpoint string = cosmos.outputs.cosmosEndpoint

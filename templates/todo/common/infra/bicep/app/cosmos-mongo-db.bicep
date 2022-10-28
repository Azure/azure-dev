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
    databaseName: !empty(databaseName) ? databaseName : 'Todo'
    location: location
    collections: collections
    keyVaultName: keyVaultName
    tags: tags
  }
}

output connectionStringKey string = cosmos.outputs.connectionStringKey
output databaseName string = databaseName
output endpoint string = cosmos.outputs.endpoint

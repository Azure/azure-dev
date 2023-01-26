param accountName string
param location string = resourceGroup().location
param tags object = {}

param collections array = [
  // ToDo!: Define Cosmos DB collections
  // {
  //   name: 'TodoList'
  //   id: 'TodoList'
  //   shardKey: 'Hash'
  //   indexKey: '_id'
  // }
  // {
  //   name: 'TodoItem'
  //   id: 'TodoItem'
  //   shardKey: 'Hash'
  //   indexKey: '_id'
  // }
]

param databaseName string
param keyVaultName string

module cosmos '../core/database/cosmos/mongo/cosmos-mongo-db.bicep' = {
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

output connectionStringKey string = cosmos.outputs.connectionStringKey
output databaseName string = cosmos.outputs.databaseName
output endpoint string = cosmos.outputs.endpoint

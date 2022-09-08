param environmentName string
param location string = resourceGroup().location
param keyVaultName string = ''
param cosmosDatabaseName string = 'Todo'
param cosmosConnectionStringKey string = 'AZURE-COSMOS-CONNECTION-STRING'
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

module cosmos '../../../../../common/infra/bicep/core/database/cosmos-mongo-db.bicep' = {
  name: 'todo-cosmos-mongo-resources'
  params: {
    environmentName: environmentName
    location: location
    cosmosDatabaseName: cosmosDatabaseName
    collections: collections
    keyVaultName: keyVaultName
  }
}

output AZURE_COSMOS_ENDPOINT string = cosmos.outputs.AZURE_COSMOS_ENDPOINT
output AZURE_COSMOS_DATABASE_NAME string = cosmosDatabaseName
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmosConnectionStringKey

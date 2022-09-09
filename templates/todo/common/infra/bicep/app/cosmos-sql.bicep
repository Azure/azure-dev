param environmentName string
param location string = resourceGroup().location
param keyVaultName string
param principalIds array = []
param cosmosDatabaseName string = 'Todo'
param containers array = [
  {
    name: 'TodoList'
    id: 'TodoList'
    partitionKey: '/id'
  }
  {
    name: 'TodoItem'
    id: 'TodoItem'
    partitionKey: '/id'
  }
]

module cosmos '../../../../../common/infra/bicep/core/database/cosmos-sql-db.bicep' = {
  name: 'todo-cosmos-sql-resources'
  params: {
    environmentName: environmentName
    location: location
    cosmosDatabaseName: cosmosDatabaseName
    containers: containers
    keyVaultName: keyVaultName
    principalIds: principalIds
  }
}

output AZURE_COSMOS_ENDPOINT string = cosmos.outputs.AZURE_COSMOS_ENDPOINT
output AZURE_COSMOS_DATABASE_NAME string = cosmosDatabaseName
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmos.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY

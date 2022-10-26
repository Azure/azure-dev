param accountName string
param location string = resourceGroup().location
param tags object = {}

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

param databaseName string = 'Todo'
param keyVaultName string
param principalIds array = []

module cosmos '../../../../../common/infra/bicep/core/database/cosmos/sql/cosmos-sql-db.bicep' = {
  name: 'cosmos-sql'
  params: {
    accountName: accountName
    location: location
    tags: tags
    containers: containers
    databaseName: databaseName
    keyVaultName: keyVaultName
    principalIds: principalIds
  }
}

output connectionStringKey string = cosmos.outputs.connectionStringKey
output accountName string = cosmos.outputs.accountName
output databaseName string = databaseName
output endpoint string = cosmos.outputs.endpoint
output roleDefinitionId string = cosmos.outputs.roleDefinitionId

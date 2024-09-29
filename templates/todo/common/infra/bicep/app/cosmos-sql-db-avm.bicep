param accountName string
param location string = resourceGroup().location
param tags object = {}
param connectionStringKey string = 'AZURE-COSMOS-CONNECTION-STRING'
param databaseName string = ''
param keyVaultResourceId string
param principalId string = ''

var defaultDatabaseName = 'Todo'
var actualDatabaseName = !empty(databaseName) ? databaseName : defaultDatabaseName

module cosmos 'br/public:avm/res/document-db/database-account:0.6.0' = {
  name: 'cosmos'
  params: {
    name: accountName
    location: location
    tags: tags
    locations: [
      {
        failoverPriority: 0
        locationName: location
        isZoneRedundant: false
      }
    ]
    secretsExportConfiguration:{
      keyVaultResourceId: keyVaultResourceId
      primaryWriteConnectionStringSecretName: connectionStringKey
    }
    capabilitiesToAdd: [ 'EnableServerless' ] 
    automaticFailover: false
    sqlDatabases: [
      {
        name: actualDatabaseName
        containers: [
          {
            name: 'TodoList'
            paths: [ 'id' ]
          }
          {
            name: 'TodoItem'
            paths: [ 'id' ]
          }
        ]
      }
    ] 
    sqlRoleAssignmentsPrincipalIds: [ principalId ]
    sqlRoleDefinitions: [
      {
        name: 'writer'
      }
    ]
  }
}

output accountName string = cosmos.outputs.name
output connectionStringKey string = connectionStringKey
output databaseName string = actualDatabaseName
output endpoint string = cosmos.outputs.endpoint

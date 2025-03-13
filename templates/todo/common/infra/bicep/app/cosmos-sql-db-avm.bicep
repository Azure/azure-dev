param accountName string
param location string = resourceGroup().location
param tags object = {}
param connectionStringKey string = 'AZURE-COSMOS-CONNECTION-STRING'
param databaseName string = ''
param keyVaultResourceId string
param principalId string = ''

@allowed([
  'Periodic'
  'Continuous'
])
@description('Optional. Default to Continuous. Describes the mode of backups. Periodic backup must be used if multiple write locations are used.')
param backupPolicyType string = 'Continuous'

var defaultDatabaseName = 'Todo'
var actualDatabaseName = !empty(databaseName) ? databaseName : defaultDatabaseName

module cosmos 'br/public:avm/res/document-db/database-account:0.6.0' = {
  name: 'cosmos-sql'
  params: {
    name: accountName
    location: location
    tags: tags
    backupPolicyType: backupPolicyType
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

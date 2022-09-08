param environmentName string
param location string = resourceGroup().location
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var abbrs = loadJsonContent('../../abbreviations.json')

resource cosmos 'Microsoft.DocumentDB/databaseAccounts@2022-05-15' existing = {
  name: '${abbrs.documentDBDatabaseAccounts}${resourceToken}'
}

resource roleDefinition 'Microsoft.DocumentDB/databaseAccounts/sqlRoleDefinitions@2022-05-15' = {
  parent: cosmos
  name: guid(cosmos.id, resourceToken, 'sql-role')
  properties: {
    assignableScopes: [
      cosmos.id
    ]
    permissions: [
      {
        dataActions: [
          'Microsoft.DocumentDB/databaseAccounts/readMetadata'
          'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/items/*'
          'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/*'
        ]
        notDataActions: []
      }
    ]
    roleName: 'Reader Writer'
    type: 'CustomRole'
  }
}

output AZURE_COSMOS_SQL_ROLE_DEFINITION_ID string = roleDefinition.id

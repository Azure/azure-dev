param environmentName string
param location string = resourceGroup().location

var abbrs = loadJsonContent('../../abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

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

resource cosmos 'Microsoft.DocumentDB/databaseAccounts@2022-05-15' existing = {
  name: '${abbrs.documentDBDatabaseAccounts}${resourceToken}'
}

output cosmosSqlRoleDefinitionId string = roleDefinition.id

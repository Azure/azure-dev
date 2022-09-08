param environmentName string
param location string = resourceGroup().location
param cosmosRoleDefinitionId string
param principalId string = ''
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var abbrs = loadJsonContent('../../abbreviations.json')

resource cosmos 'Microsoft.DocumentDB/databaseAccounts@2022-05-15' existing = {
  name: '${abbrs.documentDBDatabaseAccounts}${resourceToken}'
}

resource role 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2022-05-15' = {
  parent: cosmos
  name: guid(cosmosRoleDefinitionId, principalId, cosmos.id)
  properties: {
    principalId: principalId
    roleDefinitionId: cosmosRoleDefinitionId
    scope: cosmos.id
  }
}

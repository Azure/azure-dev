param environmentName string
param location string = resourceGroup().location
param cosmosDatabaseName string = 'Todo'
param principalIds array = []

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var abbrs = loadJsonContent('../../../../common/infra/bicep/abbreviations.json')

module cosmosAccountResources 'cosmos.bicep' = {
  name: 'cosmos-account-resources'
  params: {
    environmentName: environmentName
    location: location
    kind: 'GlobalDocumentDB'
  }
}

resource database 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases@2022-05-15' = {
  name: '${abbrs.documentDBDatabaseAccounts}${resourceToken}/${cosmosDatabaseName}'
  properties: {
    resource: { id: 'Todo' }
  }

  resource list 'containers' = {
    name: 'TodoList'
    properties: {
      resource: {
        id: 'TodoList'
        partitionKey: { paths: [ '/id' ] }
      }
      options: {}
    }
  }

  resource item 'containers' = {
    name: 'TodoItem'
    properties: {
      resource: {
        id: 'TodoItem'
        partitionKey: { paths: [ '/id' ] }
      }
      options: {}
    }
  }

  dependsOn: [
    cosmosAccountResources
  ]
}

module roleDefintionResources 'cosmos-sql-role-def.bicep' = {
  name: 'cosmos-sql-role-def-resources'
  params: {
    environmentName: environmentName
    location: location
  }
  dependsOn: [
    cosmosAccountResources
  ]
}

module userRoleResources 'cosmos-sql-role-assign.bicep' = [for principalId in principalIds: if (!empty(principalId)) {
  name: 'cosmos-sql-user-role-resources-${uniqueString(principalId)}'
  params: {
    environmentName: environmentName
    location: location
    cosmosRoleDefinitionId: roleDefintionResources.outputs.AZURE_COSMOS_SQL_ROLE_DEFINITION_ID
    principalId: principalId
  }
  dependsOn: [
    cosmosAccountResources
  ]
}]

output AZURE_COSMOS_RESOURCE_ID string = cosmosAccountResources.outputs.AZURE_COSMOS_RESOURCE_ID
output AZURE_COSMOS_SQL_ROLE_DEFINITION_ID string = roleDefintionResources.outputs.AZURE_COSMOS_SQL_ROLE_DEFINITION_ID
output AZURE_COSMOS_ENDPOINT string = cosmosAccountResources.outputs.AZURE_COSMOS_ENDPOINT
output AZURE_COSMOS_DATABASE_NAME string = database.name
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmosAccountResources.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY

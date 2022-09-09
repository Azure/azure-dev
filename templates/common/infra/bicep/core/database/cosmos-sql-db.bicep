param environmentName string
param location string = resourceGroup().location
param keyVaultName string
param cosmosDatabaseName string
param principalIds array = []
param containers array = []

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var abbrs = loadJsonContent('../../abbreviations.json')

module cosmos 'cosmos-sql.bicep' = {
  name: 'cosmos-sql-account-resources'
  params: {
    environmentName: environmentName
    location: location
    cosmosDatabaseName: cosmosDatabaseName
    keyVaultName: keyVaultName
  }
}

resource database 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases@2022-05-15' = {
  name: '${abbrs.documentDBDatabaseAccounts}${resourceToken}/${cosmosDatabaseName}'
  properties: {
    resource: { id: cosmosDatabaseName }
  }

  resource list 'containers' = [for container in containers: {
    name: container.name
    properties: {
      resource: {
        id: container.id
        partitionKey: { paths: [ container.partitionKey ] }
      }
      options: {}
    }
  }]

  dependsOn: [
    cosmos
  ]
}

module roleDefintion 'cosmos-sql-role-def.bicep' = {
  name: 'cosmos-sql-role-def-resources'
  params: {
    environmentName: environmentName
    location: location
  }
  dependsOn: [
    database
    cosmos
  ]
}

// We need batchSize(1) here because sql role assignments have to be done sequentially
@batchSize(1)
module userRole 'cosmos-sql-role-assign.bicep' = [for principalId in principalIds: if (!empty(principalId)) {
  name: 'cosmos-sql-user-role-resources-${uniqueString(principalId)}'
  params: {
    environmentName: environmentName
    location: location
    cosmosRoleDefinitionId: roleDefintion.outputs.AZURE_COSMOS_SQL_ROLE_DEFINITION_ID
    principalId: principalId
  }
  dependsOn: [
    cosmos
    database
    roleDefintion
  ]
}]

output AZURE_COSMOS_RESOURCE_ID string = cosmos.outputs.AZURE_COSMOS_RESOURCE_ID
output AZURE_COSMOS_SQL_ROLE_DEFINITION_ID string = roleDefintion.outputs.AZURE_COSMOS_SQL_ROLE_DEFINITION_ID
output AZURE_COSMOS_ENDPOINT string = cosmos.outputs.AZURE_COSMOS_ENDPOINT
output AZURE_COSMOS_DATABASE_NAME string = cosmosDatabaseName
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmos.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY

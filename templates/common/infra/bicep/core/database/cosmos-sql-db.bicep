param environmentName string
param location string = resourceGroup().location

param containers array = []
param cosmosDatabaseName string
param keyVaultName string
param principalIds array = []

var abbrs = loadJsonContent('../../abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

module cosmos 'cosmos-sql-account.bicep' = {
  name: 'cosmos-sql-account'
  params: {
    environmentName: environmentName
    location: location
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
  name: 'cosmos-sql-role-definition'
  params: {
    environmentName: environmentName
    location: location
  }
  dependsOn: [
    cosmos
    database
  ]
}

// We need batchSize(1) here because sql role assignments have to be done sequentially
@batchSize(1)
module userRole 'cosmos-sql-role-assign.bicep' = [for principalId in principalIds: if (!empty(principalId)) {
  name: 'cosmos-sql-user-role-${uniqueString(principalId)}'
  params: {
    environmentName: environmentName
    location: location
    cosmosRoleDefinitionId: roleDefintion.outputs.cosmosSqlRoleDefinitionId
    principalId: principalId
  }
  dependsOn: [
    cosmos
    database
  ]
}]

output cosmosConnectionStringKey string = cosmos.outputs.cosmosConnectionStringKey
output cosmosDatabaseName string = cosmosDatabaseName
output cosmosEndpoint string = cosmos.outputs.cosmosEndpoint
output cosmosResourceId string = cosmos.outputs.cosmosResourceId
output cosmosSqlRoleDefinitionId string = roleDefintion.outputs.cosmosSqlRoleDefinitionId

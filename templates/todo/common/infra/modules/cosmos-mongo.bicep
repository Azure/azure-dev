param environmentName string
param location string = resourceGroup().location
param cosmosDatabaseName string = 'Todo'
param cosmosConnectionStringKey string = 'AZURE-COSMOS-CONNECTION-STRING'

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var abbrs = loadJsonContent('../../../../common/infra/bicep/abbreviations.json')

module cosmosAccountResources 'cosmos.bicep' = {
  name: 'cosmos-account-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

resource database 'Microsoft.DocumentDB/databaseAccounts/mongodbDatabases@2022-05-15' = {
  name: '${abbrs.documentDBDatabaseAccounts}${resourceToken}/${cosmosDatabaseName}'
  properties: {
    resource: { id: 'Todo' }
  }

  resource list 'collections' = {
    name: 'TodoList'
    properties: {
      resource: {
        id: 'TodoList'
        shardKey: { _id: 'Hash' }
        indexes: [ { key: { keys: [ '_id' ] } } ]
      }
    }
  }

  resource item 'collections' = {
    name: 'TodoItem'
    properties: {
      resource: {
        id: 'TodoItem'
        shardKey: { _id: 'Hash' }
        indexes: [ { key: { keys: [ '_id' ] } } ]
      }
    }
  }

  dependsOn: [
    cosmosAccountResources
  ]
}

output AZURE_COSMOS_ENDPOINT string = cosmosAccountResources.outputs.AZURE_COSMOS_ENDPOINT
output AZURE_COSMOS_DATABASE_NAME string = database.name
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmosConnectionStringKey

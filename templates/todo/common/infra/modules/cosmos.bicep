param location string
param resourceToken string
param tags object
param connectionStringKey string = 'AZURE-COSMOS-CONNECTION-STRING'

var abbrs = loadJsonContent('../../../../common/infra/bicep/abbreviations.json')

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: '${abbrs.keyVaultVaults}${resourceToken}'
}

resource cosmos 'Microsoft.DocumentDB/databaseAccounts@2022-05-15' = {
  name: '${abbrs.documentDBDatabaseAccounts}${resourceToken}'
  kind: 'MongoDB'
  location: location
  tags: tags
  properties: {
    consistencyPolicy: { defaultConsistencyLevel: 'Session' }
    locations: [
      {
        locationName: location
        failoverPriority: 0
        isZoneRedundant: false
      }
    ]
    databaseAccountOfferType: 'Standard'
    enableAutomaticFailover: false
    enableMultipleWriteLocations: false
    apiProperties: { serverVersion: '4.0' }
    capabilities: [ { name: 'EnableServerless' } ]
  }

  resource database 'mongodbDatabases' = {
    name: 'Todo'
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
  }
}

resource cosmosConnectionString 'Microsoft.KeyVault/vaults/secrets@2022-07-01' = {
  parent: keyVault
  name: connectionStringKey
  properties: {
    value: cosmos.listConnectionStrings().connectionStrings[0].connectionString
  }
}

output AZURE_COSMOS_DATABASE_NAME string = cosmos::database.name
output AZURE_COSMOS_CONNECTION_STRING_KEY string = connectionStringKey

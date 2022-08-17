param location string
param resourceToken string
param tags object

var abbrs = loadJsonContent('../../../../common/infra/abbreviations.json')

var azureCosmosConnectionStringKey = 'AZURE-COSMOS-CONNECTION-STRING'

resource keyVault 'Microsoft.KeyVault/vaults@2021-10-01' existing = {
  name: '${abbrs.keyVaultVaults}${resourceToken}'
}

resource cosmosKeyVaultSecret 'Microsoft.KeyVault/vaults/secrets@2021-10-01' = {
  name: azureCosmosConnectionStringKey
  parent: keyVault
  properties: {
    value: cosmos.listConnectionStrings().connectionStrings[0].connectionString
  }
}

resource cosmos 'Microsoft.DocumentDB/databaseAccounts@2021-10-15' = {
  name: '${abbrs.documentDBDatabaseAccounts}${resourceToken}'
  kind: 'MongoDB'
  location: location
  tags: tags
  properties: {
    consistencyPolicy: {
      defaultConsistencyLevel: 'Session'
    }
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
    apiProperties: {
      serverVersion: '4.0'
    }
    capabilities: [
      {
        name: 'EnableServerless'
      }
    ]
  }

  resource database 'mongodbDatabases' = {
    name: 'Todo'
    properties: {
      resource: {
        id: 'Todo'
      }
    }

    resource list 'collections' = {
      name: 'TodoList'
      properties: {
        resource: {
          id: 'TodoList'
          shardKey: {
            _id: 'Hash'
          }
          indexes: [
            {
              key: {
                keys: [
                  '_id'
                ]
              }
            }
          ]
        }
      }
    }

    resource item 'collections' = {
      name: 'TodoItem'
      properties: {
        resource: {
          id: 'TodoItem'
          shardKey: {
            _id: 'Hash'
          }
          indexes: [
            {
              key: {
                keys: [
                  '_id'
                ]
              }
            }
          ]
        }
      }
    }
  }
}

output AZURE_COSMOS_CONNECTION_STRING_KEY string = azureCosmosConnectionStringKey
output AZURE_COSMOS_DATABASE_NAME string = cosmos::database.name

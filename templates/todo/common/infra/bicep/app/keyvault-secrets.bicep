param name string
param location string
param tags object
param principalId string
param connectionStringKey string
param resourceGroupName string
param cosmosAccountName string

module keyVault 'br/public:avm/res/key-vault/vault:0.3.5' = {
  name: 'keyvault-secrets'
  params: {
    name: name
    location: location
    tags: tags
    enableRbacAuthorization: false
    accessPolicies: [
      {
        objectId: principalId
        permissions: {
          secrets: [ 'get', 'list' ]
        }
      }
    ]
    secrets: {
      secureList: [
        {
          attributesExp: 1702648632
          attributesNbf: 10000
          contentType: 'Something'
          name: connectionStringKey
          value: listConnectionStrings(resourceId(subscription().subscriptionId, resourceGroupName, 'Microsoft.DocumentDB/databaseAccounts', cosmosAccountName), '2022-08-15').connectionStrings[0].connectionString
        }
      ]
    }
  }
}

output name string = keyVault.outputs.name
output uri string = keyVault.outputs.uri

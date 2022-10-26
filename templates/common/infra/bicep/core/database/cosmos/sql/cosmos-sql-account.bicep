param name string
param location string = resourceGroup().location
param tags object = {}

param keyVaultName string

module cosmos '../../cosmos/cosmos-account.bicep' = {
  name: 'cosmos-account'
  params: {
    name: name
    location: location
    tags: tags
    keyVaultName: keyVaultName
    kind: 'GlobalDocumentDB'
  }
}

output endpoint string = cosmos.outputs.endpoint
output connectionStringKey string = cosmos.outputs.connectionStringKey
output resourceId string = cosmos.outputs.resourceId

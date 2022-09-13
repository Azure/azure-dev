param environmentName string
param location string = resourceGroup().location
param keyVaultName string

module cosmos 'cosmos.bicep' = {
  name: 'cosmos-account-resources'
  params: {
    environmentName: environmentName
    location: location
    keyVaultName: keyVaultName
    kind: 'GlobalDocumentDB'
  }
}

output AZURE_COSMOS_RESOURCE_ID string = cosmos.outputs.AZURE_COSMOS_RESOURCE_ID
output AZURE_COSMOS_ENDPOINT string = cosmos.outputs.AZURE_COSMOS_ENDPOINT
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmos.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY

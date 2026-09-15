targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the azd environment.')
param environmentName string

@description('Primary location for all resources.')
param location string

@description('Object ID of the user or service principal running azd.')
param principalId string

var tags = {
  'azd-env-name': environmentName
}

resource resourceGroup 'Microsoft.Resources/resourceGroups@2024-03-01' = {
  name: 'rg-${environmentName}'
  location: location
  tags: tags
}

module resources 'resources.bicep' = {
  name: 'resources'
  scope: resourceGroup
  params: {
    environmentName: environmentName
    location: location
    principalId: principalId
    tags: tags
  }
}

output AZURE_CONTAINER_REGISTRY_ENDPOINT string = resources.outputs.AZURE_CONTAINER_REGISTRY_ENDPOINT
output AZURE_CONTAINER_REGISTRY_NAME string = resources.outputs.AZURE_CONTAINER_REGISTRY_NAME
output AZURE_FUNCTION_URI string = resources.outputs.AZURE_FUNCTION_URI

targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param name string

@minLength(1)
@description('Primary location for all resources')
param location string

@description('Id of the user or app to assign application roles')
param principalId string = ''

var resourceToken = toLower(uniqueString(subscription().id, name, location))

var tags = {
  'azd-env-name': name
}

resource resourceGroup 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: '${name}-rg'
  location: location
  tags: tags
}

module resources './resources.bicep' = {
  name: 'resources-${resourceToken}'
  scope: resourceGroup
  params: {
    location: location
    principalId: principalId
    resourceToken: resourceToken
    tags: tags
  }
}

output AZURE_COSMOS_ENDPOINT string = resources.outputs.AZURE_COSMOS_ENDPOINT
output AZURE_COSMOS_DATABASE_NAME string = resources.outputs.AZURE_COSMOS_DATABASE_NAME
output REACT_APP_WEB_BASE_URL string = resources.outputs.WEB_URI
output REACT_APP_API_BASE_URL string = resources.outputs.API_URI
output REACT_APP_APPLICATIONINSIGHTS_CONNECTION_STRING string = resources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output AZURE_LOCATION string = location

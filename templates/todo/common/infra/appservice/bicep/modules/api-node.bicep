param environmentName string
param location string = resourceGroup().location

module apiResources 'api.bicep' = {
  name: 'api-appservice-node-resources'
  params: {
    environmentName: environmentName
    location: location
    linuxFxVersion: 'NODE|16-lts'
  }
}

output API_PRINCIPAL_ID string = apiResources.outputs.API_PRINCIPAL_ID
output API_URI string = apiResources.outputs.API_URI

param environmentName string
param location string = resourceGroup().location
param cosmosEndpoint string = ''

module apiResources 'api.bicep' = {
  name: 'api-appservice-dotnet-resources'
  params: {
    environmentName: environmentName
    location: location
    cosmosEndpoint: cosmosEndpoint
    linuxFxVersion: 'DOTNETCORE|6.0'
    scmDoBuildDuringDeployment: false
  }
}

output API_PRINCIPAL_ID string = apiResources.outputs.API_PRINCIPAL_ID
output API_URI string = apiResources.outputs.API_URI

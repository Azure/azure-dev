@description('The location for the resource(s) to be deployed.')
param location string = resourceGroup().location

resource appHostInfrastructure_acr 'Microsoft.ContainerRegistry/registries@2025-04-01' = {
  name: take('appHostInfrastructureacr${uniqueString(resourceGroup().id)}', 50)
  location: location
  sku: {
    name: 'Basic'
  }
  tags: {
    'aspire-resource-name': 'appHostInfrastructure-acr'
  }
}

output name string = appHostInfrastructure_acr.name

output loginServer string = appHostInfrastructure_acr.properties.loginServer

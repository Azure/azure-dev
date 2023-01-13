targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param environmentName string

@description('Primary location for all resources')
param location string

@description('A time to mark on created resource groups, so they can be cleaned up via an automated process.')
param deleteAfterTime string = dateTimeAdd(utcNow('o'), 'PT1H')

@description('If true, a dummy container app instance is created during infrastructure provisioning. Otherwise, the container app instance is created during deploy.')
param provisionContainerApp string = 'false'

var tags = { 'azd-env-name': environmentName, DeleteAfter: deleteAfterTime }

resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: 'rg-${environmentName}'
  location: location
  tags: tags
}

module resources 'resources.bicep' = {
  name: 'resources'
  scope: rg
  params: {
    environmentName: environmentName
    location: location
  }
}


module web 'web.bicep' = if(provisionContainerApp == 'true') {
  name: 'web'
  scope: rg
  params: {
    containerRegistryName: resources.outputs.containerRegistryName
    containerAppsEnvironmentName: resources.outputs.containerAppsEnvironmentName
    environmentName: environmentName
    location: location
    imageName: 'nginx:latest'
  }
}

output AZURE_CONTAINER_REGISTRY_NAME string = resources.outputs.containerRegistryName
output AZURE_CONTAINER_ENVIRONMENT_NAME string = resources.outputs.containerAppsEnvironmentName
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = resources.outputs.containerRegistryloginServer
output WEBSITE_URL string = provisionContainerApp == 'true' ? web.outputs.WEBSITE_URL : ''

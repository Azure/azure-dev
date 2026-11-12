targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the environment used to generate unique resource names.')
param environmentName string

@description('Primary location for all resources.')
param location string

@description('A time to mark on the resource group so it can be cleaned up automatically.')
param deleteAfterTime string = dateTimeAdd(utcNow('o'), 'PT1H')

var tags = {
  'azd-env-name': environmentName
  DeleteAfter: deleteAfterTime
}
resource resourceGroup 'Microsoft.Resources/resourceGroups@2021-04-01' = {
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
  }
}

output AZURE_CONTAINER_APPS_ENVIRONMENT_NAME string = resources.outputs.containerAppsEnvironmentName
output WEBSITE_URL string = resources.outputs.websiteUrl

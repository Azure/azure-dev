param environmentName string
param location string = resourceGroup().location

param containerAppsEnvironmentName string = ''
param containerAppsGroupName string = 'app'
param containerRegistryName string = ''
param logAnalyticsWorkspaceName string = ''

module containerAppsEnvironment 'container-apps-environment.bicep' = {
  name: 'container-apps-environment-resources-${containerAppsGroupName}'
  params: {
    environmentName: environmentName
    location: location
    containerAppsEnvironmentName: containerAppsEnvironmentName
    logAnalyticsWorkspaceName: logAnalyticsWorkspaceName
  }
}

module containerRegistry 'container-registry.bicep' = {
  name: 'container-registry-resources-${containerAppsGroupName}'
  params: {
    environmentName: environmentName
    location: location
    containerRegistryName: containerRegistryName
  }
}

output containerAppsEnvironmentName string = containerAppsEnvironment.outputs.containerAppsEnvironmentName
output containerRegistryEndpoint string = containerRegistry.outputs.containerRegistryEndpoint
output containerRegistryName string = containerRegistry.outputs.containerRegistryName

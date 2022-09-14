param environmentName string
param location string = resourceGroup().location
param principalId string = ''
param apiImageName string = ''
param webImageName string = ''

// Container apps host (including container registry)
module containerApps '../../../../../../common/infra/bicep/core/host/container-apps.bicep' = {
  name: 'container-apps-resources'
  params: {
    environmentName: environmentName
    location: location
    logAnalyticsWorkspaceName: monitoring.outputs.logAnalyticsWorkspaceName
  }
}

// Web frontend
module web '../../../../../common/infra/bicep/app/web-containerapp.bicep' = {
  name: 'web-resources'
  params: {
    environmentName: environmentName
    location: location
    imageName: webImageName
    apiName: api.outputs.apiName
    applicationInsightsName: monitoring.outputs.applicationInsightsName
    containerAppsEnvironmentName: containerApps.outputs.containerAppsEnvironmentName
    containerRegistryName: containerApps.outputs.containerRegistryName
  }
}

// Api backend
module api '../../../../../common/infra/bicep/app/api-containerapp.bicep' = {
  name: 'api-resources'
  params: {
    environmentName: environmentName
    location: location
    imageName: apiImageName
    applicationInsightsName: monitoring.outputs.applicationInsightsName
    containerAppsEnvironmentName: containerApps.outputs.containerAppsEnvironmentName
    containerRegistryName: containerApps.outputs.containerRegistryName
    keyVaultName: keyVault.outputs.keyVaultName
  }
}

// Application database
module cosmos '../../../../../common/infra/bicep/app/cosmos-mongo.bicep' = {
  name: 'cosmos-resources'
  params: {
    environmentName: environmentName
    location: location
    keyVaultName: keyVault.outputs.keyVaultName
  }
}

// Store secrets in a keyvault
module keyVault '../../../../../../common/infra/bicep/core/security/keyvault.bicep' = {
  name: 'keyvault-resources'
  params: {
    environmentName: environmentName
    location: location
    principalId: principalId
  }
}

// Monitor application with Azure Monitor
module monitoring '../../../../../../common/infra/bicep/core/monitor/monitoring.bicep' = {
  name: 'monitoring-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

output apiUri string = api.outputs.apiUri
output applicationInsightsConnectionString string = monitoring.outputs.applicationInsightsConnectionString
output containerRegistryEndpoint string = containerApps.outputs.containerAppsEnvironmentName
output containerRegistryName string = containerApps.outputs.containerRegistryName
output cosmosConnectionStringKey string = cosmos.outputs.cosmosConnectionStringKey
output cosmosDatabaseName string = cosmos.outputs.cosmosDatabaseName
output keyVaultEndpoint string = keyVault.outputs.keyVaultEndpoint
output webUri string = web.outputs.webUri

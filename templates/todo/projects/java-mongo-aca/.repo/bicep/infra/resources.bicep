param environmentName string
param location string = resourceGroup().location
param principalId string = ''
param apiImageName string = ''
param webImageName string = ''

// Container apps host (including container registry)
module containerApps '../../../../../../common/infra/bicep/core/host/container-apps.bicep' = {
  name: 'container-apps'
  params: {
    environmentName: environmentName
    location: location
    logAnalyticsWorkspaceName: monitoring.outputs.logAnalyticsWorkspaceName
  }
}

// Web frontend
module web '../../../../../common/infra/bicep/app/web-container-app.bicep' = {
  name: 'web'
  params: {
    environmentName: environmentName
    location: location
    imageName: webImageName
    apiName: api.outputs.API_NAME
    applicationInsightsName: monitoring.outputs.applicationInsightsName
    containerAppsEnvironmentName: containerApps.outputs.containerAppsEnvironmentName
    containerRegistryName: containerApps.outputs.containerRegistryName
  }
}

// Api backend
module api '../../../../../common/infra/bicep/app/api-container-app.bicep' = {
  name: 'api'
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
module cosmos '../../../../../common/infra/bicep/app/cosmos-mongo-db.bicep' = {
  name: 'cosmos'
  params: {
    environmentName: environmentName
    location: location
    keyVaultName: keyVault.outputs.keyVaultName
  }
}

// Store secrets in a keyvault
module keyVault '../../../../../../common/infra/bicep/core/security/keyvault.bicep' = {
  name: 'keyvault'
  params: {
    environmentName: environmentName
    location: location
    principalId: principalId
  }
}

// Monitor application with Azure Monitor
module monitoring '../../../../../../common/infra/bicep/core/monitor/monitoring.bicep' = {
  name: 'monitoring'
  params: {
    environmentName: environmentName
    location: location
  }
}

output API_URI string = api.outputs.API_URI
output APPLICATIONINSIGHTS_CONNECTION_STRING string = monitoring.outputs.applicationInsightsConnectionString
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = containerApps.outputs.containerRegistryEndpoint
output AZURE_CONTAINER_REGISTRY_NAME string = containerApps.outputs.containerRegistryName
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmos.outputs.cosmosConnectionStringKey
output AZURE_COSMOS_DATABASE_NAME string = cosmos.outputs.cosmosDatabaseName
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.keyVaultEndpoint
output WEB_URI string = web.outputs.WEB_URI

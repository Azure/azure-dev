param environmentName string
param location string = resourceGroup().location
param principalId string = ''
param apiImageName string = ''
param webImageName string = ''

module containerAppsEnvironment '../../../../../../common/infra/bicep/core/host/container-apps-environment.bicep' = {
  name: 'container-apps-environment-resources'
  params: {
    environmentName: environmentName
    location: location
    logAnalyticsWorkspaceName: monitoring.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_NAME
  }
}

module containerRegistry '../../../../../../common/infra/bicep/core/host/container-registry.bicep' = {
  name: 'container-registry-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

module web '../../../../../common/infra/bicep/app/web-containerapp.bicep' = {
  name: 'web-resources'
  params: {
    environmentName: environmentName
    location:location
    imageName: webImageName != '' ? webImageName : 'nginx:latest'
  }
  dependsOn: [
    containerAppsEnvironment
    containerRegistry
  ]
}

module api '../../../../../common/infra/bicep/app/api-containerapp.bicep' = {
  name: 'api-resources'
  params: {
    environmentName: environmentName
    location:location
    imageName: apiImageName != '' ? apiImageName : 'nginx:latest' 
  }
  dependsOn: [
    containerAppsEnvironment
    containerRegistry
  ]
}

// The application database
module cosmos '../../../../../common/infra/bicep/app/cosmos-mongo.bicep' = {
  name: 'cosmos-resources'
  params: {
    environmentName: environmentName
    location: location
    keyVaultName: keyVault.outputs.AZURE_KEY_VAULT_NAME
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

output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmos.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY
output AZURE_COSMOS_DATABASE_NAME string = cosmos.outputs.AZURE_COSMOS_DATABASE_NAME
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.AZURE_KEY_VAULT_ENDPOINT
output APPLICATIONINSIGHTS_CONNECTION_STRING string = monitoring.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = containerRegistry.outputs.AZURE_CONTAINER_REGISTRY_ENDPOINT
output AZURE_CONTAINER_REGISTRY_NAME string = containerRegistry.outputs.AZURE_CONTAINER_REGISTRY_NAME
output WEB_URI string = web.outputs.WEB_URI
output API_URI string = api.outputs.API_URI

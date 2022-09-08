param environmentName string
param location string = resourceGroup().location
param principalId string = ''
param apiImageName string = ''
param webImageName string = ''

module acaResources '../../../../../common/infra/bicep/core/aca.bicep' = {
  name: 'aca-resources'
  params: {
    environmentName: environmentName
    location: location
  }
  dependsOn: [
    logAnalyticsWorkspaceResources
  ]
}

module acrResources '../../../../../common/infra/bicep/core/acr.bicep' = {
  name: 'acr-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

module apiResources 'modules/api.bicep' = {
  name: 'api-resources'
  params: {
    environmentName: environmentName
    location: location
    imageName: apiImageName != '' ? apiImageName : 'nginx:latest'
  }
  dependsOn: [
    acaResources
    acrResources
    applicationInsightsResources
    keyVaultResources
  ]
}

module webResources 'modules/web.bicep' = {
  name: 'web-resources'
  params: {
    environmentName: environmentName
    location: location
    imageName: webImageName != '' ? webImageName : 'nginx:latest'
  }
  dependsOn: [
    acaResources
    acrResources
    applicationInsightsResources
    keyVaultResources
    apiResources
  ]
}

module keyVaultResources '../../../../../common/infra/bicep/core/keyvault.bicep' = {
  name: 'keyvault-resources'
  params: {
    environmentName: environmentName
    location: location
    principalId: principalId
  }
}

module applicationInsightsResources '../../../../../common/infra/bicep/core/applicationinsights.bicep' = {
  name: 'applicationinsights-resources'
  params: {
    environmentName: environmentName
    location: location
    workspaceId: logAnalyticsWorkspaceResources.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_ID
  }
}

module logAnalyticsWorkspaceResources '../../../../../common/infra/bicep/core/loganalytics.bicep' = {
  name: 'loganalytics-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

module cosmosResources '../../../../../../templates/todo/common/infra/core/cosmos.bicep' = {
  name: 'cosmos-resources'
  params: {
    environmentName: environmentName
    location: location
  }
  dependsOn: [
    keyVaultResources
  ]
}

output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmosResources.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY
output AZURE_COSMOS_DATABASE_NAME string = cosmosResources.outputs.AZURE_COSMOS_DATABASE_NAME
output AZURE_KEY_VAULT_ENDPOINT string = keyVaultResources.outputs.AZURE_KEY_VAULT_ENDPOINT
output APPLICATIONINSIGHTS_CONNECTION_STRING string = applicationInsightsResources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = acrResources.outputs.AZURE_CONTAINER_REGISTRY_ENDPOINT
output AZURE_CONTAINER_REGISTRY_NAME string = acrResources.outputs.AZURE_CONTAINER_REGISTRY_NAME
output WEB_URI string = webResources.outputs.WEB_URI
output API_URI string = apiResources.outputs.API_URI

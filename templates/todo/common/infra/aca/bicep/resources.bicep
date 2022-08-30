param name string
param location string
param principalId string = ''
param resourceToken string
param tags object
param apiImageName string = ''
param webImageName string = ''

module acaResources '../../../../../common/infra/bicep/modules/aca.bicep' = {
  name: 'aca-resources'
  params: {
    location: location
    resourceToken: resourceToken
    tags: tags
  }
  dependsOn: [
    logAnalyticsWorkspaceResources
  ]
}

module acrResources '../../../../../common/infra/bicep/modules/acr.bicep' = {
  name: 'acr-resources'
  params: {
    location: location
    resourceToken: resourceToken
    tags: tags
  }
}

module apiResources 'modules/api.bicep' = {
  name: 'api-resources'
  params: {
    name: name
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
    name: name
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

module keyVaultResources '../../../../../common/infra/bicep/modules/keyvault.bicep' = {
  name: 'keyvault-resources'
  params: {
    location: location
    principalId: principalId
    resourceToken: resourceToken
    tags: tags
  }
}

module applicationInsightsResources '../../../../../common/infra/bicep/modules/applicationinsights.bicep' = {
  name: 'applicationinsights-resources'
  params: {
    location: location
    resourceToken: resourceToken
    tags: tags
    workspaceId: logAnalyticsWorkspaceResources.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_ID
  }
}

module logAnalyticsWorkspaceResources '../../../../../common/infra/bicep/modules/loganalytics.bicep' = {
  name: 'loganalytics-resources'
  params: {
    location: location
    resourceToken: resourceToken
    tags: tags
  }
}

module cosmosResources '../../../../../../templates/todo/common/infra/modules/cosmos.bicep' = {
  name: 'cosmos-resources'
  params: {
    location: location
    resourceToken: resourceToken
    tags: tags
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

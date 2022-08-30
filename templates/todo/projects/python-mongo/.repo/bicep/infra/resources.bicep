param location string
param principalId string = ''
param resourceToken string
param tags object

module appServicePlanResources '../../../../../../common/infra/bicep/modules/appserviceplan.bicep' = {
  name: 'appserviceplan-resources'
  params: {
    location: location
    resourceToken: resourceToken
    tags: tags
  }
}

module webResources '../../../../../../todo/common/infra/appservice/bicep/modules/web.bicep' = {
  name: 'web-resources'
  params: {
    location: location
    resourceToken: resourceToken
    tags: tags
  }
  dependsOn: [
    applicationInsightsResources
    appServicePlanResources
  ]
}

module apiResources '../../../../../../todo/common/infra/appservice/bicep/modules/api.bicep' = {
  name: 'api-resources'
  params: {
    location: location
    resourceToken: resourceToken
    tags: tags
    cosmosDatabaseName: cosmosResources.outputs.AZURE_COSMOS_DATABASE_NAME
    cosmosConnectionStringKey: cosmosResources.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY
    linuxFxVersion: 'PYTHON|3.8'
    appCommandLine: 'gunicorn --workers 4 --threads 2 --timeout 60 --access-logfile "-" --error-logfile "-" --bind=0.0.0.0:8000 -k uvicorn.workers.UvicornWorker todo.app:app'
  }
  dependsOn: [
    applicationInsightsResources
    keyVaultResources
    appServicePlanResources
  ]
}

module keyVaultResources '../../../../../../common/infra/bicep/modules/keyvault.bicep' = {
  name: 'keyvault-resources'
  params: {
    location: location
    principalId: principalId
    resourceToken: resourceToken
    tags: tags
  }
}

module cosmosResources '../../../../../common/infra/modules/cosmos.bicep' = {
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

module logAnalyticsWorkspaceResources '../../../../../../common/infra/bicep/modules/loganalytics.bicep' = {
  name: 'loganalytics-resources'
  params: {
    location: location
    resourceToken: resourceToken
    tags: tags
  }
}

module applicationInsightsResources '../../../../../../common/infra/bicep/modules/applicationinsights.bicep' = {
  name: 'applicationinsights-resources'
  params: {
    location: location
    resourceToken: resourceToken
    tags: tags
    workspaceId: logAnalyticsWorkspaceResources.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_ID
  }
}

output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmosResources.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY
output AZURE_COSMOS_DATABASE_NAME string = cosmosResources.outputs.AZURE_COSMOS_DATABASE_NAME
output AZURE_KEY_VAULT_ENDPOINT string = keyVaultResources.outputs.AZURE_KEY_VAULT_ENDPOINT
output APPLICATIONINSIGHTS_CONNECTION_STRING string = applicationInsightsResources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output WEB_URI string = webResources.outputs.WEB_URI
output API_URI string = apiResources.outputs.API_URI
